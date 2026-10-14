package store_test

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/postgres"
)

// This fixture authenticates only deterministic test exports. It qualifies SQL
// transaction behavior, not the production native producer or external custody.
type sealPostgresHost struct {
	managed.RemovalRevalidationProducer
	managed.RemovalCommitProducer
	managed.RemovalGuardCloseProducer
	managed.ProviderInvocationClosureProducer

	calls int
}

func (h *sealPostgresHost) VerifyRemovalRevalidation(context.Context, managed.RecoveryRecord[managed.RecoveryNativeIntent], managed.RemovalRevalidationFacts) error {
	h.calls++

	return nil
}
func (h *sealPostgresHost) VerifyRemovalCommit(context.Context, managed.RecoveryRecord[managed.RecoveryNativeIntent], managed.ProviderNativeCommitFacts) error {
	h.calls++

	return nil
}
func (h *sealPostgresHost) VerifyRemovalClose(context.Context, managed.RecoveryRecord[managed.RecoveryNativeIntent], managed.RecoveryNativeCloseFacts) error {
	h.calls++

	return nil
}
func (*sealPostgresHost) VerifyProviderNoDispatch(context.Context, managed.RecoveryRecord[managed.ProviderInvocationPermission], *managed.RecoveryRecord[managed.ProviderInvocationMarker], managed.ProviderInvocationFailure) error {
	return managed.ErrUnsupported
}

func sealPostgresCommand(t *testing.T, f *removalFixture, ref managed.AttemptRef) managed.RecoveryCommand {
	t.Helper()
	q := quiescenceCommand(t, f.s, f.key.Key)

	return managed.RecoveryCommand{Key: q.Key, ID: q.ID, Revision: managed.Decimal(strconv.FormatUint(q.Revision, 10)), LeaseEpoch: managed.Decimal(strconv.FormatUint(q.LeaseEpoch, 10)), Owner: q.Owner, AttemptRef: ref, Profile: managed.RemovalRecoveryProfile}
}
func sealPostgresAssociation[T any](t *testing.T, key managed.AttemptKey, record managed.RecoveryRecord[T]) managed.RecoveryAssociation {
	t.Helper()

	ref, err := managed.RecoveryRecordAssociation(key, record)
	if err != nil {
		t.Fatal(err)
	}

	return ref
}

