package managed

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// RuntimeRole fixes the permitted startup behavior of an immutable member.
type RuntimeRole string

const (
	// Polling permits enrollment followed by separately authorized worker startup.
	Polling RuntimeRole = "polling"
	// RetainedQuery permits historical queries with permanently closed admission.
	RetainedQuery RuntimeRole = "retained_query"
)

// ManagedSpec preserves role order alongside the immutable provider configuration.
// Parsing this envelope does not validate provider execution or remote readiness.
type ManagedSpec struct {
	Profile      string          `json:"profile"`
	RuntimeRoles []RuntimeRole   `json:"runtime_roles"`
	Provision    json.RawMessage `json:"provision"`
}

// ParseManagedSpec validates the explicit controller profile without rewriting bytes.
// Legacy manifests remain readable through Store but cannot gain new issue permission.
func ParseManagedSpec(m Manifest) (ManagedSpec, error) {
	var spec ManagedSpec
	if len(m.Spec) == 0 || len(m.Spec) > MaxManifestSpec || Digest(m.Spec) != m.SpecDigest || len(m.Members) == 0 || len(m.Members) > MaxMembers {
		return spec, ErrInvalid
	}

	if err := strictObject(m.Spec, &spec); err != nil {
		return spec, err
	}

	if spec.Profile != "ctrlplane.managed.v1" || len(spec.RuntimeRoles) != len(m.Members) || len(spec.Provision) == 0 || spec.Provision[0] != '{' {
		return ManagedSpec{}, ErrInvalid
	}

	for _, role := range spec.RuntimeRoles {
		if role != Polling && role != RetainedQuery {
			return ManagedSpec{}, ErrInvalid
		}
	}

	return spec, nil
}

// Decimal is the canonical wire encoding for a nonnegative integer.
// Consumers apply their own semantic bounds after decoding.
type Decimal string

// Uint64 rejects signs, leading zeroes, whitespace and overflow.
func (d Decimal) Uint64() (uint64, error) {
	v, err := strconv.ParseUint(string(d), 10, 64)
	if err != nil || strconv.FormatUint(v, 10) != string(d) {
		return 0, ErrInvalid
	}

	return v, nil
}

// strictObject rejects duplicate keys and null values throughout bounded input.
// The second pass rejects fields which the versioned destination does not define.
func strictObject(raw []byte, into any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()

	if err := strictValue(d); err != nil {
		return err
	}

	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}

	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()

	if err := d.Decode(into); err != nil {
		return ErrInvalid
	}

	return nil
}

func strictValue(d *json.Decoder) error {
	v, err := d.Token()
	if err != nil || v == nil {
		return ErrInvalid
	}

	switch v {
	case json.Delim('{'):
		seen := map[string]bool{}

		for d.More() {
			key, err := d.Token()

			name, ok := key.(string)
			if err != nil || !ok || seen[name] {
				return ErrInvalid
			}

			seen[name] = true

			if err = strictValue(d); err != nil {
				return err
			}
		}

		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalid
		}
	case json.Delim('['):
		for d.More() {
			if err := strictValue(d); err != nil {
				return err
			}
		}

		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return ErrInvalid
		}
	}

	return nil
}

// ActionKind identifies the finite set of accepted operator decisions.
type ActionKind string

const (
	ActionDeploy   ActionKind = "deploy"
	ActionPause    ActionKind = "pause"
	ActionRetire   ActionKind = "retire"
	ActionRollback ActionKind = "rollback"
	// ActionContinue is reserved and refused until settled continuation is implemented.
	ActionContinue ActionKind = "continue"
)

// AuditScope identifies verified tenant, explicitly authorized global or denial scope.
type AuditScope struct {
	Kind        string `json:"kind"`
	TenantID    string `json:"tenant_id"`
	DenialScope string `json:"denial_scope"`
}

// AuditActor contains only verified identity or an explicit unauthenticated marker.
type AuditActor struct {
	Subject         string `json:"subject"`
	Unauthenticated bool   `json:"unauthenticated"`
}

