package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/postgres"
)

func batchRequest(t *testing.T, f *removalFixture) managed.QuiescenceBatchAcceptanceRequest {
	t.Helper()

	target, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil {
		t.Fatal(err)
	}

	attempt, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
	if err != nil {
		t.Fatal(err)
	}

	q := managed.QuiescenceBatchAcceptanceRequest{Schema: managed.QuiescenceBatchRequestSchema, Command: managed.QuiescenceBatchCommand{Key: f.key.Key, ID: id.New(managed.PrefixOperation), Revision: managed.Decimal(strconv.FormatUint(target.Revision, 10)), LeaseEpoch: managed.Decimal(strconv.FormatUint(target.LeaseEpoch, 10)), Owner: target.LeaseOwner}, Attempt: attempt.State.AttemptRef}
	for _, rid := range attempt.Descriptor.ReservationIDs {
		for _, original := range f.observations {
			if original.Command.ReservationID != rid {
				continue
			}

			o := original.Observation
			o.ObservedAt = quiescenceClock(t, f.s)
			o.ValidUntil = o.ObservedAt.Add(time.Minute)
			signQuiescence(t, &o)
			q.Entries = append(q.Entries, managed.QuiescenceBatchEntry{OperationID: id.New(managed.PrefixOperation), Observation: o})
		}
	}

	if len(q.Entries) != len(attempt.Descriptor.ReservationIDs) {
		t.Fatal("missing original observations")
	}

	return q
}
func batchChildRequest(q managed.QuiescenceBatchAcceptanceRequest, i int) managed.QuiescenceAcceptanceRequest {
	r, _ := q.Command.Revision.Uint64()
	entry := q.Entries[i]

	return managed.QuiescenceAcceptanceRequest{Command: managed.OriginalCommand{Key: q.Command.Key, ID: entry.OperationID, Revision: managed.Decimal(strconv.FormatUint(r+uint64(i), 10)), LeaseEpoch: q.Command.LeaseEpoch, Owner: q.Command.Owner, ReservationID: entry.Observation.Reference.ReservationID}, Observation: entry.Observation}
}
func TestManagedQuiescenceBatchProtocol(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			authority := f.s.Authority.(managed.QuiescenceBatchStore)
			q := batchRequest(t, f)

			before, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil {
				t.Fatal(err)
			}

			attempt, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
			if err != nil {
				t.Fatal(err)
			}

			operationKey := managed.OperationKey{Key: f.key.Key, ActionID: f.s.action.ID}

			operation, err := f.s.ReadManagedOperation(t.Context(), operationKey)
			if err != nil {
				t.Fatal(err)
			}

			receipt, err := authority.AcceptManagedQuiescenceBatch(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}

			if receipt.Count != managed.Decimal(strconv.Itoa(len(q.Entries))) || receipt.Receipt.Revision != managed.Decimal(strconv.FormatUint(before.Revision+uint64(len(q.Entries))+1, 10)) {
				t.Fatal("aggregate counters", receipt)
			}

			after, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil {
				t.Fatal(err)
			}

			before.Revision += uint64(len(q.Entries)) + 1
			if !reflect.DeepEqual(before, after) {
				t.Fatal("batch changed parent fields")
			}

			laterAttempt, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
			if err != nil || !reflect.DeepEqual(attempt, laterAttempt) {
				t.Fatal("batch changed attempt", err)
			}

			laterOperation, err := f.s.ReadManagedOperation(t.Context(), operationKey)
			if err != nil || !reflect.DeepEqual(operation, laterOperation) {
				t.Fatal("batch changed journal", err)
			}

			for i := range q.Entries {
				child := batchChildRequest(q, i)

				saved, err := f.s.AcceptManagedQuiescence(t.Context(), child)
				if err != nil {
					t.Fatal("individual replay", err)
				}

				raw, err := json.Marshal(struct {
					Kind  string                              `json:"kind"`
					Input managed.QuiescenceAcceptanceRequest `json:"input"`
				}{managed.QuiescenceHistory, child})
				if err != nil {
					t.Fatal(err)
				}

				if saved.Digest != managed.Digest(raw) || saved.Revision != after.Revision-uint64(len(q.Entries))+uint64(i) || saved.AcceptedAt != receipt.Receipt.AcceptedAt {
					t.Fatal("ordinary receipt changed")
				}

				row, err := f.s.ReadManagedQuiescence(t.Context(), managed.QuiescenceKey{Key: f.key.Key, ReservationID: child.Command.ReservationID, OperationID: child.Command.ID})
				if err != nil || !reflect.DeepEqual(row.Request, child) {
					t.Fatal("child history", err)
				}
			}

			for _, read := range []func() (managed.QuiescenceBatchReceipt, error){func() (managed.QuiescenceBatchReceipt, error) {
				return authority.AcceptManagedQuiescenceBatch(t.Context(), q)
			}, func() (managed.QuiescenceBatchReceipt, error) {
				return authority.ReadManagedQuiescenceBatch(t.Context(), managed.QuiescenceBatchKey{Key: f.key.Key, BatchID: q.Command.ID})
			}} {
				replay, e := read()
				if e != nil || replay != receipt {
					t.Fatal("aggregate replay", e)
				}
			}

			newer := batchChildRequest(batchRequest(t, f), 0)
			if _, err = f.s.AcceptManagedQuiescence(t.Context(), newer); err != nil {
				t.Fatal("newer individual", err)
			}

			if replay, e := authority.AcceptManagedQuiescenceBatch(t.Context(), q); e != nil || replay != receipt {
				t.Fatal("historical batch replay", e)
			}

			raw, err := q.Bytes()
			if err != nil {
				t.Fatal(err)
			}

			parsed, err := managed.ParseQuiescenceBatchAcceptanceRequest(raw)
			if err != nil || !reflect.DeepEqual(parsed, q) {
				t.Fatal("request roundtrip", err)
			}

			wire, err := receipt.Bytes()
			if err != nil {
				t.Fatal(err)
			}

			if parsedReceipt, e := managed.ParseQuiescenceBatchReceipt(wire); e != nil || parsedReceipt != receipt {
				t.Fatal("receipt roundtrip", e)
			}

			for _, bad := range [][]byte{append(append([]byte{}, raw...), []byte(" ")...), bytes.Replace(raw, []byte(`"schema":`), []byte(`"unknown":`), 1), bytes.Replace(raw, []byte(`"entries":[`), []byte(`"entries":null,"duplicate":[`), 1)} {
				if _, e := managed.ParseQuiescenceBatchAcceptanceRequest(bad); e == nil {
					t.Fatal("noncanonical request accepted")
				}
			}
		})
	}
}