func sealPostgresSetup(t *testing.T) (*removalFixture, *postgres.Store, *managedstate.Repository, *sealPostgresHost, managed.AttemptRef, []managed.RecoveryRecord[managed.RemovalRevalidationFacts]) {
	t.Helper()
	f := newRemovalFixture(t, "postgres")
	f.admit()

	s, ok := f.s.Authority.(*postgres.Store)
	if !ok {
		t.Fatal("fixture did not provide real PostgreSQL")
	}

	attempt, err := s.ReadManagedRemovalAttempt(t.Context(), f.key)
	if err != nil {
		t.Fatal(err)
	}

	target, err := s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil {
		t.Fatal(err)
	}

	bindings := make([]managed.RemovalRecoveryBinding, 0, len(f.reservations))
	for _, rid := range f.reservations {
		original, readErr := s.ReadManagedAttemptBinding(t.Context(), managed.AttemptBindingKey{AttemptKey: f.key, ReservationID: rid})
		if readErr != nil {
			t.Fatal(readErr)
		}

		var begin managed.AttemptCommandRecord

		for _, command := range original.Commands {
			if command.Key.Kind == managed.AttemptBegin {
				begin = command
			}
		}

		var drain managed.OriginalLifecycleReference

		for _, reservation := range target.Reservations {
			if reservation.ID == rid {
				if reservation.Drain == nil {
					t.Fatal("missing original drain")
				}

				drain, err = managed.ParseOriginalReference(reservation.Drain.Evidence)
				if err != nil {
					t.Fatal(err)
				}
			}
		}

		bindings = append(bindings, managed.RemovalRecoveryBinding{Key: original.Key, DescriptorDigest: attempt.State.AttemptRef.DescriptorDigest, ProviderOperationID: attempt.Descriptor.ProviderOperationID, OriginalRegistrationDigest: begin.BindingReference.OriginalRegistrationDigest, OriginalDrainDigest: drain.MaterialDigest, OriginalCommandDigest: original.Fence.CommandDigest, OriginalBeginRequestID: original.Fence.NativeAcceptance.RequestID, OriginalBeginRequestDigest: original.Fence.NativeAcceptance.RequestDigest, NativeFenceDigest: original.Fence.NativeFenceDigest, OriginalProcessDigest: begin.BindingReference.ProcessDigest, AdmissionDigest: attempt.State.AdmissionDigest})
	}

	host := &sealPostgresHost{}
	identity := managed.RecoveryProducerIdentity{ProducerID: "fixture", Profile: "fixture.v1", Version: "1", VerifierID: "verifier"}

	repository, err := s.WithRemovalRecovery(managed.RemovalRecoveryHost{Revalidation: host, Commit: host, Close: host, Closure: host, Verifier: host, RevalidationIdentity: identity, CommitIdentity: identity, CloseIdentity: identity, ClosureVerifierID: "verifier"})
	if err != nil {
		t.Fatal(err)
	}

	ref := attempt.State.AttemptRef
	if _, err = repository.ActivateManagedRemovalRecovery(t.Context(), managed.RemovalRecoveryActivationRequest{Command: sealPostgresCommand(t, f, ref)}); err != nil {
		t.Fatal(err)
	}

	proofs := make([]managed.RecoveryRecord[managed.RemovalRevalidationFacts], 0, len(bindings))
	for _, binding := range bindings {
		raw := []byte("postgres fixture live guard:" + binding.Key.ReservationID.String())

		intent, acceptErr := repository.RecordManagedRecoveryNativeIntent(t.Context(), managed.RecoveryNativeIntentRequest{Command: sealPostgresCommand(t, f, ref), Intent: managed.RecoveryNativeIntent{Binding: binding, Profile: managed.RemovalRecoveryProfile, Operation: "revalidate", RequestID: "live-" + binding.Key.ReservationID.String(), RequestDigest: managed.Digest(raw), Command: raw, CommandDigest: managed.Digest(raw)}})
		if acceptErr != nil {
			t.Fatal(acceptErr)
		}

		now := time.Now().UTC()
		hash := managed.Digest(raw)
		export, attestation := []byte("fixture native export"), []byte("fixture attestation")
		facts := managed.RemovalRevalidationFacts{Binding: binding, Intent: sealPostgresAssociation(t, f.key, intent), GuardID: "guard-" + binding.Key.ReservationID.String(), GuardRootDigest: hash, NativeRequestID: intent.Facts.RequestID, NativeRequestDigest: intent.Facts.RequestDigest, NativeMaterialDigest: hash, NativeReceiptDigest: hash, NativeCurrentDigest: hash, NativeScopeDigest: hash, CandidateVersion: "1", RemovalEpoch: "1", CandidateDigest: hash, BuildDigest: hash, SurvivorDigest: hash, ExclusionDigest: hash, CoverageDigest: hash, ProcessDisposition: "live", SourceObservedAt: now, NativeAcceptedAt: now, ValidUntil: now.Add(time.Hour), Export: managed.RecoveryNativeExport{Raw: export, RawDigest: managed.Digest(export), Attestation: attestation, AttestationDigest: managed.Digest(attestation), ProducerID: identity.ProducerID, ProducerProfile: identity.Profile, ProducerVersion: identity.Version, VerifierID: identity.VerifierID}}

		proof, acceptErr := repository.AcceptManagedRemovalRevalidation(t.Context(), managed.RemovalRevalidationRequest{Command: sealPostgresCommand(t, f, ref), Facts: facts})
		if acceptErr != nil {
			t.Fatal(acceptErr)
		}

		proofs = append(proofs, proof)
	}

	return f, s, repository, host, ref, proofs
}

