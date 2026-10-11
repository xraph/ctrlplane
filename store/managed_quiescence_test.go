package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/managedservice"
	"github.com/xraph/ctrlplane/store/postgres"
)

func quiescenceClock(t *testing.T, s managed.ControllerStore) time.Time {
	t.Helper()

	if action, ok := s.(*quiescenceActionStore); ok {
		return quiescenceClock(t, action.Authority)
	}

	if pg, ok := s.(*postgres.Store); ok {
		var micros int64
		if err := pgdriver.Unwrap(pg.DB()).NewRaw("SELECT (extract(epoch FROM clock_timestamp())*1000000)::bigint").Scan(t.Context(), &micros); err != nil {
			t.Fatal(err)
		}

		return time.UnixMicro(micros).UTC()
	}

	return time.Now().UTC()
}

func quiescenceCommand(t *testing.T, s managed.Store, k managed.Key) managed.Command {
	t.Helper()
	c := originalCommand(t, s, k)
	c.ReservationID = id.New(managed.PrefixReservation)

	local, err := c.LocalCommand()
	if err != nil {
		t.Fatal(err)
	}

	return local
}

func quiescenceRequest(t *testing.T, s managed.ControllerStore, a managed.OriginalRegistrationAcceptanceRequest, d managed.OriginalDrainAcceptanceRequest, incomplete bool) managed.QuiescenceAcceptanceRequest {
	t.Helper()

	reg, err := s.ReadManagedOriginalLifecycle(t.Context(), managed.OriginalLifecycleKey{Key: a.Command.Key, Kind: managed.OriginalRegistration, OperationID: a.Command.ID})
	if err != nil {
		t.Fatal(err)
	}

	drain, err := s.ReadManagedOriginalLifecycle(t.Context(), managed.OriginalLifecycleKey{Key: a.Command.Key, Kind: managed.OriginalDrain, OperationID: d.Command.ID})
	if err != nil {
		t.Fatal(err)
	}

	ref, err := managed.NewQuiescenceReference(reg, drain)
	if err != nil {
		t.Fatal(err)
	}

	c := originalCommand(t, s, a.Command.Key)
	c.ReservationID = a.Command.ReservationID
	stamp := quiescenceClock(t, s).Add(-time.Millisecond)

	o := managed.QuiescenceObservation{QuiescenceLeaf: managed.QuiescenceLeaf{Reference: ref, ObservedAt: stamp, ValidUntil: stamp.Add(time.Minute), AdmissionClosed: true, Quiescent: true, Complete: !incomplete, DeadlineExpired: incomplete, ActiveClaims: "0", UnknownClaims: "0", ObservationID: "source-observation", ChallengeID: "challenge", VerifierID: ref.Process.ProducerVerifier}, Attestation: []byte("synthetic authenticated-producer storage fixture")}
	if incomplete {
		o.ErrorClass = "incomplete"
	}

	signQuiescence(t, &o)

	return managed.QuiescenceAcceptanceRequest{Command: c, Observation: o}
}

func signQuiescence(t *testing.T, o *managed.QuiescenceObservation) {
	t.Helper()

	var err error

	o.ChallengeDigest, err = managed.QuiescenceChallengeDigest(o.QuiescenceLeaf)
	if err != nil {
		t.Fatal(err)
	}
}