func TestManagedQuiescenceBatchRefusals(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			authority := f.s.Authority.(managed.QuiescenceBatchStore)

			for _, kind := range []string{"order", "duplicate", "wrong-attempt", "expired", "future", "stale-cas", "cancelled", "existing-child"} {
				t.Run(kind, func(t *testing.T) {
					q := batchRequest(t, f)
					ctx := t.Context()

					switch kind {
					case "order":
						q.Entries[0], q.Entries[1] = q.Entries[1], q.Entries[0]
					case "duplicate":
						q.Entries[1] = q.Entries[0]
					case "wrong-attempt":
						q.Attempt.AttemptID = id.New(managed.PrefixOperation)
					case "expired":
						for i := range q.Entries {
							q.Entries[i].Observation.ObservedAt = q.Entries[i].Observation.ObservedAt.Add(-time.Minute * 2)
							q.Entries[i].Observation.ValidUntil = q.Entries[i].Observation.ValidUntil.Add(-time.Minute * 2)
							signQuiescence(t, &q.Entries[i].Observation)
						}
					case "future":
						for i := range q.Entries {
							q.Entries[i].Observation.ObservedAt = q.Entries[i].Observation.ObservedAt.Add(time.Minute)
							q.Entries[i].Observation.ValidUntil = q.Entries[i].Observation.ValidUntil.Add(time.Minute)
							signQuiescence(t, &q.Entries[i].Observation)
						}
					case "stale-cas":
						q.Command.Revision = "1"
					case "cancelled":
						var cancel context.CancelFunc

						ctx, cancel = context.WithCancel(ctx)
						cancel()
					case "existing-child":
						if _, err := f.s.AcceptManagedQuiescence(ctx, batchChildRequest(q, 0)); err != nil {
							t.Fatal(err)
						}
					}

					before, _ := f.s.ReadManagedTarget(t.Context(), f.key.Key)

					result, err := authority.AcceptManagedQuiescenceBatch(ctx, q)
					if err == nil || result != (managed.QuiescenceBatchReceipt{}) {
						t.Fatal("refusal returned success", err)
					}

					after, _ := f.s.ReadManagedTarget(t.Context(), f.key.Key)
					if !reflect.DeepEqual(before, after) {
						t.Fatal("refusal wrote parent")
					}

					_, err = authority.ReadManagedQuiescenceBatch(t.Context(), managed.QuiescenceBatchKey{Key: q.Command.Key, BatchID: q.Command.ID})
					if !errors.Is(err, managed.ErrNotFound) {
						t.Fatal("refusal left aggregate", err)
					}
				})
			}
		})
	}
}

func batchRemoved(t *testing.T, f *removalFixture, finish bool) {
	t.Helper()
	f.admit()

	for _, step := range []struct {
		service string
		kind    managed.ProviderStepKind
	}{{"api", managed.StepRemoveContainer}, {"sidecar", managed.StepRemoveContainer}, {"", managed.StepRemoveNetwork}} {
		grant := f.step(step.service, step.kind)
		if err := f.s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: grant.Permission.ResourceID, Evidence: []byte("positive removal"), ObservedAt: quiescenceClock(t, f.s)}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := f.s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: f.request(), Disposition: managed.Removed, ProducerProfile: "ctrlplane.managed.remove.v1", VerifierID: "provider", Evidence: []byte("complete"), SettledAt: quiescenceClock(t, f.s)}); err != nil {
		t.Fatal(err)
	}

	if finish {
		for _, rid := range f.reservations {
			grant := f.command(rid, managed.AttemptFinish)
			if _, err := f.s.RecordManagedAttemptFinish(t.Context(), managed.AttemptFinishRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(grant.Command)}); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func batchAbort(t *testing.T, f *removalFixture, historical bool) {
	t.Helper()

	rid := f.reservations[0]
	if historical {
		grant := f.command(rid, managed.AttemptBegin)
		if _, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
			t.Fatal(err)
		}

		proof := []byte("expired fence")
		if _, err := f.s.RecordManagedAttemptFence(t.Context(), managed.AttemptFenceRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(grant.Command), Fence: managed.FenceWire(managed.Fence{Epoch: 1, Evidence: proof, Digest: managed.Digest(proof), CompatibilityEvidence: []byte("retired"), ValidUntil: quiescenceClock(t, f.s).Add(-time.Second), Survivors: []string{"off-instance"}})}); err != nil {
			t.Fatal(err)
		}
	} else {
		f.admit()

		if _, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
			t.Fatal(err)
		}
	}

	binding, err := f.s.ReadManagedAttemptBinding(t.Context(), managed.AttemptBindingKey{AttemptKey: f.key, ReservationID: rid})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = f.s.RecordManagedAttemptBindingSettlement(t.Context(), managed.AttemptBindingSettlementRequest{AttemptRequest: f.request(), ReservationID: rid, FenceFactDigest: fixtureAttemptFact(t, "removal_fence", *binding.Fence)}); err != nil {
		t.Fatal(err)
	}

	grant := f.command(rid, managed.AttemptAbort)
	if _, err = f.s.RecordManagedAttemptAbort(t.Context(), managed.AttemptAbortRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(grant.Command), ProofVerifiedAt: quiescenceClock(t, f.s), ProofValidUntil: quiescenceClock(t, f.s).Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
}
func TestManagedQuiescenceBatchModes(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, mode := range []string{"revoked", "historical-abort", "projected-abort", "removed", "finished", "deleted"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				f := newRemovalFixture(t, backend)
				authority := f.s.Authority.(managed.QuiescenceBatchStore)
				original := batchRequest(t, f)

				receipt, err := authority.AcceptManagedQuiescenceBatch(t.Context(), original)
				if err != nil {
					t.Fatal(err)
				}

				switch mode {
				case "revoked":
					_, err = f.s.RevokeManagedRemovalAttempt(t.Context(), f.request())
				case "historical-abort":
					batchAbort(t, f, true)
				case "projected-abort":
					batchAbort(t, f, false)
				case "removed":
					batchRemoved(t, f, false)
				case "finished":
					batchRemoved(t, f, true)
				case "deleted":
					batchRemoved(t, f, true)
					_, err = f.s.TombstoneManagedRemovalAttempt(t.Context(), f.request())
				}

				if err != nil {
					t.Fatal(err)
				}

				fresh := batchRequest(t, f)
				before, _ := f.s.ReadManagedTarget(t.Context(), f.key.Key)

				result, err := authority.AcceptManagedQuiescenceBatch(t.Context(), fresh)
				if mode == "projected-abort" || mode == "deleted" {
					if !errors.Is(err, managed.ErrBlocked) || result != (managed.QuiescenceBatchReceipt{}) {
						t.Fatal("mode did not refuse", err)
					}

					after, _ := f.s.ReadManagedTarget(t.Context(), f.key.Key)
					if !reflect.DeepEqual(before, after) {
						t.Fatal("blocked mode wrote")
					}
				} else if err != nil {
					t.Fatal("observational retention refused", err)
				}

				if mode != "projected-abort" && mode != "deleted" {
					f.exerciseIndividualObservation()
				} else {
					freshChild := batchChildRequest(fresh, 0)
					if _, e := f.s.AcceptManagedQuiescence(t.Context(), freshChild); !errors.Is(e, managed.ErrBlocked) {
						t.Fatal("terminal fresh individual", e)
					}
				}

				replay, err := authority.AcceptManagedQuiescenceBatch(t.Context(), original)
				if err != nil || replay != receipt {
					t.Fatal("terminal replay", err)
				}

				read, err := authority.ReadManagedQuiescenceBatch(t.Context(), managed.QuiescenceBatchKey{Key: f.key.Key, BatchID: original.Command.ID})
				if err != nil || read != receipt {
					t.Fatal("terminal read", err)
				}

				child := batchChildRequest(original, 0)
				if _, err = f.s.AcceptManagedQuiescence(t.Context(), child); err != nil {
					t.Fatal("terminal individual replay", err)
				}
			})
		}
	}
}