// ActionSubmission freezes authenticated command content and reserved identities.
// The trusted service authenticates first; this persistence interface is not public.
type ActionSubmission struct {
	Command

	Kind                    ActionKind `json:"kind"`
	Installation            string     `json:"installation"`
	Authority               string     `json:"authority"`
	ApplicationID           string     `json:"application_id"`
	OrganizationID          string     `json:"organization_id"`
	Actor                   AuditActor `json:"actor"`
	OriginalCommand         []byte     `json:"original_command"`
	OriginalDigest          string     `json:"original_digest"`
	CanonicalDigest         string     `json:"canonical_digest"`
	ExpectedDesiredRevision uint64     `json:"expected_desired_revision"`
	NewTarget               *Identity  `json:"new_target,omitempty"`
	ReleaseID               id.ID      `json:"release_id,omitzero"`
	RollbackReleaseID       id.ID      `json:"rollback_release_id,omitzero"`
	OccurredAt              time.Time  `json:"occurred_at"`
}

// ActionReceipt is immutable local acceptance, separate from external completion.
type ActionReceipt struct {
	ID              id.ID     `json:"id"`
	Key             Key       `json:"key"`
	Target          Key       `json:"target"`
	CanonicalDigest string    `json:"canonical_digest"`
	OriginalDigest  string    `json:"original_digest"`
	Revision        uint64    `json:"revision"`
	DesiredRevision uint64    `json:"desired_revision"`
	AuditID         id.ID     `json:"audit_id"`
	ReleaseID       id.ID     `json:"release_id,omitzero"`
	AcceptedAt      time.Time `json:"accepted_at"`
}

// AccessAuditRequest freezes one server-issued disclosure or refusal attempt.
// SelectorDigest is a sanitized fingerprint, never row contents or credentials.
type AccessAuditRequest struct {
	AttemptID      id.ID      `json:"attempt_id"`
	Installation   string     `json:"installation"`
	Authority      string     `json:"authority"`
	ApplicationID  string     `json:"application_id"`
	OrganizationID string     `json:"organization_id"`
	Actor          AuditActor `json:"actor"`
	Scope          AuditScope `json:"scope"`
	Action         string     `json:"action"`
	ResourceKind   string     `json:"resource_kind"`
	ResourceID     string     `json:"resource_id"`
	SelectorDigest string     `json:"selector_digest,omitempty"`
	Outcome        string     `json:"outcome"`
	Reason         string     `json:"reason"`
	OccurredAt     time.Time  `json:"occurred_at"`
	DecisionID     string     `json:"decision_id,omitempty"`
	CorrelationID  string     `json:"correlation_id,omitempty"`
}

// AccessAuditReceipt proves local access intent persistence without granting access.
type AccessAuditReceipt struct {
	ID         id.ID     `json:"id"`
	Digest     string    `json:"digest"`
	AuditID    id.ID     `json:"audit_id"`
	AcceptedAt time.Time `json:"accepted_at"`
}

// AuditIntent is the frozen source content delivered by the independent publisher.
// MappingVersion and source identity cannot change during sink receipt recovery.
type AuditIntent struct {
	ID             id.ID      `json:"id"`
	MappingVersion Decimal    `json:"mapping_version"`
	SourceKey      string     `json:"source_key"`
	Installation   string     `json:"installation"`
	Authority      string     `json:"authority"`
	ApplicationID  string     `json:"application_id"`
	OrganizationID string     `json:"organization_id"`
	Actor          AuditActor `json:"actor"`
	Scope          AuditScope `json:"scope"`
	Action         string     `json:"action"`
	ResourceKind   string     `json:"resource_kind"`
	ResourceID     string     `json:"resource_id"`
	Outcome        string     `json:"outcome"`
	Reason         string     `json:"reason"`
	SelectorDigest string     `json:"selector_digest,omitempty"`
	DecisionID     string     `json:"decision_id,omitempty"`
	CorrelationID  string     `json:"correlation_id,omitempty"`
	SourceDigest   string     `json:"source_digest"`
	OccurredAt     time.Time  `json:"occurred_at"`
	AcceptedAt     time.Time  `json:"accepted_at"`
}

// AuditSinkReceipt preserves the complete sink binding and normalized fingerprint.
type AuditSinkReceipt struct {
	IntentID              id.ID     `json:"intent_id"`
	IntentDigest          string    `json:"intent_digest"`
	MappingVersion        Decimal   `json:"mapping_version"`
	SourceKey             string    `json:"source_key"`
	SourceDigest          string    `json:"source_digest"`
	Installation          string    `json:"installation"`
	Authority             string    `json:"authority"`
	ApplicationID         string    `json:"application_id"`
	OrganizationID        string    `json:"organization_id"`
	NormalizedFingerprint string    `json:"normalized_fingerprint"`
	SinkReceiptID         string    `json:"sink_receipt_id"`
	Receipt               []byte    `json:"receipt"`
	AcceptedAt            time.Time `json:"accepted_at"`
}

