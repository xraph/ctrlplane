//go:build linux || darwin

package docker

import (
	"errors"
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/managed"
)

func TestManagedDockerIndependentRemovalReceipt(t *testing.T) {
	for _, kind := range []managed.ProviderStepKind{managed.StepRemoveContainer, managed.StepRemoveNetwork} {
		t.Run(string(kind), func(t *testing.T) {
			request := custodyRequestFixture(kind)
			request.Receipt.Digest = strings.Repeat("9", 64)
			request.Binding.Original.Receipt = request.Receipt
			authority := &custodyAuthorityFixture{requests: map[string]ManagedDockerRequest{}}
			authority.trust(request)

			journal, directory := custodyJournalFixture(t)
			transport := &custodyTransportFixture{}

			executor := custodyExecutorFixture(t, journal, transport, authority)
			if _, err := executor.Execute(t.Context(), request); err != nil {
				t.Fatal("independent original receipt refused", err)
			}

			reopened, err := OpenFileManagedDockerJournal(directory)
			if err != nil {
				t.Fatal(err)
			}

			replay := custodyExecutorFixture(t, reopened, transport, authority)
			if _, err = replay.Execute(t.Context(), request); err != nil {
				t.Fatal("historical receipt replay", err)
			}

			if transport.calls.Load() != 1 {
				t.Fatal("reopen repeated transport")
			}

			altered := custodyClone(request)
			altered.Receipt.Digest = strings.Repeat("8", 64)
			altered.Binding.Original.Receipt = altered.Receipt
			// Both submitted copies agree, but independent retained custody does not.
			if _, err = replay.Execute(t.Context(), altered); !errors.Is(err, managed.ErrAuthority) {
				t.Fatal("coordinated receipt substitution accepted", err)
			}

			if transport.calls.Load() != 1 {
				t.Fatal("untrusted receipt reached transport")
			}
		})
	}
}