func quiescenceSeal(t *testing.T, s managed.ControllerStore, k managed.Key) {
	t.Helper()

	if _, err := s.SealManagedTarget(t.Context(), quiescenceCommand(t, s, k)); err != nil {
		t.Fatal(err)
	}

	target, err := s.ReadManagedTarget(t.Context(), k)
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range target.Reservations {
		if v.Phase != managed.Accepted {
			continue
		}

		proof := []byte("fence-" + v.ID.String())

		_, err = s.RecordManagedFence(t.Context(), managed.FenceRequest{ReservationRequest: managed.ReservationRequest{Command: quiescenceCommand(t, s, k), ReservationID: v.ID}, Fence: managed.Fence{Epoch: 1, Evidence: proof, Digest: managed.Digest(proof), CompatibilityEvidence: []byte("retired"), ValidUntil: time.Now().UTC().Add(time.Hour), Survivors: []string{"off-instance"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestManagedQuiescenceTerminal(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, incomplete := range []bool{false, true} {
			t.Run(backend+strconv.FormatBool(incomplete), func(t *testing.T) {
				s := controllerStore(t, backend)
				a, b := acceptOriginalPair(t, s)
				d := originalDrain(t, s, a)
				d.Facts.Deadline = quiescenceClock(t, s).Add(3 * time.Second)

				originalReceipt, err := s.RecordManagedOriginalDrain(t.Context(), d)
				if err != nil {
					t.Fatal(err)
				}

				time.Sleep(time.Until(d.Facts.Deadline) + 10*time.Millisecond)
				quiescenceSeal(t, s, a.Command.Key)

				original, err := s.ReadManagedOriginalLifecycle(t.Context(), managed.OriginalLifecycleKey{Key: a.Command.Key, Kind: managed.OriginalDrain, OperationID: d.Command.ID})
				if err != nil {
					t.Fatal(err)
				}

				before, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
				if err != nil {
					t.Fatal(err)
				}

				q := quiescenceRequest(t, s, a, d, incomplete)

				receipt, err := s.AcceptManagedQuiescence(t.Context(), q)
				if err != nil {
					t.Fatal(err)
				}

				admitted := managed.AdmissionRequest{Command: quiescenceCommand(t, s, a.Command.Key), OperationID: id.New(managed.PrefixOperation), Verifier: "provider"}
				if _, err = s.AdmitManagedRemoval(t.Context(), admitted); !errors.Is(err, managed.ErrBlocked) {
					t.Fatal("unobserved sibling must remain blocked", err)
				}

				qb := quiescenceRequest(t, s, b, d, incomplete)
				if _, err = s.AcceptManagedQuiescence(t.Context(), qb); err != nil {
					t.Fatal(err)
				}

				after, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
				if err != nil || !reflect.DeepEqual(before.Reservations, after.Reservations) {
					t.Fatal("observation modified A evidence", err)
				}

				recovered, err := s.ReadManagedOriginalLifecycle(t.Context(), managed.OriginalLifecycleKey{Key: a.Command.Key, Kind: managed.OriginalDrain, OperationID: d.Command.ID})
				if err != nil || !reflect.DeepEqual(original, recovered) {
					t.Fatal("original material changed", err)
				}

				replay, err := s.RecordManagedOriginalDrain(t.Context(), d)
				if err != nil || replay != originalReceipt {
					t.Fatal("original receipt changed", err)
				}

				observed, err := s.ReadManagedQuiescence(t.Context(), managed.QuiescenceKey{Key: a.Command.Key, ReservationID: q.Command.ReservationID, OperationID: q.Command.ID})
				if err != nil || observed.Receipt != managed.OriginalReceipt(receipt) || observed.VerifiedAt.Before(q.Observation.ObservedAt) {
					t.Fatal("observation read", err)
				}

				admitted.Command = quiescenceCommand(t, s, a.Command.Key)
				if _, err = s.AdmitManagedRemoval(t.Context(), admitted); err != nil {
					t.Fatal(err)
				}

				issued := quiescenceCommand(t, s, a.Command.Key)
				if _, err = s.IssueManagedDeletion(t.Context(), issued); err != nil {
					t.Fatal(err)
				}
				// Both outcomes preserve the exact fence-set digest framing.
				type bindingFence struct {
					ID    id.ID         `json:"id"`
					Fence managed.Fence `json:"fence"`
				}

				fences := []bindingFence{}
				for _, v := range after.Reservations {
					fences = append(fences, bindingFence{v.ID, *v.Fence})
				}

				raw, err := json.Marshal(fences)
				if err != nil {
					t.Fatal(err)
				}

				final, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
				if err != nil || final.Deletion.FenceDigest != managed.Digest(raw) {
					t.Fatal("fence digest changed", err)
				}
			})
		}
	}
}

func TestManagedQuiescenceRefusals(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)
			a, _ := acceptOriginalPair(t, s)

			d := originalDrain(t, s, a)
			if _, err := s.RecordManagedOriginalDrain(t.Context(), d); err != nil {
				t.Fatal(err)
			}

			q := quiescenceRequest(t, s, a, d, false)

			cases := map[string]func(*managed.QuiescenceObservation){
				"runtime":   func(o *managed.QuiescenceObservation) { o.Reference.Process.RuntimeID = "other" },
				"operation": func(o *managed.QuiescenceObservation) { o.Reference.OperationID = "other" },
				"deadline":  func(o *managed.QuiescenceObservation) { o.Reference.Deadline = o.Reference.Deadline.Add(time.Second) },
				"host":      func(o *managed.QuiescenceObservation) { o.Reference.Process.HostID = "other" },
				"configuration": func(o *managed.QuiescenceObservation) {
					o.Reference.Process.ConfigurationDigest = strings.Repeat("f", 64)
				},
				"descriptor":  func(o *managed.QuiescenceObservation) { o.Reference.Process.Descriptor.BindingGeneration = "2" },
				"role":        func(o *managed.QuiescenceObservation) { o.Reference.Role = managed.RetainedQuery },
				"member":      func(o *managed.QuiescenceObservation) { o.Reference.Member = "sibling" },
				"reservation": func(o *managed.QuiescenceObservation) { o.Reference.ReservationID = id.New(managed.PrefixReservation) },
				"registration-reference": func(o *managed.QuiescenceObservation) {
					o.Reference.RegistrationReferenceDigest = strings.Repeat("f", 64)
				},
				"registration-material": func(o *managed.QuiescenceObservation) {
					o.Reference.RegistrationMaterialDigest = strings.Repeat("f", 64)
				},
				"drain-reference": func(o *managed.QuiescenceObservation) { o.Reference.DrainReferenceDigest = strings.Repeat("f", 64) },
				"drain-material":  func(o *managed.QuiescenceObservation) { o.Reference.DrainMaterialDigest = strings.Repeat("f", 64) },
				"absent-original": func(o *managed.QuiescenceObservation) { o.Reference.DrainOperationID = id.New(managed.PrefixOperation) },
				"target":          func(o *managed.QuiescenceObservation) { o.Reference.TargetIdentityDigest = strings.Repeat("f", 64) },
				"verifier":        func(o *managed.QuiescenceObservation) { o.VerifierID = "other" },
				"future": func(o *managed.QuiescenceObservation) {
					o.ObservedAt = time.Now().UTC().Add(time.Minute)
					o.ValidUntil = o.ObservedAt.Add(time.Second)
				},
				"expired": func(o *managed.QuiescenceObservation) {
					o.ObservedAt = time.Now().UTC().Add(-time.Second)
					o.ValidUntil = o.ObservedAt.Add(time.Millisecond)
				},
				"overlong":                  func(o *managed.QuiescenceObservation) { o.ValidUntil = o.ObservedAt.Add(time.Minute + time.Nanosecond) },
				"zero-source":               func(o *managed.QuiescenceObservation) { o.ObservedAt = time.Time{} },
				"zero-validity":             func(o *managed.QuiescenceObservation) { o.ValidUntil = o.ObservedAt },
				"active":                    func(o *managed.QuiescenceObservation) { o.ActiveClaims = "1" },
				"unknown":                   func(o *managed.QuiescenceObservation) { o.UnknownClaims = "1" },
				"reopened":                  func(o *managed.QuiescenceObservation) { o.AdmissionClosed = false },
				"nonquiescent":              func(o *managed.QuiescenceObservation) { o.Quiescent = false },
				"incomplete-without-expiry": func(o *managed.QuiescenceObservation) { o.Complete = false; o.ErrorClass = "incomplete" },
				"success-with-expiry":       func(o *managed.QuiescenceObservation) { o.DeadlineExpired = true },
				"attestation-overflow":      func(o *managed.QuiescenceObservation) { o.Attestation = bytes.Repeat([]byte{1}, 4097) },
			}
			for _, kind := range []string{"cancelled", "identity", "policy", "transport", "unavailable"} {
				cases[kind] = func(o *managed.QuiescenceObservation) { o.ErrorClass = kind }
			}

			for name, mutate := range cases {
				t.Run(name, func(t *testing.T) {
					bad := q
					mutate(&bad.Observation)
					// Recompute the handle and challenge so association checks cannot rely on a stale digest.
					ref := &bad.Observation.Reference
					ref.HandleDigest, _ = managed.OriginalDrainHandleDigest(ref.TargetIdentityDigest, managed.OriginalDrainFacts{Process: ref.Process, OperationID: ref.OperationID, Deadline: ref.Deadline})

					signQuiescence(t, &bad.Observation)

					before, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
					if err != nil {
						t.Fatal(err)
					}

					if _, err = s.AcceptManagedQuiescence(t.Context(), bad); err == nil {
						t.Fatal("invalid observation accepted")
					}

					after, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
					if err != nil || !reflect.DeepEqual(before, after) {
						t.Fatal("rejected observation changed parent", err)
					}
				})
			}

			q.Observation.Attestation = bytes.Repeat([]byte{255}, 4096)
			if _, err := s.AcceptManagedQuiescence(t.Context(), q); err != nil {
				t.Fatal("exact attestation bound", err)
			}
		})
	}
}

func TestManagedQuiescenceReplayAndMonotonicity(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)
			a, _ := acceptOriginalPair(t, s)

			d := originalDrain(t, s, a)
			if _, err := s.RecordManagedOriginalDrain(t.Context(), d); err != nil {
				t.Fatal(err)
			}

			q := quiescenceRequest(t, s, a, d, false)
			q.Observation.ObservedAt = quiescenceClock(t, s).Add(-100 * time.Millisecond)
			q.Observation.ValidUntil = q.Observation.ObservedAt.Add(3 * time.Second)
			signQuiescence(t, &q.Observation)

			receipt, err := s.AcceptManagedQuiescence(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}

			for _, delta := range []time.Duration{0, -time.Millisecond} {
				other := q
				other.Command = originalCommand(t, s, a.Command.Key)
				other.Command.ReservationID = q.Command.ReservationID
				other.Observation.ObservedAt = other.Observation.ObservedAt.Add(delta)
				signQuiescence(t, &other.Observation)

				if _, err = s.AcceptManagedQuiescence(t.Context(), other); !errors.Is(err, managed.ErrConflict) {
					t.Fatal("equal/older replaced current", err)
				}
			}

			newer := quiescenceRequest(t, s, a, d, false)
			if _, err = s.AcceptManagedQuiescence(t.Context(), newer); err != nil {
				t.Fatal(err)
			}

			time.Sleep(time.Until(q.Observation.ValidUntil) + time.Millisecond)

			c := quiescenceCommand(t, s, a.Command.Key)
			if _, err = s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: c, Duration: time.Nanosecond}); err != nil {
				t.Fatal(err)
			}

			time.Sleep(time.Millisecond)

			c = quiescenceCommand(t, s, a.Command.Key)

			c.Owner = "takeover"
			if _, err = s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: c, Duration: managed.MaxLease}); err != nil {
				t.Fatal(err)
			}

			replay, err := s.AcceptManagedQuiescence(t.Context(), q)
			if err != nil || replay != receipt {
				t.Fatal("expired historical replay after takeover", err)
			}

			other := q

			other.Observation.Attestation = []byte("altered")
			if _, err = s.AcceptManagedQuiescence(t.Context(), other); !errors.Is(err, managed.ErrConflict) {
				t.Fatal("altered replay", err)
			}

			sameTime := newer
			sameTime.Command = originalCommand(t, s, a.Command.Key)

			sameTime.Command.ReservationID = q.Command.ReservationID
			if _, err = s.AcceptManagedQuiescence(t.Context(), sameTime); !errors.Is(err, managed.ErrConflict) {
				t.Fatal("historical replay rolled current back", err)
			}
		})
	}
}