func TestManagedQuiescenceBatchPostgresAtomicWrites(t *testing.T) {
	f := newRemovalFixture(t, "postgres")
	s := f.s.Authority.(*postgres.Store)
	authority := f.s.Authority.(managed.QuiescenceBatchStore)
	q := batchRequest(t, f)
	pg := pgdriver.Unwrap(s.DB())
	before := attemptPostgresSnapshot(t, s)

	cases := []struct{ name, table, condition string }{{"parent", "cp_managed_targets", "true"}, {"aggregate", "cp_managed_controller_records", "NEW.kind='quiescence_observation_batch'"}, {"aggregate-receipt", "cp_managed_commands", "NEW.operation_id='" + q.Command.ID.String() + "'"}}
	for i, entry := range q.Entries {
		scope, err := managed.QuiescenceScope(q.Command.Key, entry.Observation.Reference.ReservationID)
		if err != nil {
			t.Fatal(err)
		}

		suffix := strconv.Itoa(i)
		cases = append(cases, struct{ name, table, condition string }{"child-receipt-" + suffix, "cp_managed_commands", "NEW.operation_id='" + entry.OperationID.String() + "'"}, struct{ name, table, condition string }{"child-history-" + suffix, "cp_managed_controller_records", "NEW.kind='quiescence_observation' AND NEW.key='" + entry.OperationID.String() + "'"}, struct{ name, table, condition string }{"child-current-" + suffix, "cp_managed_controller_records", "NEW.kind='quiescence_current' AND NEW.scope='" + scope + "'"})
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			query := "CREATE FUNCTION batch_write_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF " + c.condition + " THEN RAISE EXCEPTION 'injected batch write failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER batch_write_failure AFTER INSERT OR UPDATE ON " + c.table + " FOR EACH ROW EXECUTE FUNCTION batch_write_failure();"
			if _, err := pg.Exec(t.Context(), query); err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				if _, err := pg.Exec(ctx, "DROP TRIGGER batch_write_failure ON "+c.table+"; DROP FUNCTION batch_write_failure()"); err != nil {
					t.Fatal(err)
				}
			})

			result, err := authority.AcceptManagedQuiescenceBatch(t.Context(), q)
			if err == nil || result != (managed.QuiescenceBatchReceipt{}) {
				t.Fatal("injected write succeeded", err)
			}

			if after := attemptPostgresSnapshot(t, s); !reflect.DeepEqual(before, after) {
				t.Fatal("partial stage persisted")
			}
		})
	}

	if _, err := pg.Exec(t.Context(), `DO $$ BEGIN
IF EXISTS (SELECT 1 FROM pg_proc WHERE proname = 'batch_write_failure' AND pronargs = 0 AND pronamespace = current_schema()::regnamespace)
OR EXISTS (SELECT 1 FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid WHERE t.tgname = 'batch_write_failure' AND c.relnamespace = current_schema()::regnamespace)
THEN RAISE EXCEPTION 'batch fault objects survived cleanup'; END IF; END $$`); err != nil {
		t.Fatal("fault cleanup", err)
	}

	if _, err := authority.AcceptManagedQuiescenceBatch(t.Context(), q); err != nil {
		t.Fatal("retry", err)
	}
}

func TestManagedQuiescenceBatchPostgresCorruption(t *testing.T) {
	for _, kind := range []string{"aggregate-row", "aggregate-receipt", "child-history", "child-receipt", "current", "manifest-digest", "oversized-manifest", "unselected-native"} {
		t.Run(kind, func(t *testing.T) {
			f := newRemovalFixture(t, "postgres")
			s := f.s.Authority.(*postgres.Store)
			authority := f.s.Authority.(managed.QuiescenceBatchStore)
			q := batchRequest(t, f)

			var unselected managed.AttemptCommandGrant
			if kind == "unselected-native" {
				unselected = f.command(q.Entries[1].Observation.Reference.ReservationID, managed.AttemptBegin)
				q = batchRequest(t, f)
				q.Entries = q.Entries[:1]
			}

			if _, err := authority.AcceptManagedQuiescenceBatch(t.Context(), q); err != nil {
				t.Fatal(err)
			}

			pg := pgdriver.Unwrap(s.DB())
			scope, _ := managed.QuiescenceBatchScope(q.Command.Key)
			childScope, _ := managed.QuiescenceScope(q.Command.Key, q.Entries[0].Observation.Reference.ReservationID)

			var err error

			switch kind {
			case "aggregate-row":
				_, err = pg.NewRaw("DELETE FROM cp_managed_controller_records WHERE kind=$1 AND scope=$2 AND key=$3", managed.QuiescenceBatchHistory, scope, q.Command.ID.String()).Exec(t.Context())
			case "aggregate-receipt":
				_, err = pg.NewRaw("DELETE FROM cp_managed_commands WHERE target_id=$1 AND operation_id=$2", q.Command.Key.InstanceID.String(), q.Command.ID.String()).Exec(t.Context())
			case "child-history":
				_, err = pg.NewRaw("DELETE FROM cp_managed_controller_records WHERE kind=$1 AND scope=$2 AND key=$3", managed.QuiescenceHistory, childScope, q.Entries[0].OperationID.String()).Exec(t.Context())
			case "child-receipt":
				_, err = pg.NewRaw("DELETE FROM cp_managed_commands WHERE target_id=$1 AND operation_id=$2", q.Command.Key.InstanceID.String(), q.Entries[0].OperationID.String()).Exec(t.Context())
			case "current":
				_, err = pg.NewRaw("DELETE FROM cp_managed_controller_records WHERE kind=$1 AND scope=$2 AND key='current'", managed.QuiescenceCurrent, childScope).Exec(t.Context())
			case "oversized-manifest":
				_, err = pg.NewRaw("UPDATE cp_managed_controller_records SET data=$4 WHERE kind=$1 AND scope=$2 AND key=$3", managed.QuiescenceBatchHistory, scope, q.Command.ID.String(), bytes.Repeat([]byte(" "), managed.MaxQuiescenceBatchRecord+1)).Exec(t.Context())
			case "manifest-digest":
				var raw []byte
				if err = pg.NewRaw("SELECT data FROM cp_managed_controller_records WHERE kind=$1 AND scope=$2 AND key=$3", managed.QuiescenceBatchHistory, scope, q.Command.ID.String()).Scan(t.Context(), &raw); err == nil {
					needle := []byte(`"request_digest":"`)

					at := bytes.Index(raw, needle) + len(needle)
					if at < len(needle) {
						t.Fatal("missing digest")
					}

					if raw[at] == 'a' {
						raw[at] = 'b'
					} else {
						raw[at] = 'a'
					}

					_, err = pg.NewRaw("UPDATE cp_managed_controller_records SET data=$4 WHERE kind=$1 AND scope=$2 AND key=$3", managed.QuiescenceBatchHistory, scope, q.Command.ID.String(), raw).Exec(t.Context())
				}
			case "unselected-native":
				_, err = pg.NewRaw("DELETE FROM cp_managed_commands WHERE target_id=$1 AND operation_id=$2", q.Command.Key.InstanceID.String(), unselected.Command.AuthorizationID.String()).Exec(t.Context())
			}

			if err != nil {
				t.Fatal(err)
			}

			before := attemptPostgresSnapshot(t, s)

			for _, read := range []func() (managed.QuiescenceBatchReceipt, error){func() (managed.QuiescenceBatchReceipt, error) {
				return authority.AcceptManagedQuiescenceBatch(t.Context(), q)
			}, func() (managed.QuiescenceBatchReceipt, error) {
				return authority.ReadManagedQuiescenceBatch(t.Context(), managed.QuiescenceBatchKey{Key: q.Command.Key, BatchID: q.Command.ID})
			}} {
				result, e := read()
				if e == nil || result != (managed.QuiescenceBatchReceipt{}) {
					t.Fatal("broken graph read", e)
				}
			}

			if after := attemptPostgresSnapshot(t, s); !reflect.DeepEqual(before, after) {
				t.Fatal("corrupt history repaired")
			}
		})
	}
}