// ActionDigest binds parsed acceptance, original bytes and all reserved identities.
// CanonicalDigest itself is excluded to avoid a self-referential digest.
func ActionDigest(q ActionSubmission) (string, error) {
	q.CanonicalDigest = ""

	raw, err := json.Marshal(q)
	if err != nil {
		return "", err
	}

	return Digest(raw), nil
}

// Controller storage bounds leave retained completion records outside A's target document.
const (
	MaxControllerRecord = 256 << 10
	MaxAuditIntent      = 32 << 10
	MaxProviderStep     = 16 << 10
	MaxContinuation     = 32 << 10
	MaxControllerPage   = 64
	MaxControllerCursor = 1024
	MaxProviderSteps    = 3*MaxMembers + 2
)

// StartupBindingRef pins a non-secret trusted configuration descriptor generation.
// It never contains an endpoint, credential, DSN or a digest of secret bytes.
type StartupBindingRef struct {
	BindingID         string  `json:"binding_id"`
	BindingGeneration Decimal `json:"binding_generation"`
	BindingDigest     string  `json:"binding_digest"`
}

// ProvisionService freezes the public portion of a single provider service.
// StartupBindingRef supplies private configuration through a later trusted resolver.
type ProvisionService struct {
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	Role      string            `json:"role"`
	CPUMillis Decimal           `json:"cpu_millis"`
	MemoryMiB Decimal           `json:"memory_mib"`
	PIDLimit  Decimal           `json:"pid_limit"`
	Startup   StartupBindingRef `json:"startup"`
}

// ProvisionSpec is the closed first profile with no caller env or additional effects.
type ProvisionSpec struct {
	Version  Decimal            `json:"version"`
	Services []ProvisionService `json:"services"`
}

// ParseProvisionSpec validates syntax and safe conversion, never host capabilities.
func ParseProvisionSpec(m Manifest) (ProvisionSpec, error) {
	var out ProvisionSpec

	spec, err := ParseManagedSpec(m)
	if err != nil {
		return out, err
	}

	if err = strictObject(spec.Provision, &out); err != nil {
		return out, err
	}

	if out.Version != "1" || len(out.Services) == 0 || len(out.Services) > MaxMembers {
		return ProvisionSpec{}, ErrInvalid
	}

	services := map[string]bool{}
	main := 0

	for _, s := range out.Services {
		if !plainToken(s.Name) || s.Name == "network" || services[s.Name] || len(s.Image) > 256 {
			return ProvisionSpec{}, ErrInvalid
		}

		parts := strings.Split(s.Image, "@sha256:")
		if len(parts) != 2 || !imageName(parts[0]) || !hexDigest(parts[1]) {
			return ProvisionSpec{}, ErrInvalid
		}

		if s.Role == "main" {
			main++
		} else if s.Role != "sidecar" {
			return ProvisionSpec{}, ErrInvalid
		}

		cpu, e1 := s.CPUMillis.Uint64()
		memory, e2 := s.MemoryMiB.Uint64()
		pid, e3 := s.PIDLimit.Uint64()

		generation, e4 := s.Startup.BindingGeneration.Uint64()
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || cpu == 0 || memory == 0 || pid == 0 || generation == 0 || generation > math.MaxInt64 || cpu > math.MaxInt64/1000000 || memory > math.MaxInt64/1048576 || pid > math.MaxInt64 || cpu > uint64(^uint(0)>>1) || memory > uint64(^uint(0)>>1) || !plainToken(s.Startup.BindingID) || !hexDigest(s.Startup.BindingDigest) {
			return ProvisionSpec{}, ErrInvalid
		}

		services[s.Name] = true
	}

	if main != 1 {
		return ProvisionSpec{}, ErrInvalid
	}

	for _, member := range m.Members {
		if !services[member.Service] {
			return ProvisionSpec{}, ErrInvalid
		}
	}

	return out, nil
}

func plainToken(s string) bool {
	if len(s) == 0 || len(s) > 256 {
		return false
	}

	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && !strings.ContainsRune("-_.", c) {
			return false
		}
	}

	return true
}
func imageName(s string) bool {
	if s == "" {
		return false
	}

	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && !strings.ContainsRune("-_./:", c) {
			return false
		}
	}

	return true
}
func hexDigest(s string) bool {
	if len(s) != 64 {
		return false
	}

	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}