func TestManagedRecoveryCoverageSealPostgres(t *testing.T) {
	if os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN") == "" {
		if os.Getenv("CTRLPLANE_RECOVERY_REQUIRED") == "1" {
			t.Fatal("CTRLPLANE_TEST_POSTGRES_DSN required for named PostgreSQL seal qualification")
		}

		t.Skip("CTRLPLANE_TEST_POSTGRES_DSN unset")
	}

	f, s, repository, host, ref, proofs := sealPostgresSetup(t)

	cutoff, err := repository.CutoffManagedRemovalRecovery(t.Context(), sealPostgresCommand(t, f, ref))
	if err != nil {
		t.Fatal(err)
	}

	command := sealPostgresCommand(t, f, ref)
	pg := pgdriver.Unwrap(s.DB())
	before := attemptPostgresSnapshot(t, s)
	calls := host.calls

	for _, row := range []string{string(managed.RecoveryCoverageBindingRecord), string(managed.RecoveryCoverageRecord), "removal_recovery_acceptance", "removal_attempt_event", "removal_coverage_selection", "@target", "@receipt"} {
		t.Run("rollback/"+row, func(t *testing.T) {
			table, condition := "cp_managed_controller_records", "NEW.kind='"+row+"'"
			if row == "@target" {
				table, condition = "cp_managed_targets", "true"
			}

			if row == "@receipt" {
				table, condition = "cp_managed_commands", "true"
			}

			query := "CREATE FUNCTION seal_write_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF " + condition + " THEN RAISE EXCEPTION 'injected seal write failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER seal_write_failure AFTER INSERT OR UPDATE ON " + table + " FOR EACH ROW EXECUTE FUNCTION seal_write_failure()"
			if _, err = pg.Exec(t.Context(), query); err != nil {
				t.Fatal(err)
			}

			defer func() {
				if _, dropErr := pg.Exec(t.Context(), "DROP TRIGGER seal_write_failure ON "+table+"; DROP FUNCTION seal_write_failure()"); dropErr != nil {
					t.Fatal(dropErr)
				}
			}()

			if _, sealErr := repository.SealManagedEffectiveRemovalCoverage(t.Context(), command); sealErr == nil || !strings.Contains(sealErr.Error(), "injected seal write failure") {
				t.Fatal("missing real SQL rollback failure", sealErr)
			}

			if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) || host.calls != calls {
				t.Fatal("SQL failure leaked rows, receipt, target or native callback")
			}
		})
	}

	if t.Failed() {
		t.FailNow()
	}

	coverage, err := repository.SealManagedEffectiveRemovalCoverage(t.Context(), command)
	if err != nil {
		t.Fatal("seal after PostgreSQL rollback", err)
	}

	sealed := attemptPostgresSnapshot(t, s)

	replay, err := repository.SealManagedEffectiveRemovalCoverage(t.Context(), command)
	if err != nil || !reflect.DeepEqual(replay, coverage) || !reflect.DeepEqual(sealed, attemptPostgresSnapshot(t, s)) || calls != host.calls {
		t.Fatal("PostgreSQL seal replay changed state", err)
	}

	coverageRef := sealPostgresAssociation(t, f.key, coverage)

	cutoffRef := sealPostgresAssociation(t, f.key, cutoff)
	for _, proof := range proofs {
		proofRef := sealPostgresAssociation(t, f.key, proof)
		raw := []byte("postgres native close bound to coverage:" + coverage.Digest + ":" + proof.Facts.Binding.Key.ReservationID.String())
		request := managed.RecoveryNativeIntentRequest{Command: sealPostgresCommand(t, f, ref), Intent: managed.RecoveryNativeIntent{Binding: proof.Facts.Binding, Profile: managed.RemovalRecoveryProfile, Operation: "close", RequestID: "close-" + proof.Facts.Binding.Key.ReservationID.String(), RequestDigest: managed.Digest(raw), Command: raw, CommandDigest: managed.Digest(raw), Coverage: &coverageRef, Cutoff: &cutoffRef, Proof: &proofRef}}

		accepted, acceptErr := repository.RecordManagedRecoveryNativeIntent(t.Context(), request)
		if acceptErr != nil || accepted.Facts.Coverage == nil || *accepted.Facts.Coverage != coverageRef {
			t.Fatal("PostgreSQL close source association", acceptErr)
		}

		snapshot := attemptPostgresSnapshot(t, s)

		again, readErr := repository.RecordManagedRecoveryNativeIntent(t.Context(), request)
		if readErr != nil || !reflect.DeepEqual(again, accepted) || !reflect.DeepEqual(snapshot, attemptPostgresSnapshot(t, s)) {
			t.Fatal("PostgreSQL close replay changed state", readErr)
		}
	}

	retained, err := repository.ReadManagedEffectiveRemovalCoverageExact(t.Context(), managed.RecoveryExactRequest{Key: f.key, Association: coverageRef})
	if err != nil || !reflect.DeepEqual(retained, coverage) || calls != host.calls {
		t.Fatal("PostgreSQL retained coverage changed", err)
	}
	// Remove only the fixture's seal event after positive close/replay. Independent
	// receipt and canonical coverage bytes cannot substitute for missing custody.
	if _, err = pg.Exec(t.Context(), "DELETE FROM cp_managed_controller_records WHERE kind='removal_attempt_event' AND key=$1", command.ID.String()); err != nil {
		t.Fatal(err)
	}

	corrupt := attemptPostgresSnapshot(t, s)
	if _, err = repository.ReadManagedEffectiveRemovalCoverageExact(t.Context(), managed.RecoveryExactRequest{Key: f.key, Association: coverageRef}); err == nil {
		t.Fatal("PostgreSQL missing seal event accepted")
	}

	if _, err = repository.SealManagedEffectiveRemovalCoverage(t.Context(), command); err == nil || !reflect.DeepEqual(corrupt, attemptPostgresSnapshot(t, s)) {
		t.Fatal("PostgreSQL corrupt seal replay repaired rows", err)
	}
}