func TestManagedQuiescenceBatchPostgresLockExpiry(t *testing.T) {
	f := newRemovalFixture(t, "postgres")
	s := f.s.Authority.(*postgres.Store)
	q := batchRequest(t, f)

	until := quiescenceClock(t, f.s).Add(300 * time.Millisecond)
	for i := range q.Entries {
		q.Entries[i].Observation.ValidUntil = until
		signQuiescence(t, &q.Entries[i].Observation)
	}

	before := attemptPostgresSnapshot(t, s)

	err := releaseAfterStoreTime(t, s, f.key.Key, until, func() error {
		result, e := s.AcceptManagedQuiescenceBatch(t.Context(), q)
		if result != (managed.QuiescenceBatchReceipt{}) {
			t.Error("expired callback exposed receipt")
		}

		return e
	})
	if !errors.Is(err, managed.ErrBlocked) {
		t.Fatal("post-lock proof expiry", err)
	}

	if after := attemptPostgresSnapshot(t, s); !reflect.DeepEqual(before, after) {
		t.Fatal("expired batch wrote")
	}
}
func TestManagedQuiescenceBatchPostgresLeaseLockExpiry(t *testing.T) {
	f := newRemovalFixture(t, "postgres")
	s := f.s.Authority.(*postgres.Store)

	lease, err := s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: f.request().Command, Duration: 500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	q := batchRequest(t, f)
	before := attemptPostgresSnapshot(t, s)

	beforeCall := quiescenceClock(t, f.s)
	if !beforeCall.Before(lease.LeaseUntil) {
		t.Fatal("lease expired before lock-expiry call")
	}

	err = releaseAfterStoreTime(t, s, f.key.Key, lease.LeaseUntil, func() error {
		result, e := s.AcceptManagedQuiescenceBatch(t.Context(), q)
		if result != (managed.QuiescenceBatchReceipt{}) {
			t.Error("expired lease exposed batch receipt")
		}

		return e
	})
	afterCall := quiescenceClock(t, f.s)
	unchanged := reflect.DeepEqual(before, attemptPostgresSnapshot(t, s))
	t.Logf("lease lock expiry pre=%s deadline=%s post=%s unchanged=%t", beforeCall.Format(time.RFC3339Nano), lease.LeaseUntil.Format(time.RFC3339Nano), afterCall.Format(time.RFC3339Nano), unchanged)

	if !afterCall.After(lease.LeaseUntil) {
		t.Fatal("call did not cross actual lease deadline")
	}

	for _, entry := range q.Entries {
		if !afterCall.Before(entry.Observation.ValidUntil) {
			t.Fatal("new proof expired during lease lock case")
		}
	}

	if !errors.Is(err, managed.ErrConflict) {
		t.Fatal("post-lock lease expiry", err)
	}

	if !unchanged {
		t.Fatal("expired lease batch wrote")
	}
}
func TestManagedQuiescenceBatchConcurrent(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, kind := range []string{"overlap", "disjoint", "individual", "lease", "revoke", "provider"} {
			t.Run(backend+"/"+kind, func(t *testing.T) {
				f := newRemovalFixture(t, backend)
				if kind == "provider" {
					f.admit()
				}

				authority := f.s.Authority.(managed.QuiescenceBatchStore)
				q := batchRequest(t, f)
				other := batchRequest(t, f)

				switch kind {
				case "disjoint":
					q.Entries = q.Entries[:1]
					other.Entries = other.Entries[1:]
				case "overlap":
					other.Entries = other.Entries[:1]
				}

				command := quiescenceCommand(t, f.s, f.key.Key)
				attemptRequest := f.request()

				operation, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
				if err != nil {
					t.Fatal(err)
				}

				alternate := func() error {
					_, e := authority.AcceptManagedQuiescenceBatch(t.Context(), other)

					return e
				}

				switch kind {
				case "individual":
					alternate = func() error {
						_, e := f.s.AcceptManagedQuiescence(t.Context(), batchChildRequest(other, 0))

						return e
					}
				case "lease":
					alternate = func() error {
						_, e := f.s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: command, Duration: managed.MaxLease})

						return e
					}
				case "revoke":
					alternate = func() error {
						_, e := f.s.RevokeManagedRemovalAttempt(t.Context(), attemptRequest)

						return e
					}
				case "provider":
					alternate = func() error {
						grant, e := f.s.AuthorizeManagedProviderStep(t.Context(), managed.ProviderStepAuthorization{Command: command, Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: f.key.Key, ActionID: f.s.action.ID}, ProviderOperationID: operation.Descriptor.ProviderOperationID, Kind: managed.StepRemoveContainer, Service: "api"}})
						if e != nil && grant.NewlyIssued {
							t.Error("failed competitor issued")
						}

						return e
					}
				}

				start := make(chan struct{})
				results := make(chan error, 2)

				go func() { <-start; _, e := authority.AcceptManagedQuiescenceBatch(t.Context(), q); results <- e }()
				go func() { <-start; results <- alternate() }()

				close(start)

				a, b := <-results, <-results
				if (a == nil) == (b == nil) {
					t.Fatalf("expected one fresh winner: %v / %v", a, b)
				}

				for _, e := range []error{a, b} {
					if e != nil && !errors.Is(e, managed.ErrConflict) {
						t.Fatal("unexpected competitor refusal", e)
					}
				}
			})
		}
	}
}