// quiescenceActionStore gives the shared original fixture a real accepted action and projection.
type quiescenceActionStore struct {
	managedservice.Authority

	t      *testing.T
	action managed.ActionSubmission
}

func (s *quiescenceActionStore) CreateManagedTarget(ctx context.Context, i managed.Identity) (*managed.Target, error) {
	s.action = controllerAction(s.t)
	s.action.Key = i.Key
	s.action.NewTarget = &i
	signControllerAction(s.t, &s.action)

	if _, err := s.SubmitManagedAction(ctx, s.action); err != nil {
		return nil, err
	}

	return s.ReadManagedTarget(ctx, i.Key)
}

func (s *quiescenceActionStore) IssueManagedProvision(ctx context.Context, c managed.Command) (managed.Receipt, error) {
	if _, err := s.ProjectManagedRows(ctx, managed.ProjectionRequest{Command: quiescenceCommand(s.t, s, c.Key), ActionID: s.action.ID, Stage: managed.ProjectionPending}); err != nil {
		return managed.Receipt{}, err
	}

	fresh := quiescenceCommand(s.t, s, c.Key)
	fresh.ID = c.ID

	return s.Authority.IssueManagedProvision(ctx, fresh)
}

func TestManagedQuiescenceProviderExpiry(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := &quiescenceActionStore{Authority: controllerStore(t, backend), t: t}
			a, b := acceptOriginalPair(t, s)

			d := originalDrain(t, s, a)
			if _, err := s.RecordManagedOriginalDrain(t.Context(), d); err != nil {
				t.Fatal(err)
			}

			quiescenceSeal(t, s, a.Command.Key)

			requests := []managed.QuiescenceAcceptanceRequest{quiescenceRequest(t, s, a, d, false), quiescenceRequest(t, s, b, d, false)}

			observed := quiescenceClock(t, s).Add(-time.Millisecond)
			expiry := observed.Add(5 * time.Second)

			for _, q := range requests {
				rid := q.Command.ReservationID
				q.Command = originalCommand(t, s, a.Command.Key)
				q.Command.ReservationID = rid

				q.Observation.ObservedAt = observed
				q.Observation.ValidUntil = expiry
				signQuiescence(t, &q.Observation)

				if _, err := s.AcceptManagedQuiescence(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}

			admission := managed.AdmissionRequest{Command: quiescenceCommand(t, s, a.Command.Key), OperationID: id.New(managed.PrefixOperation), Verifier: "provider"}
			if _, err := s.AdmitManagedRemoval(t.Context(), admission); err != nil {
				t.Fatal(err)
			}

			first := managed.ProviderStepAuthorization{Command: quiescenceCommand(t, s, a.Command.Key), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: a.Command.Key, ActionID: s.action.ID}, ProviderOperationID: admission.OperationID, Kind: managed.StepRemoveContainer, Service: "api"}}

			grant, err := s.AuthorizeManagedProviderStep(t.Context(), first)
			if err != nil || !grant.NewlyIssued {
				t.Fatal("first removal step", err)
			}

			if err = s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: grant.Permission.ResourceID, Evidence: []byte("removed"), ObservedAt: quiescenceClock(t, s)}); err != nil {
				t.Fatal(err)
			}

			time.Sleep(time.Until(expiry) + time.Millisecond)

			replay, err := s.AuthorizeManagedProviderStep(t.Context(), first)
			if err != nil || replay.NewlyIssued || replay.Permission != grant.Permission {
				t.Fatal("permission replay granted another send", err)
			}

			second := first
			second.Command = quiescenceCommand(t, s, a.Command.Key)
			second.Step.Service = "sidecar"

			before, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
			if err != nil {
				t.Fatal(err)
			}

			if _, err = s.AuthorizeManagedProviderStep(t.Context(), second); !errors.Is(err, managed.ErrBlocked) {
				t.Fatal("expired observation authorized fresh step", err)
			}

			after, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("failed fresh step wrote parent", err)
			}

			if _, err = s.ReadManagedProviderStep(t.Context(), second.Step); !errors.Is(err, managed.ErrNotFound) {
				t.Fatal("failed fresh step persisted", err)
			}

			for _, reg := range []managed.OriginalRegistrationAcceptanceRequest{a, b} {
				q := quiescenceRequest(t, s, reg, d, false)
				if _, err = s.AcceptManagedQuiescence(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}

			second.Command = quiescenceCommand(t, s, a.Command.Key)
			if result, err := s.AuthorizeManagedProviderStep(t.Context(), second); err != nil || !result.NewlyIssued {
				t.Fatal("refreshed proof did not permit next exact step", err)
			}
		})
	}
}

