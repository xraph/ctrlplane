//go:build linux || darwin

package docker

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

func TestManagedDockerIndependentProvisionReceipt(t *testing.T) {
	for _, kind := range []managed.ProviderStepKind{managed.StepCreateNetwork, managed.StepCreateContainer, managed.StepStartContainer} {
		t.Run(string(kind), func(t *testing.T) {
			for _, delta := range []time.Duration{-time.Millisecond, time.Millisecond} {
				request := custodyRequestFixture(kind)
				request.Receipt.Digest = strings.Repeat("9", 64)
				request.Receipt.AcceptedAt = request.Permission.IssuedAt.Add(delta)
				journal, _ := custodyJournalFixture(t)
				authority := &custodyAuthorityFixture{requests: map[string]ManagedDockerRequest{}}
				authority.trust(request)

				transport := &custodyTransportFixture{}

				executor := custodyExecutorFixture(t, journal, transport, authority)
				if _, err := executor.Execute(t.Context(), request); err != nil {
					t.Fatal("independent retained receipt refused", err)
				}

				replay := custodyExecutorFixture(t, journal, transport, authority)
				if _, err := replay.Execute(t.Context(), request); err != nil {
					t.Fatal("receipt replay", err)
				}

				if transport.calls.Load() != 1 {
					t.Fatal("replay repeated transport")
				}

				for _, mutate := range []func(*ManagedDockerRequest){
					func(r *ManagedDockerRequest) { r.Receipt.Digest = strings.Repeat("8", 64) },
					func(r *ManagedDockerRequest) { r.Receipt.AcceptedAt = r.Receipt.AcceptedAt.Add(time.Microsecond) },
					func(r *ManagedDockerRequest) { r.Permission.Digest = strings.Repeat("7", 64) },
				} {
					altered := custodyClone(request)
					mutate(&altered)

					if _, err := replay.Execute(t.Context(), altered); !errors.Is(err, managed.ErrAuthority) {
						t.Fatal("untrusted receipt bypassed authority", err)
					}
				}

				if transport.calls.Load() != 1 {
					t.Fatal("tampered receipt reached transport")
				}
			}
		})
	}
}