func (f *removalFixture) refreshObservationBatch() (int, time.Duration) {
	t := f.t
	t.Helper()

	q := batchRequest(t, f)
	for i := range q.Entries {
		q.Entries[i].Observation.Attestation = bytes.Repeat([]byte{255}, managed.MaxQuiescenceAttestation)
	}

	before, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil {
		t.Fatal(err)
	}

	attempt, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
	if err != nil {
		t.Fatal(err)
	}

	operationKey := managed.OperationKey{Key: f.key.Key, ActionID: f.s.action.ID}

	journal, err := f.s.ReadManagedOperation(t.Context(), operationKey)
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()

	receipt, err := f.s.Authority.(managed.QuiescenceBatchStore).AcceptManagedQuiescenceBatch(t.Context(), q)
	if err != nil {
		t.Fatal("full observation batch", err)
	}

	after, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil {
		t.Fatal(err)
	}

	before.Revision += uint64(len(q.Entries) + 1)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("full batch parent delta")
	}

	later, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
	if err != nil || !reflect.DeepEqual(attempt, later) {
		t.Fatal("full batch attempt delta", err)
	}

	laterJournal, err := f.s.ReadManagedOperation(t.Context(), operationKey)
	if err != nil || !reflect.DeepEqual(journal, laterJournal) {
		t.Fatal("full batch journal delta", err)
	}

	f.batches = append(f.batches, q)

	f.observations = f.observations[:0]
	for i := range q.Entries {
		f.observations = append(f.observations, batchChildRequest(q, i))
	}

	now := quiescenceClock(t, f.s)
	margin := time.Minute
	acceptanceMargin := time.Minute

	for _, entry := range q.Entries {
		if left := entry.Observation.ValidUntil.Sub(now); left < margin {
			margin = left
		}

		if left := entry.Observation.ValidUntil.Sub(receipt.Receipt.AcceptedAt); left < acceptanceMargin {
			acceptanceMargin = left
		}
	}

	t.Logf("batch64 cost including exact counter checks=%s count=%d parent_delta=%d actual_callback_margin=%s return_margin=%s", time.Since(started), len(q.Entries), len(q.Entries)+1, acceptanceMargin, margin)

	if margin <= 0 || acceptanceMargin <= 0 {
		t.Fatal("batch cannot establish live proof set")
	}

	return len(q.Entries), margin
}
func TestManagedQuiescenceBatchFullFiniteSet(t *testing.T) {
	requireManagedMaximum(t, "batch")
	testManagedFiniteSetWithBatch(t, false, "", true)
}
func TestManagedQuiescenceBatchPostgresFullFiniteSetNearLimit(t *testing.T) {
	requireManagedMaximum(t, "batch")
	testManagedFiniteSetWithBatch(t, true, "", true)
}
func TestManagedQuiescenceBatchPostgresAfterFenceDirectRefresh(t *testing.T) {
	if !batchDiagnosticEnabled(t) {
		return
	}

	testManagedFiniteSetWithBatch(t, false, "after-fence-direct", true)
}
func TestManagedQuiescenceBatchPostgresFullLedgerDirectRefresh(t *testing.T) {
	if !batchDiagnosticEnabled(t) {
		return
	}

	testManagedFiniteSetWithBatch(t, false, "full-ledger-direct", true)
}
func batchDiagnosticEnabled(t *testing.T) bool {
	t.Helper()

	if os.Getenv("CTRLPLANE_ATTEMPT_PROFILE") != "1" {
		t.Skip("opt-in bounded batch timing diagnostic")

		return false
	}

	return true
}