func TestManagedQuiescenceIssueExpiry(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)
			a, b := acceptOriginalPair(t, s)

			d := originalDrain(t, s, a)
			if _, err := s.RecordManagedOriginalDrain(t.Context(), d); err != nil {
				t.Fatal(err)
			}

			quiescenceSeal(t, s, a.Command.Key)

			requests := []managed.QuiescenceAcceptanceRequest{quiescenceRequest(t, s, a, d, false), quiescenceRequest(t, s, b, d, false)}

			observed := quiescenceClock(t, s).Add(-time.Millisecond)
			expiry := observed.Add(5 * time.Second)

			for _, q := range requests {
				rid := q.Command.ReservationID
				q.Command = originalCommand(t, s, a.Command.Key)
				q.Command.ReservationID = rid

				q.Observation.ObservedAt = observed
				q.Observation.ValidUntil = expiry
				signQuiescence(t, &q.Observation)

				if _, err := s.AcceptManagedQuiescence(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}

			admission := managed.AdmissionRequest{Command: quiescenceCommand(t, s, a.Command.Key), OperationID: id.New(managed.PrefixOperation), Verifier: "provider"}

			saved, err := s.AdmitManagedRemoval(t.Context(), admission)
			if err != nil {
				t.Fatal(err)
			}

			time.Sleep(time.Until(expiry) + time.Millisecond)

			if receipt, err := s.AdmitManagedRemoval(t.Context(), admission); err != nil || receipt != saved {
				t.Fatal("expired accepted admission replay", err)
			}

			if _, err = s.IssueManagedDeletion(t.Context(), quiescenceCommand(t, s, a.Command.Key)); !errors.Is(err, managed.ErrBlocked) {
				t.Fatal("expired observation issued deletion", err)
			}
		})
	}
}

