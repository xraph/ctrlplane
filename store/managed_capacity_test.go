package store_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/memory"
)

// maximumManagedDocument includes mutually exclusive state, longest enum/boolean encodings and timezone offsets.
func maximumManagedDocument() managed.Target {
	text := strings.Repeat("Z", 256)
	opaque := bytes.Repeat([]byte{255}, managed.MaxEvidence)
	stamp := time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.FixedZone("maximum-offset", 23*60*60+59*60))
	maxCounter := ^uint64(0)
	i := managedIdentity()
	i.TenantID = text
	i.Physical = managed.Physical{Provider: text, Placement: text, Project: text, Generation: text}
	i.Manifest.Spec = opaque
	i.Manifest.SpecDigest = managed.Digest(opaque)
	i.Manifest.Version = maxCounter
	i.Manifest.Members = nil

	for n := range managed.MaxMembers {
		name := strings.Repeat("Z", 253) + fmt.Sprintf("%03d", n)
		// Permitted ASCII identifiers contain no JSON-escaped characters.
		i.Manifest.Members = append(i.Manifest.Members, managed.Member{Name: name, Service: text, OwnerID: id.New(id.Prefix(strings.Repeat("a", 63))), Installation: text, Namespace: text, Queue: text, Build: text, ArtifactDigest: managed.Digest(opaque), ConfigurationDigest: managed.Digest(opaque)})
	}

	replacement := i
	replacement.InstanceID = id.New(id.PrefixInstance)

	t := managed.Target{TerminalDisposition: managed.NoResourcesCreated, Provision: managed.Provision{Issued: false, Complete: false, Revoked: false, Receipt: opaque}, ReplacementOperationID: id.New(managed.PrefixOperation), Identity: i, CreatedAt: stamp, Revision: maxCounter, Desired: managed.Retiring, DesiredRevision: maxCounter, Phase: managed.DeleteIssued, Seal: maxCounter, LeaseOwner: text, LeaseEpoch: maxCounter, LeaseUntil: stamp, Replacement: &replacement, Resources: map[string]string{}}
	for n := range managed.MaxMembers + 1 {
		t.Resources[strings.Repeat("Z", 254)+string(rune('A'+n/10))+string(rune('0'+n%10))] = text
	}

	for range managed.MaxReservations {
		survivors := make([]string, managed.MaxReservations)
		for n := range survivors {
			survivors[n] = text
		}

		t.Reservations = append(t.Reservations, managed.Reservation{Registration: managed.Registration{ID: id.New(managed.PrefixReservation), Member: text, RuntimeID: text, InstanceID: text, HostID: text, RequestID: text, Command: opaque, CommandDigest: managed.Digest(opaque)}, Phase: managed.Revoked, Receipt: opaque, Unknown: false, Fence: &managed.Fence{CompatibilityEvidence: opaque, Epoch: maxCounter, Evidence: opaque, Digest: managed.Digest(opaque), ValidUntil: stamp, Survivors: survivors}, Drain: &managed.Drain{OperationID: text, Deadline: stamp, Complete: false, Quiescent: false, CompletedAt: stamp, Evidence: opaque}, Finished: opaque, Abort: &managed.AbortProof{FenceDigest: managed.Digest(opaque), VerifiedAt: stamp, ValidUntil: stamp, Evidence: opaque, Receipt: opaque}})
	}

	t.Deletion = &managed.Deletion{OperationID: id.New(managed.PrefixOperation), FenceDigest: managed.Digest(opaque), Verifier: text, Issued: false, IssuedAt: stamp, Settlement: &managed.Settlement{OperationID: id.New(managed.PrefixOperation), FenceDigest: managed.Digest(opaque), Disposition: managed.SettledWithoutDeletion, Verifier: text, Evidence: opaque, SettledAt: stamp}}

	return t
}
func TestManagedTerminalCapacity(t *testing.T) {
	largest := maximumManagedDocument()

	body, err := json.Marshal(largest)
	if err != nil {
		t.Fatal(err)
	}

	if len(body) >= managed.MaxDocument {
		t.Fatalf("terminal document needs %d bytes, bound %d", len(body), managed.MaxDocument)
	}

	receipt, err := json.Marshal(managed.Receipt{ID: id.New(managed.PrefixOperation), Digest: strings.Repeat("f", 64), Revision: 1<<63 - 1, LeaseEpoch: 1<<63 - 1, AcceptedAt: largest.CreatedAt, LeaseUntil: largest.LeaseUntil})
	if err != nil {
		t.Fatal(err)
	}

	if len(receipt) >= 2048 {
		t.Fatalf("receipt cannot fit: %d", len(receipt))
	}

	t.Logf("all fields retained: encoded target %d / %d bytes; receipt %d / 2048 bytes", len(body), managed.MaxDocument, len(receipt))

	s := memory.New()

	for _, invalid := range []string{strings.Repeat("\\", 256), strings.Repeat("\x01", 256), strings.Repeat("&", 256), strings.Repeat("\"", 256), string([]rune{0x65e5, 0x672c})} {
		i := managedIdentity()

		i.Physical.Project = invalid
		if _, err := s.CreateManagedTarget(t.Context(), i); !errors.Is(err, managed.ErrInvalid) {
			t.Fatalf("escaping identifier accepted: %v", err)
		}
	}
}
func TestManagedPostgresTerminalCapacity(t *testing.T) {
	s, _ := managedPG(t)

	body, err := json.Marshal(maximumManagedDocument())
	if err != nil {
		t.Fatal(err)
	}

	var size int
	if err = pgdriver.Unwrap(s.DB()).NewRaw("SELECT octet_length($1::jsonb::text)", string(body)).Scan(t.Context(), &size); err != nil {
		t.Fatal(err)
	}

	if size >= managed.MaxDocument {
		t.Fatalf("PostgreSQL serialization cannot fit: %d", size)
	}

	t.Logf("PostgreSQL canonical document %d / %d bytes", size, managed.MaxDocument)
}