func batchHistoricalOriginals(t *testing.T, f *removalFixture, q managed.QuiescenceBatchAcceptanceRequest) {
	t.Helper()

	for _, entry := range q.Entries {
		ref := entry.Observation.Reference
		for _, selected := range []struct {
			kind      string
			operation id.ID
		}{{managed.OriginalRegistration, ref.RegistrationOperationID}, {managed.OriginalDrain, ref.DrainOperationID}} {
			record, err := f.s.ReadManagedOriginalLifecycle(t.Context(), managed.OriginalLifecycleKey{Key: f.key.Key, Kind: selected.kind, OperationID: selected.operation})
			if err != nil {
				t.Fatal("post-Abort original read", err)
			}

			m := record.Material

			var receipt managed.Receipt
			if m.Kind == managed.OriginalRegistration {
				receipt, err = f.s.AcceptManagedOriginalRegistration(t.Context(), managed.OriginalRegistrationAcceptanceRequest{Command: m.Command, Facts: *m.Registration, NativeReceipt: m.NativeReceipt, NativeReceiptDigest: m.NativeReceiptDigest})
			} else {
				receipt, err = f.s.RecordManagedOriginalDrain(t.Context(), managed.OriginalDrainAcceptanceRequest{Command: m.Command, Facts: *m.Drain, NativeReceipt: m.NativeReceipt, NativeReceiptDigest: m.NativeReceiptDigest})
			}

			if err != nil || managed.OriginalReceipt(receipt) != record.Receipt {
				t.Fatal("post-Abort exact original replay", err)
			}
		}
	}
}
func TestManagedQuiescenceBatchProjectedAbortHistory(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			authority := f.s.Authority.(managed.QuiescenceBatchStore)
			q := batchRequest(t, f)

			receipt, err := authority.AcceptManagedQuiescenceBatch(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}

			batchAbort(t, f, false)
			before, _ := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			batchHistoricalOriginals(t, f, q)

			for i := range q.Entries {
				child := batchChildRequest(q, i)

				row, err := f.s.ReadManagedQuiescence(t.Context(), managed.QuiescenceKey{Key: f.key.Key, ReservationID: child.Command.ReservationID, OperationID: child.Command.ID})
				if err != nil {
					t.Fatal(err)
				}

				saved, err := f.s.AcceptManagedQuiescence(t.Context(), child)
				if err != nil || managed.OriginalReceipt(saved) != row.Receipt {
					t.Fatal("post-Abort child replay", err)
				}
			}

			if replay, e := authority.AcceptManagedQuiescenceBatch(t.Context(), q); e != nil || replay != receipt {
				t.Fatal("post-Abort aggregate", e)
			}

			fresh := batchRequest(t, f)
			for i := range fresh.Entries {
				child := batchChildRequest(fresh, i)
				child.Command = originalCommand(t, f.s, f.key.Key)

				child.Command.ReservationID = fresh.Entries[i].Observation.Reference.ReservationID
				if _, err = f.s.AcceptManagedQuiescence(t.Context(), child); !errors.Is(err, managed.ErrBlocked) {
					t.Fatal("fresh same-runtime child accepted", i, err)
				}

				subset := fresh

				subset.Entries = []managed.QuiescenceBatchEntry{fresh.Entries[i]}
				if _, err = authority.AcceptManagedQuiescenceBatch(t.Context(), subset); !errors.Is(err, managed.ErrBlocked) {
					t.Fatal("fresh same-runtime batch accepted", i, err)
				}
			}

			ref := q.Entries[1].Observation.Reference

			drain, err := f.s.ReadManagedOriginalLifecycle(t.Context(), managed.OriginalLifecycleKey{Key: f.key.Key, Kind: managed.OriginalDrain, OperationID: ref.DrainOperationID})
			if err != nil {
				t.Fatal(err)
			}

			m := drain.Material
			command := originalCommand(t, f.s, f.key.Key)

			command.ReservationID = ref.ReservationID
			if _, err = f.s.RecordManagedOriginalDrain(t.Context(), managed.OriginalDrainAcceptanceRequest{Command: command, Facts: *m.Drain, NativeReceipt: m.NativeReceipt, NativeReceiptDigest: m.NativeReceiptDigest}); !errors.Is(err, managed.ErrBlocked) {
				t.Fatal("fresh original drain accepted", err)
			}

			native := []byte("new forbidden native invocation")

			grant, err := f.s.AuthorizeManagedAttemptCommand(t.Context(), managed.AttemptCommandRequest{AttemptRequest: f.request(), ReservationID: f.reservations[1], Kind: managed.AttemptBegin, ExpectedNativeVersion: "1", NativeCommand: managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: "forbidden", Original: native, OriginalDigest: managed.Digest(native), Native: native, NativeDigest: managed.Digest(native)}})
			if err == nil || grant.NewlyIssued {
				t.Fatal("historical repair opened native issue", err)
			}

			provider, err := f.s.AuthorizeManagedProviderStep(t.Context(), managed.ProviderStepAuthorization{Command: quiescenceCommand(t, f.s, f.key.Key), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: f.key.Key, ActionID: f.s.action.ID}, ProviderOperationID: f.provider, Kind: managed.StepRemoveContainer, Service: "api"}})
			if err == nil || provider.NewlyIssued {
				t.Fatal("historical repair opened provider issue", err)
			}

			after, _ := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("historical reads/refusals wrote")
			}
		})
	}
}
func TestManagedQuiescenceBatchCrossRuntimeAbort(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			action := largeRemovalAction(t)

			spec, err := managed.ParseProvisionSpec(action.NewTarget.Manifest)
			if err != nil {
				t.Fatal(err)
			}

			spec.Services = spec.Services[:2]

			provision, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}

			action.NewTarget.Manifest.Members = action.NewTarget.Manifest.Members[:2]

			action.NewTarget.Manifest.Spec, err = json.Marshal(managed.ManagedSpec{Profile: "ctrlplane.managed.v1", RuntimeRoles: []managed.RuntimeRole{managed.Polling, managed.Polling}, Provision: provision})
			if err != nil {
				t.Fatal(err)
			}

			action.NewTarget.Manifest.SpecDigest = managed.Digest(action.NewTarget.Manifest.Spec)
			signControllerAction(t, &action)
			f, _ := removalActionFixture(t, controllerStore(t, backend), action, 1)
			batchAbort(t, f, false)
			q := batchRequest(t, f)
			selected := q.Entries[1]

			q.Entries = []managed.QuiescenceBatchEntry{selected}
			if _, err = f.s.Authority.(managed.QuiescenceBatchStore).AcceptManagedQuiescenceBatch(t.Context(), q); err != nil {
				t.Fatal("different runtime incorrectly blocked", err)
			}

			fresh := batchRequest(t, f)
			child := batchChildRequest(fresh, 1)
			child.Command = originalCommand(t, f.s, f.key.Key)

			child.Command.ReservationID = selected.Observation.Reference.ReservationID
			if _, err = f.s.AcceptManagedQuiescence(t.Context(), child); err != nil {
				t.Fatal("different runtime individual blocked", err)
			}
		})
	}
}

func TestManagedQuiescenceBatchPostgresPostAbortCorruption(t *testing.T) {
	for _, kind := range []string{"registration-missing", "registration-malformed", "registration-receipt", "drain-missing", "drain-malformed", "drain-receipt", "drain-projection"} {
		t.Run(kind, func(t *testing.T) {
			f := newRemovalFixture(t, "postgres")
			s := f.s.Authority.(*postgres.Store)
			authority := f.s.Authority.(managed.QuiescenceBatchStore)

			q := batchRequest(t, f)
			if _, err := authority.AcceptManagedQuiescenceBatch(t.Context(), q); err != nil {
				t.Fatal(err)
			}

			batchAbort(t, f, false)

			ref := q.Entries[0].Observation.Reference

			selected := managed.OriginalLifecycleKey{Key: f.key.Key, Kind: managed.OriginalDrain, OperationID: ref.DrainOperationID}
			if kind[:5] == "regis" {
				selected.Kind = managed.OriginalRegistration
				selected.OperationID = ref.RegistrationOperationID
			}

			raw, err := json.Marshal(f.key.Key)
			if err != nil {
				t.Fatal(err)
			}

			scope := managed.Digest(raw)
			pg := pgdriver.Unwrap(s.DB())

			switch kind {
			case "registration-missing", "drain-missing":
				_, err = pg.NewRaw("DELETE FROM cp_managed_controller_records WHERE kind=$1 AND scope=$2 AND key=$3", selected.Kind, scope, selected.OperationID.String()).Exec(t.Context())
			case "registration-malformed", "drain-malformed":
				_, err = pg.NewRaw("UPDATE cp_managed_controller_records SET data=$4 WHERE kind=$1 AND scope=$2 AND key=$3", selected.Kind, scope, selected.OperationID.String(), []byte("{}")).Exec(t.Context())
			case "registration-receipt", "drain-receipt":
				_, err = pg.NewRaw("DELETE FROM cp_managed_commands WHERE target_id=$1 AND operation_id=$2", f.key.InstanceID.String(), selected.OperationID.String()).Exec(t.Context())
			case "drain-projection":
				var target *managed.Target

				target, err = f.s.ReadManagedTarget(t.Context(), f.key.Key)
				if err == nil {
					for i := range target.Reservations {
						if target.Reservations[i].ID == ref.ReservationID {
							target.Reservations[i].Drain = nil
						}
					}

					raw, err = json.Marshal(target)
					if err == nil {
						_, err = pg.NewRaw("UPDATE cp_managed_targets SET data=$2::jsonb WHERE id=$1", f.key.InstanceID.String(), string(raw)).Exec(t.Context())
					}
				}
			}

			if err != nil {
				t.Fatal(err)
			}

			before := attemptPostgresSnapshot(t, s)
			if _, err = f.s.ReadManagedOriginalLifecycle(t.Context(), selected); err == nil {
				t.Fatal("corrupt original accepted")
			}

			if _, err = f.s.ReadManagedQuiescence(t.Context(), managed.QuiescenceKey{Key: f.key.Key, ReservationID: ref.ReservationID, OperationID: q.Entries[0].OperationID}); err == nil {
				t.Fatal("corrupt child accepted")
			}

			if _, err = authority.AcceptManagedQuiescenceBatch(t.Context(), q); err == nil {
				t.Fatal("corrupt replay accepted")
			}

			if _, err = authority.ReadManagedQuiescenceBatch(t.Context(), managed.QuiescenceBatchKey{Key: f.key.Key, BatchID: q.Command.ID}); err == nil {
				t.Fatal("corrupt aggregate accepted")
			}

			if after := attemptPostgresSnapshot(t, s); !reflect.DeepEqual(before, after) {
				t.Fatal("corrupt history repaired")
			}
		})
	}
}