func TestManagedQuiescenceEncodedLimits(t *testing.T) {
	s := controllerStore(t, "memory")
	a, _ := acceptOriginalPair(t, s)

	d := originalDrain(t, s, a)
	if _, err := s.RecordManagedOriginalDrain(t.Context(), d); err != nil {
		t.Fatal(err)
	}

	q := quiescenceRequest(t, s, a, d, false)
	maxToken := strings.Repeat("x", 256)
	maxDigest := strings.Repeat("a", 64)
	maxCounter := managed.Decimal(strconv.FormatInt(math.MaxInt64, 10))
	stamp := time.Date(9999, 12, 31, 23, 59, 58, 999999999, time.UTC)
	q.Command.TenantID = maxToken
	q.Command.Owner = maxToken
	q.Command.Revision = "9223372036854775806"
	q.Command.LeaseEpoch = maxCounter
	o := &q.Observation
	r := &o.Reference
	p := &r.Process
	p.Installation = maxToken
	p.Namespace = maxToken
	p.Build = maxToken
	p.Queue = maxToken
	p.RuntimeID = maxToken
	p.InstanceID = maxToken
	p.HostID = maxToken
	p.ConfigurationVersion = maxToken
	p.ProbePolicyID = maxToken
	p.VerifierID = maxToken
	p.Descriptor.BindingID = maxToken
	p.ProducerVerifier = maxToken
	p.ArtifactDigest = maxDigest
	p.ConfigurationDigest = maxDigest
	p.EnrollmentEvidenceDigest = maxDigest
	p.Descriptor.BindingDigest = maxDigest
	p.ProbePolicyVersion = maxCounter
	p.MaximumProofValidityNS = maxCounter
	p.Descriptor.BindingGeneration = maxCounter
	r.TargetIdentityDigest = maxDigest
	r.Member = maxToken
	r.Role = managed.RetainedQuery
	r.RegistrationReferenceDigest = maxDigest
	r.RegistrationMaterialDigest = maxDigest
	r.DrainReferenceDigest = maxDigest
	r.DrainMaterialDigest = maxDigest
	r.OperationID = maxToken
	r.Deadline = stamp

	var err error

	r.HandleDigest, err = managed.OriginalDrainHandleDigest(r.TargetIdentityDigest, managed.OriginalDrainFacts{Process: *p, OperationID: r.OperationID, Deadline: r.Deadline})
	if err != nil {
		t.Fatal(err)
	}

	o.ObservedAt = stamp
	o.ValidUntil = stamp.Add(time.Second)
	o.ObservationID = maxToken
	o.ChallengeID = maxToken
	o.VerifierID = maxToken

	o.Attestation = bytes.Repeat([]byte{255}, 4096)
	for _, incomplete := range []bool{false, true} {
		o.Complete = !incomplete
		o.DeadlineExpired = incomplete

		o.ErrorClass = ""
		if incomplete {
			o.ErrorClass = "incomplete"
		}

		signQuiescence(t, o)

		rec := managed.QuiescenceRecord{Request: q, VerifiedAt: stamp, Receipt: managed.OriginalLocalReceipt{ID: q.Command.ID, Digest: maxDigest, Revision: maxCounter, LeaseEpoch: maxCounter, AcceptedAt: stamp, LeaseUntil: stamp}}

		raw, err := rec.Bytes()
		if err != nil {
			t.Fatal(err)
		}

		digest, err := managed.QuiescenceRecordDigest(rec)
		if err != nil {
			t.Fatal(err)
		}

		index := managed.QuiescenceIndex{Schema: "ctrlplane.quiescence-current.v1", ReservationID: q.Command.ReservationID, OperationID: q.Command.ID, ObservedAt: stamp, RecordDigest: digest}

		current, err := index.Bytes()
		if err != nil {
			t.Fatal(err)
		}

		leaf, err := json.Marshal(o.QuiescenceLeaf)
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("actual maximal incomplete=%v row=%d index=%d leaf=%d attestation=%d", incomplete, len(raw), len(current), len(leaf), len(o.Attestation))

		if _, err = managed.ParseQuiescenceRecord(raw); err != nil {
			t.Fatal(err)
		}

		if _, err = managed.ParseQuiescenceIndex(current); err != nil {
			t.Fatal(err)
		}

		for _, bad := range [][]byte{append([]byte(" "), raw...), append(bytes.Clone(raw), []byte("null")...), bytes.Replace(raw, []byte(`"request":`), []byte(`"unknown":`), 1), bytes.Replace(raw, []byte(`"active_claims":"0"`), []byte(`"active_claims":0`), 1), bytes.Replace(raw, []byte(`"verified_at":`), []byte(`"verified_at":null,"verified_at":`), 1), bytes.Replace(raw, []byte(`"attestation":`), []byte(`"attestation":null,"attestation":`), 1)} {
			if _, err = managed.ParseQuiescenceRecord(bad); err == nil {
				t.Fatal("noncanonical record accepted")
			}
		}

		for _, bad := range [][]byte{append([]byte(" "), current...), append(bytes.Clone(current), []byte("null")...), bytes.Replace(current, []byte(`"schema":`), []byte(`"unknown":`), 1), bytes.Replace(current, []byte(`"operation_id":`), []byte(`"operation_id":null,"operation_id":`), 1)} {
			if _, err = managed.ParseQuiescenceIndex(bad); err == nil {
				t.Fatal("noncanonical index accepted")
			}
		}
	}

	if _, err = managed.ParseQuiescenceRecord(bytes.Repeat([]byte{' '}, 16385)); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal(err)
	}

	if _, err = managed.ParseQuiescenceIndex(bytes.Repeat([]byte{' '}, 1025)); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal(err)
	}
}

