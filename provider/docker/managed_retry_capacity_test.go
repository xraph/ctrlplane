//go:build linux || darwin

package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/managed"
)

func largeRetryCustodyFixture(t *testing.T) ManagedDockerRequest {
	t.Helper()
	request := retryCustodyFixture(t, managed.StepRemoveContainer, 1)

	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	// Widen actual token fields to their 256-byte contract limit, preserving exact
	// repeated identities. No ignored padding or extra protocol field is introduced.
	for _, value := range []string{"tenant", "worker", "host", "project", "generation"} {
		raw = bytes.ReplaceAll(raw, []byte(`"`+value+`"`), []byte(`"`+strings.Repeat(value[:1], 256)+`"`))
	}

	if err = json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}

	request.Binding.Attestation = bytes.Repeat([]byte{0xff}, managed.MaxProviderEffectAttestation)
	request.Binding.Producer.ProducerID = strings.Repeat("p", 256)
	request.Binding.Producer.VerifierID = strings.Repeat("v", 256)
	e := request.Retry
	retryCustodyRedigest(t, &e.Root)
	e.Permission.Facts.RootDigest = e.Root.Digest
	retryCustodyRedigest(t, &e.Permission)
	e.Authorization.Facts.RootDigest = e.Root.Digest

	e.Authorization.Facts.Permission, err = managed.RecoveryAssociationFor(e.Authorization.Facts.Permission.Key, e.Permission)
	if err != nil {
		t.Fatal(err)
	}

	e.Authorization.Facts.Previous.RootDigest = e.Root.Digest

	e.Authorization.Facts.Previous.Authority, err = managed.RecoveryAssociationFor(e.Authorization.Facts.Previous.Authority.Key, e.Root)
	if err != nil {
		t.Fatal(err)
	}

	e.Authorization.Facts.Previous.Digest, err = managed.RetryPreviousDigest(e.Authorization.Facts.Previous)
	if err != nil {
		t.Fatal(err)
	}

	retryCustodyRedigest(t, &e.Authorization)
	e.Marker.Facts.RootDigest = e.Root.Digest

	permission, err := request.Permission.Value()
	if err != nil {
		t.Fatal(err)
	}

	raw, err = json.Marshal(permission)
	if err != nil {
		t.Fatal(err)
	}

	e.Marker.Facts.PermissionDigest = managed.Digest(raw)
	retryCustodyRedigest(t, &e.Marker)

	request.Invocation = &e.Permission.Facts
	if err = validateManagedDockerRetry(request); err != nil {
		t.Fatal("large composed retry", err)
	}

	if err = managed.ValidateProviderEffectBindingFacts(*request.Binding); err != nil {
		t.Fatal("large original evidence", err)
	}

	return request
}

func TestManagedDockerRetryLargeComposedEvidence(t *testing.T) {
	request := largeRetryCustodyFixture(t)
	authority := retryCustodyTrust(request)
	journal, _ := custodyJournalFixture(t)
	transport := &custodyTransportFixture{}
	executor := retryCustodyExecutor(t, journal, transport, authority)

	prepared, err := executor.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal("large prepare", err)
	}

	raw, err := custodyBytes(prepared)
	if err != nil || len(raw) <= 16384 || len(raw) > ManagedDockerJournalLimit {
		t.Fatal("large complete envelope size", len(raw), err)
	}

	t.Logf("composed retry prepared bytes=%d limit=%d", len(raw), ManagedDockerJournalLimit)

	complete, err := executor.Execute(t.Context(), request)
	if err != nil || complete.State != "complete" {
		t.Fatal("large execution", err)
	}

	raw, err = custodyBytes(complete)
	if err != nil || len(raw) > ManagedDockerJournalLimit {
		t.Fatal("complete custody exceeded cap", len(raw), err)
	}

	t.Logf("composed retry completed bytes=%d", len(raw))

	if _, err = executor.Execute(t.Context(), request); err != nil || transport.calls.Load() != 1 || authority.admits != 1 {
		t.Fatal("large evidence replay", err)
	}
}

type retryCapacityAuthority struct {
	*retryCustodyAuthority

	verified int
}

func (a *retryCapacityAuthority) VerifyRequest(ctx context.Context, request ManagedDockerRequest) error {
	a.verified++

	return a.retryCustodyAuthority.VerifyRequest(ctx, request)
}

// requestAtEncodedSize builds a byte-envelope boundary case. Its enlarged
// attestation intentionally violates the independent evidence cap too; it is not
// accepted native evidence. Global size rejection must precede every callback.
func requestAtEncodedSize(t *testing.T, size int) ManagedDockerRequest {
	t.Helper()

	original := largeRetryCustodyFixture(t)
	for width := 1; width <= 4; width++ {
		request := custodyClone(original)
		request.Binding.Producer.ProducerID = strings.Repeat("p", width)
		request.Binding.Attestation = nil

		base, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		// nil encodes null (4 bytes); a non-nil byte slice encodes quoted base64.
		estimate := (size - len(base) + 2) / 4 * 3
		for n := estimate - 6; n <= estimate+6; n++ {
			if n < 0 {
				continue
			}

			request.Binding.Attestation = bytes.Repeat([]byte{1}, n)

			raw, marshalErr := json.Marshal(request)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}

			if len(raw) == size {
				return request
			}
		}
	}

	t.Fatal("could not construct exact encoded boundary")

	return ManagedDockerRequest{}
}

func TestManagedDockerRetryEncodedCapRefusesBeforeDelegation(t *testing.T) {
	exact := requestAtEncodedSize(t, ManagedDockerJournalLimit)
	if raw, err := custodyBytes(exact); err != nil || len(raw) != ManagedDockerJournalLimit {
		t.Fatal("encoded cap", len(raw), err)
	}

	oversized := requestAtEncodedSize(t, ManagedDockerJournalLimit+1)
	if _, err := custodyBytes(oversized); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("encoded cap+1", err)
	}

	authority := &retryCapacityAuthority{retryCustodyAuthority: retryCustodyTrust(oversized)}
	journal, _ := custodyJournalFixture(t)
	transport := &custodyTransportFixture{}

	executor, err := NewManagedDockerExecutor(journal, transport, authority, authority, "engine-A")
	if err != nil {
		t.Fatal(err)
	}

	if _, err = executor.Execute(t.Context(), oversized); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("oversized retry reached delegation", err)
	}

	if transport.calls.Load() != 0 || authority.admits != 0 || authority.verified != 0 {
		t.Fatal("oversized evidence reached a callback")
	}

	if raw, readErr := journal.Load(t.Context(), custodyKey(oversized.Permission)); readErr != nil || len(raw) != 0 {
		t.Fatal("oversized evidence wrote journal", readErr)
	}
}