// exerciseIndividualObservation invokes the individual API under a batch-refreshed ledger.
func (f *removalFixture) exerciseIndividualObservation() {
	t := f.t
	t.Helper()

	before, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil {
		t.Fatal(err)
	}

	prior, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
	if err != nil {
		t.Fatal(err)
	}

	f.refreshObservation(0)
	q := f.observations[0]

	after, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil {
		t.Fatal(err)
	}

	before.Revision++
	if !reflect.DeepEqual(before, after) {
		t.Fatal("individual observation delta")
	}

	saved, err := f.s.ReadManagedQuiescence(t.Context(), managed.QuiescenceKey{Key: f.key.Key, ReservationID: q.Command.ReservationID, OperationID: q.Command.ID})
	if err != nil {
		t.Fatal(err)
	}

	replay, err := f.s.AcceptManagedQuiescence(t.Context(), q)
	if err != nil || managed.OriginalReceipt(replay) != saved.Receipt {
		t.Fatal("individual exact replay", err)
	}

	for _, mode := range []string{"stale", "malformed", "cancelled"} {
		denied := q
		denied.Command.ID = id.New(managed.PrefixOperation)
		ctx := t.Context()

		if mode == "malformed" {
			denied.Observation.ChallengeDigest = "bad"
		}

		if mode == "cancelled" {
			cancelled, cancel := context.WithCancel(ctx)
			cancel()

			ctx = cancelled
		}

		if _, err = f.s.AcceptManagedQuiescence(ctx, denied); err == nil {
			t.Fatal("individual refusal", mode)
		}
	}

	current, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil || !reflect.DeepEqual(after, current) {
		t.Fatal("individual replay/refusal wrote", err)
	}

	later, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
	if err != nil || !reflect.DeepEqual(prior, later) {
		t.Fatal("individual mutated attempt", err)
	}
}
func (f *removalFixture) verifyBatchHistory() {
	t := f.t
	t.Helper()

	before, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil {
		t.Fatal(err)
	}

	authority := f.s.Authority.(managed.QuiescenceBatchStore)
	for _, q := range f.batches {
		saved, e := authority.ReadManagedQuiescenceBatch(t.Context(), managed.QuiescenceBatchKey{Key: f.key.Key, BatchID: q.Command.ID})
		if e != nil {
			t.Fatal("terminal batch history", e)
		}

		replay, e := authority.AcceptManagedQuiescenceBatch(t.Context(), q)
		if e != nil || replay != saved {
			t.Fatal("terminal batch exact replay", e)
		}

		for _, i := range []int{0, len(q.Entries) - 1} {
			child := batchChildRequest(q, i)

			record, e := f.s.ReadManagedQuiescence(t.Context(), managed.QuiescenceKey{Key: f.key.Key, ReservationID: child.Command.ReservationID, OperationID: child.Command.ID})
			if e != nil {
				t.Fatal(e)
			}

			receipt, e := f.s.AcceptManagedQuiescence(t.Context(), child)
			if e != nil || managed.OriginalReceipt(receipt) != record.Receipt {
				t.Fatal("terminal maximum individual replay", e)
			}
		}
	}

	after, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("terminal maximum replay wrote", err)
	}
}

func TestManagedQuiescenceBatchPostgresReserve(t *testing.T) {
	f := newRemovalFixture(t, "postgres")
	s := f.s.Authority.(*postgres.Store)
	q := batchRequest(t, f)

	const (
		reserve = 25
		cost    = 3
	)

	pg := pgdriver.Unwrap(s.DB())
	setRevision := func(revision int64) {
		t.Helper()

		if _, err := pg.Exec(t.Context(), "UPDATE cp_managed_targets SET revision=$1,data=jsonb_set(data,'{revision}',to_jsonb($1::bigint)) WHERE id=$2", revision, f.key.InstanceID.String()); err != nil {
			t.Fatal(err)
		}

		q.Command.Revision = managed.Decimal(strconv.FormatInt(revision, 10))
	}
	setRevision(math.MaxInt64 - reserve - cost + 1)

	before := attemptPostgresSnapshot(t, s)
	if _, err := s.AcceptManagedQuiescenceBatch(t.Context(), q); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("batch consumed remaining terminal reserve", err)
	}

	if after := attemptPostgresSnapshot(t, s); !reflect.DeepEqual(before, after) {
		t.Fatal("capacity refusal wrote")
	}

	setRevision(math.MaxInt64 - reserve - cost)

	receipt, err := s.AcceptManagedQuiescenceBatch(t.Context(), q)
	if err != nil {
		t.Fatal("exact batch allowance", err)
	}

	revision, _ := receipt.Receipt.Revision.Uint64()
	if revision != math.MaxInt64-reserve {
		t.Fatal("batch N+1 counter mismatch")
	}
}

const batchCompletionHeadroom = 490 + 65*(1+33) + (64 + 1 + 33 + 64)

type batchRenewalBudget struct {
	maxBatches, maxLeases        int
	batchAttempts, leaseAttempts int
	batches, leases              int
}