func TestManagedQuiescencePostgresLock(t *testing.T) {
	s, dsn := managedPG(t)
	a, _ := acceptOriginalPair(t, s)

	d := originalDrain(t, s, a)
	if _, err := s.RecordManagedOriginalDrain(t.Context(), d); err != nil {
		t.Fatal(err)
	}

	q := quiescenceRequest(t, s, a, d, false)

	lock := pgdriver.New()
	if err := lock.Open(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = lock.Close() })

	tx, err := lock.BeginTxQuery(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = tx.Rollback() })

	var raw []byte
	if err = tx.NewRaw("SELECT data FROM cp_managed_targets WHERE id=$1 FOR UPDATE", a.Command.InstanceID.String()).Scan(t.Context(), &raw); err != nil {
		t.Fatal(err)
	}

	q.Observation.ObservedAt = quiescenceClock(t, s).Add(-time.Millisecond)
	q.Observation.ValidUntil = q.Observation.ObservedAt.Add(300 * time.Millisecond)
	signQuiescence(t, &q.Observation)

	result := make(chan error, 1)

	go func() { _, e := s.AcceptManagedQuiescence(t.Context(), q); result <- e }()

	deadline := time.Now().Add(3 * time.Second)

	for {
		var count int
		if err = pgdriver.Unwrap(s.DB()).NewRaw("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%cp_managed_targets%'").Scan(t.Context(), &count); err != nil {
			t.Fatal(err)
		}

		if count > 0 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("acceptance never blocked on actual row lock")
		}

		time.Sleep(5 * time.Millisecond)
	}

	time.Sleep(time.Until(q.Observation.ValidUntil) + time.Millisecond)

	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err = <-result; !errors.Is(err, managed.ErrBlocked) {
		t.Fatal("post-lock expiry accepted", err)
	}

	target, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
	if err != nil || strconv.FormatUint(target.Revision, 10) != string(q.Command.Revision) {
		t.Fatal("expired acceptance mutated target", err)
	}
}

func TestManagedQuiescencePostgresRecoveryAndCorruption(t *testing.T) {
	s, dsn := managedPG(t)
	a, _ := acceptOriginalPair(t, s)

	d := originalDrain(t, s, a)
	if _, err := s.RecordManagedOriginalDrain(t.Context(), d); err != nil {
		t.Fatal(err)
	}

	q := quiescenceRequest(t, s, a, d, false)

	receipt, err := s.AcceptManagedQuiescence(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}

	k := managed.QuiescenceKey{Key: a.Command.Key, ReservationID: q.Command.ReservationID, OperationID: q.Command.ID}
	peer := openManagedPG(t, dsn)

	saved, err := peer.ReadManagedQuiescence(t.Context(), k)
	if err != nil || saved.Receipt != managed.OriginalReceipt(receipt) {
		t.Fatal("independent store read", err)
	}

	replay, err := peer.AcceptManagedQuiescence(t.Context(), q)
	if err != nil || replay != receipt {
		t.Fatal("independent store replay", err)
	}

	pg := pgdriver.Unwrap(s.DB())

	scope, err := managed.QuiescenceScope(k.Key, k.ReservationID)
	if err != nil {
		t.Fatal(err)
	}

	var history, current, local, original []byte
	for _, entry := range []struct {
		kind, key string
		dst       *[]byte
	}{{managed.QuiescenceHistory, k.OperationID.String(), &history}, {managed.QuiescenceCurrent, "current", &current}, {managed.OriginalDrain, d.Command.ID.String(), &original}} {
		if err = pg.NewRaw("SELECT data FROM cp_managed_controller_records WHERE kind=$1 AND key=$2", entry.kind, entry.key).Scan(t.Context(), entry.dst); err != nil {
			t.Fatal(err)
		}
	}

	if err = pg.NewRaw("SELECT receipt::text::bytea FROM cp_managed_commands WHERE operation_id=$1", q.Command.ID.String()).Scan(t.Context(), &local); err != nil {
		t.Fatal(err)
	}

	originalScopeRaw, err := json.Marshal(k.Key)
	if err != nil {
		t.Fatal(err)
	}

	mutations := map[string]string{
		"missing-history":  "DELETE FROM cp_managed_controller_records WHERE kind='quiescence_observation'",
		"corrupt-history":  "UPDATE cp_managed_controller_records SET data='{}'::bytea WHERE kind='quiescence_observation'",
		"missing-current":  "DELETE FROM cp_managed_controller_records WHERE kind='quiescence_current'",
		"corrupt-current":  "UPDATE cp_managed_controller_records SET data='{}'::bytea WHERE kind='quiescence_current'",
		"missing-original": "DELETE FROM cp_managed_controller_records WHERE kind='original_drain'",
		"corrupt-original": "UPDATE cp_managed_controller_records SET data='{}'::bytea WHERE kind='original_drain'",
		"missing-receipt":  "DELETE FROM cp_managed_commands WHERE operation_id=$1",
		"corrupt-receipt":  "UPDATE cp_managed_commands SET receipt=jsonb_set(receipt,'{revision}','999') WHERE operation_id=$1",
	}
	for name, query := range mutations {
		t.Run(name, func(t *testing.T) {
			args := []any{}
			if strings.Contains(query, "$1") {
				args = append(args, q.Command.ID.String())
			}

			if _, err := pg.Exec(t.Context(), query, args...); err != nil {
				t.Fatal(err)
			}

			if _, err := s.ReadManagedQuiescence(t.Context(), k); err == nil {
				t.Fatal("corrupt read accepted")
			}

			if _, err := s.AcceptManagedQuiescence(t.Context(), q); err == nil {
				t.Fatal("corrupt replay repaired")
			}

			newer := q
			newer.Command = originalCommand(t, s, k.Key)
			newer.Command.ReservationID = k.ReservationID
			newer.Observation.ObservedAt = quiescenceClock(t, s)
			newer.Observation.ValidUntil = newer.Observation.ObservedAt.Add(time.Minute)
			signQuiescence(t, &newer.Observation)

			if _, err := s.AcceptManagedQuiescence(t.Context(), newer); err == nil {
				t.Fatal("fresh acceptance repaired missing/corrupt history")
			}

			for _, entry := range []struct {
				kind, scope, key string
				raw              []byte
			}{{managed.QuiescenceHistory, scope, k.OperationID.String(), history}, {managed.QuiescenceCurrent, scope, "current", current}, {managed.OriginalDrain, managed.Digest(originalScopeRaw), d.Command.ID.String(), original}} {
				if _, err := pg.Exec(t.Context(), "INSERT INTO cp_managed_controller_records(kind,scope,key,data) VALUES($1,$2,$3,$4) ON CONFLICT(kind,scope,key) DO UPDATE SET data=excluded.data", entry.kind, entry.scope, entry.key, entry.raw); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := pg.Exec(t.Context(), "INSERT INTO cp_managed_commands(target_id,operation_id,receipt) VALUES($1,$2,$3::jsonb) ON CONFLICT(target_id,operation_id) DO UPDATE SET receipt=excluded.receipt", k.InstanceID.String(), k.OperationID.String(), string(local)); err != nil {
				t.Fatal(err)
			}
		})
	}

	index, err := managed.ParseQuiescenceIndex(current)
	if err != nil {
		t.Fatal(err)
	}

	index.OperationID = id.New(managed.PrefixOperation)

	bad, err := index.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	if _, err = pg.Exec(t.Context(), "UPDATE cp_managed_controller_records SET data=$1 WHERE kind='quiescence_current'", bad); err != nil {
		t.Fatal(err)
	}

	if _, err = s.AcceptManagedQuiescence(t.Context(), q); err == nil {
		t.Fatal("current target corruption accepted")
	}

	for kind, size := range map[string]int{managed.QuiescenceHistory: 16385, managed.QuiescenceCurrent: 1025} {
		if _, err = pg.Exec(t.Context(), "INSERT INTO cp_managed_controller_records(kind,scope,key,data) VALUES($1,'overflow','overflow',$2)", kind, bytes.Repeat([]byte{1}, size)); err == nil {
			t.Fatal("constraint accepted overflow", kind)
		}
	}

	if _, err = pg.Exec(t.Context(), "INSERT INTO cp_managed_controller_records(kind,scope,key,data) VALUES('unrelated','overflow','overflow',$1)", bytes.Repeat([]byte{1}, 16385)); err != nil {
		t.Fatal("constraint affected unrelated kind", err)
	}
}

func TestManagedQuiescencePostgresRollback(t *testing.T) {
	s, _ := managedPG(t)
	a, _ := acceptOriginalPair(t, s)

	d := originalDrain(t, s, a)
	if _, err := s.RecordManagedOriginalDrain(t.Context(), d); err != nil {
		t.Fatal(err)
	}

	q := quiescenceRequest(t, s, a, d, false)
	originalPGRollback(t, s, a.Command.Key, q.Command.ID, func() (managed.Receipt, error) { return s.AcceptManagedQuiescence(t.Context(), q) })

	pg := pgdriver.Unwrap(s.DB())
	if _, err := pg.Exec(t.Context(), "CREATE FUNCTION reject_quiescence_index() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='quiescence_current' THEN RAISE EXCEPTION 'injected index failure'; END IF; RETURN NEW; END $$"); err != nil {
		t.Fatal(err)
	}

	if _, err := pg.Exec(t.Context(), "CREATE TRIGGER reject_quiescence_index BEFORE INSERT OR UPDATE ON cp_managed_controller_records FOR EACH ROW EXECUTE FUNCTION reject_quiescence_index()"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AcceptManagedQuiescence(t.Context(), q); err == nil {
		t.Fatal("index write failure accepted")
	}

	var count int
	if err := pg.NewRaw("SELECT count(*) FROM cp_managed_controller_records WHERE kind IN ('quiescence_current','quiescence_observation')").Scan(t.Context(), &count); err != nil || count != 0 {
		t.Fatal("partial observation/index", count, err)
	}

	if err := pg.NewRaw("SELECT count(*) FROM cp_managed_commands WHERE operation_id=$1", q.Command.ID.String()).Scan(t.Context(), &count); err != nil || count != 0 {
		t.Fatal("partial local receipt", count, err)
	}

	if _, err := pg.Exec(t.Context(), "DROP TRIGGER reject_quiescence_index ON cp_managed_controller_records"); err != nil {
		t.Fatal(err)
	}

	if _, err := pg.Exec(t.Context(), "DROP FUNCTION reject_quiescence_index()"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AcceptManagedQuiescence(t.Context(), q); err != nil {
		t.Fatal("rollback recovery", err)
	}
}

var _ managed.ControllerStore = (*postgres.Store)(nil)

func TestManagedQuiescenceLastIncarnation(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)
			a, b := acceptOriginalPair(t, s)
			// Fill the actual 64-reservation set. The last issued incarnation remains unresolved.
			var last managed.Registration

			for n := 2; n < managed.MaxReservations; n++ {
				body := []byte("incarnation-" + strconv.Itoa(n))

				last = managed.Registration{ID: id.New(managed.PrefixReservation), Member: a.Facts.Member, RuntimeID: "runtime-" + strconv.Itoa(n), InstanceID: a.Facts.Process.InstanceID, HostID: "host", RequestID: "request-" + strconv.Itoa(n), Command: body, CommandDigest: managed.Digest(body)}
				if _, err := s.ReserveManagedRegistration(t.Context(), managed.ReserveRequest{Command: quiescenceCommand(t, s, a.Command.Key), Registration: last}); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := s.IssueManagedRegistration(t.Context(), managed.ReservationRequest{Command: quiescenceCommand(t, s, a.Command.Key), ReservationID: last.ID}); err != nil {
				t.Fatal(err)
			}

			if _, err := s.ObserveManagedUnknown(t.Context(), managed.ReservationRequest{Command: quiescenceCommand(t, s, a.Command.Key), ReservationID: last.ID}); err != nil {
				t.Fatal(err)
			}

			d := originalDrain(t, s, a)
			if _, err := s.RecordManagedOriginalDrain(t.Context(), d); err != nil {
				t.Fatal(err)
			}

			quiescenceSeal(t, s, a.Command.Key)

			for _, reg := range []managed.OriginalRegistrationAcceptanceRequest{a, b} {
				q := quiescenceRequest(t, s, reg, d, false)
				if _, err := s.AcceptManagedQuiescence(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := s.AdmitManagedRemoval(t.Context(), managed.AdmissionRequest{Command: quiescenceCommand(t, s, a.Command.Key), OperationID: id.New(managed.PrefixOperation), Verifier: "provider"}); !errors.Is(err, managed.ErrBlocked) {
				t.Fatal("last unresolved incarnation was skipped", err)
			}

			target, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
			if err != nil || len(target.Reservations) != 64 || !target.Reservations[63].Unknown || target.Deletion != nil {
				t.Fatal("full set fixture lost unresolved obligation", err)
			}
		})
	}
}

func TestManagedQuiescencePostgresHistoricalReplayCorruption(t *testing.T) {
	for _, failure := range []string{"newer-history", "newer-receipt"} {
		t.Run(failure, func(t *testing.T) {
			s, _ := managedPG(t)
			a, _ := acceptOriginalPair(t, s)

			d := originalDrain(t, s, a)
			if _, err := s.RecordManagedOriginalDrain(t.Context(), d); err != nil {
				t.Fatal(err)
			}

			older := quiescenceRequest(t, s, a, d, false)
			if _, err := s.AcceptManagedQuiescence(t.Context(), older); err != nil {
				t.Fatal(err)
			}

			newer := older
			newer.Command = originalCommand(t, s, a.Command.Key)
			newer.Command.ReservationID = older.Command.ReservationID
			newer.Observation.ObservedAt = quiescenceClock(t, s)
			newer.Observation.ValidUntil = newer.Observation.ObservedAt.Add(time.Minute)
			newer.Observation.ObservationID = "newer-observation"
			signQuiescence(t, &newer.Observation)

			if _, err := s.AcceptManagedQuiescence(t.Context(), newer); err != nil {
				t.Fatal(err)
			}

			if _, err := s.AcceptManagedQuiescence(t.Context(), older); err != nil {
				t.Fatal("valid historical replay", err)
			}

			pg := pgdriver.Unwrap(s.DB())

			scope, err := managed.QuiescenceScope(older.Command.Key, older.Command.ReservationID)
			if err != nil {
				t.Fatal(err)
			}

			if failure == "newer-history" {
				_, err = pg.Exec(t.Context(), "UPDATE cp_managed_controller_records SET data='{}'::bytea WHERE kind=$1 AND scope=$2 AND key=$3", managed.QuiescenceHistory, scope, newer.Command.ID.String())
			} else {
				_, err = pg.Exec(t.Context(), "UPDATE cp_managed_commands SET receipt=jsonb_set(receipt,'{revision}','999') WHERE target_id=$1 AND operation_id=$2", newer.Command.InstanceID.String(), newer.Command.ID.String())
			}

			if err != nil {
				t.Fatal(err)
			}

			snapshot := func() []string {
				out := []string{}

				for _, table := range []string{"cp_managed_targets", "cp_managed_commands", "cp_managed_controller_records"} {
					var raw string
					if e := pg.NewRaw("SELECT COALESCE(jsonb_agg(rowdata ORDER BY rowdata::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) AS rowdata FROM "+table+" t) x").Scan(t.Context(), &raw); e != nil {
						t.Fatal(e)
					}

					out = append(out, raw)
				}

				return out
			}
			before := snapshot()

			if _, err = s.AcceptManagedQuiescence(t.Context(), older); err == nil {
				t.Fatal("historical replay ignored corrupt newer selection")
			}

			if after := snapshot(); !reflect.DeepEqual(after, before) {
				t.Fatal("historical replay mutated or repaired parent, receipt, history or index")
			}
		})
	}
}