func newBatchRenewalBudget(t *testing.T, nearLimit bool, diagnostic string) *batchRenewalBudget {
	t.Helper()

	b := &batchRenewalBudget{maxBatches: 34, maxLeases: 162}
	if !nearLimit {
		b.maxBatches++
	}

	if diagnostic == "full-ledger-direct" {
		b.maxLeases++
	}

	return b
}
func (b *batchRenewalBudget) batchAttempt(t *testing.T) {
	t.Helper()

	b.batchAttempts++
	if b.batchAttempts > b.maxBatches {
		t.Fatalf("batch attempt budget exceeded: %d > %d", b.batchAttempts, b.maxBatches)
	}
}
func (b *batchRenewalBudget) leaseAttempt(t *testing.T) {
	t.Helper()

	b.leaseAttempts++
	if b.leaseAttempts > b.maxLeases {
		t.Fatalf("lease attempt budget exceeded: %d > %d", b.leaseAttempts, b.maxLeases)
	}
}
func (b *batchRenewalBudget) complete(t *testing.T) {
	t.Helper()

	if b.batchAttempts != b.batches || b.leaseAttempts != b.leases || b.batches > b.maxBatches || b.leases > b.maxLeases {
		t.Fatal("renewal attempts and accepted work differ", b)
	}
}
func (b *batchRenewalBudget) log(t *testing.T) {
	t.Helper()
	t.Logf("renewal envelope Bmax=%d Lmax=%d batches_attempted=%d batches_accepted=%d leases_attempted=%d leases_accepted=%d", b.maxBatches, b.maxLeases, b.batchAttempts, b.batches, b.leaseAttempts, b.leases)
}

func TestManagedQuiescenceBatchPostgresPositiveReserve(t *testing.T) {
	testManagedFiniteSetWithBatch(t, false, "positive-reserve", true)
}
func assertPostgresPositiveBatchReserve(t *testing.T, f *removalFixture) {
	t.Helper()

	s := f.s.Authority.(*postgres.Store)
	key := f.key

	record, err := s.ReadManagedRemovalAttempt(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}

	if record.State.Revision != "154" || len(record.Descriptor.ReservationIDs) != 64 {
		t.Fatal("incorrect admitted positive ledger", record.State.Revision)
	}

	for _, mask := range record.State.BindingSlots {
		if mask != 1|8 {
			t.Fatal("missing native Begin/fence association")
		}
	}

	positive, unissued := 0, 0

	for _, mask := range record.State.ProviderSlots {
		switch mask {
		case 1 | 2:
			positive++
		case 0:
			unissued++
		default:
			t.Fatal("unexpected provider slot", mask)
		}
	}

	if positive != 24 || unissued != 9 {
		t.Fatal("incorrect provider coverage", positive, unissued)
	}

	journalKey := managed.OperationKey{Key: key.Key, ActionID: f.s.action.ID}

	journal, err := s.ReadManagedOperation(t.Context(), journalKey)
	if err != nil {
		t.Fatal(err)
	}

	pg := pgdriver.Unwrap(s.DB())
	setRevision := func(value int64) {
		t.Helper()

		if _, err := pg.Exec(t.Context(), "UPDATE cp_managed_targets SET revision=$1,data=jsonb_set(data,'{revision}',to_jsonb($1::bigint)) WHERE id=$2", value, key.InstanceID.String()); err != nil {
			t.Fatal(err)
		}
	}
	setRevision(math.MaxInt64 - 206)

	q := batchRequest(t, f)
	before := attemptPostgresSnapshot(t, s)

	logPositiveBatchAuthority(t, f, q, 206, "pre-call")
	refused, err := s.AcceptManagedQuiescenceBatch(t.Context(), q)

	unchanged := reflect.DeepEqual(before, attemptPostgresSnapshot(t, s))
	if !errors.Is(err, managed.ErrCapacity) || !reflect.DeepEqual(refused, managed.QuiescenceBatchReceipt{}) {
		logPositiveBatchAuthority(t, f, q, 206, "unexpected post-call")
		t.Logf("206 unexpected unchanged=%t", unchanged)
		t.Fatal("206 headroom must refuse without a receipt", err)
	}

	if !unchanged {
		logPositiveBatchAuthority(t, f, q, 206, "unexpected post-call")
		t.Log("206 unexpected unchanged=false")
		t.Fatal("206 refusal changed rows or receipts")
	}

	setRevision(math.MaxInt64 - 209)
	q.Command.Revision = managed.Decimal(strconv.FormatUint(math.MaxInt64-209, 10))
	before = attemptPostgresSnapshot(t, s)
	logPositiveBatchAuthority(t, f, q, 209, "pre-call")

	receipt, err := s.AcceptManagedQuiescenceBatch(t.Context(), q)
	if err != nil {
		unchanged = reflect.DeepEqual(before, attemptPostgresSnapshot(t, s))
		logPositiveBatchAuthority(t, f, q, 209, "unexpected post-call")
		t.Logf("209 unexpected unchanged=%t", unchanged)
		t.Fatal("209 headroom must accept", err)
	}

	target, err := s.ReadManagedTarget(t.Context(), key.Key)
	if err != nil || target.Revision != math.MaxInt64-144 {
		t.Fatal("accepted batch did not leave144", err)
	}

	revision, err := receipt.Receipt.Revision.Uint64()
	if err != nil || revision != target.Revision {
		t.Fatal("receipt counter differs", err)
	}

	later, err := s.ReadManagedRemovalAttempt(t.Context(), key)
	if err != nil || !reflect.DeepEqual(record, later) {
		t.Fatal("batch changed attempt", err)
	}

	laterJournal, err := s.ReadManagedOperation(t.Context(), journalKey)
	if err != nil || !reflect.DeepEqual(journal, laterJournal) {
		t.Fatal("batch changed journal", err)
	}

	accepted := attemptPostgresSnapshot(t, s)
	logPositiveBatchAuthority(t, f, q, 144, "pre-replay")

	replay, err := s.AcceptManagedQuiescenceBatch(t.Context(), q)

	unchanged = reflect.DeepEqual(accepted, attemptPostgresSnapshot(t, s))
	if err != nil || !reflect.DeepEqual(receipt, replay) || !unchanged {
		logPositiveBatchAuthority(t, f, q, 144, "unexpected post-replay")
		t.Logf("replay unexpected unchanged=%t", unchanged)
		t.Fatal("capacity boundary replay changed", err)
	}

	t.Log("actual64bindings/24positive/9unissued:206refused unchanged;209accepted65,left144;attempt154 unchanged")
}

func logPositiveBatchAuthority(t *testing.T, f *removalFixture, q managed.QuiescenceBatchAcceptanceRequest, headroom int, phase string) {
	t.Helper()

	target, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil {
		t.Fatal(err)
	}

	now := quiescenceClock(t, f.s)

	margin := time.Minute
	for _, entry := range q.Entries {
		if left := entry.Observation.ValidUntil.Sub(now); left < margin {
			margin = left
		}
	}

	revision, _ := q.Command.Revision.Uint64()
	epoch, _ := q.Command.LeaseEpoch.Uint64()
	t.Logf("positive-reserve %s headroom=%d db_now=%s revision=%d epoch=%d owner=%q revision_match=%t epoch_match=%t owner_match=%t lease_deadline=%s lease_margin=%s new_proof_margin=%s", phase, headroom, now.Format(time.RFC3339Nano), target.Revision, target.LeaseEpoch, target.LeaseOwner, revision == target.Revision, epoch == target.LeaseEpoch, q.Command.Owner == target.LeaseOwner, target.LeaseUntil.Format(time.RFC3339Nano), target.LeaseUntil.Sub(now), margin)
}
