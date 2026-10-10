package managed

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/xraph/ctrlplane/id"
)

// RefusalError identifies the managed operation required before a generic mutation.
type RefusalError struct {
	Action    string
	Operation string
	Cause     error
}

func (r *RefusalError) Error() string {
	return fmt.Sprintf("%s refused: use managed %s: %v", r.Action, r.Operation, r.Cause)
}

// Unwrap preserves the protection failure for callers.
func (r *RefusalError) Unwrap() error { return r.Cause }

// Guard reads protection from the same object used for ordinary persistence.
type Guard struct {
	protection ProtectionStore
	owner      OwnerProtectionStore
	authority  any
	err        error
}

// NewGuard installs protection even when the authority cannot supply the capability.
func NewGuard(authority any) *Guard {
	p, err := RequireProtection(authority)
	owner, _ := authority.(OwnerProtectionStore)

	return &Guard{protection: p, owner: owner, authority: authority, err: err}
}

func refusal(action string, cause error) error {
	operation := "replacement"

	switch action {
	case "instances.delete", "workloads.delete", "tenants.delete":
		operation = "retire"
	case "instances.stop", "instances.suspend", "workloads.pause":
		operation = "pause"
	case "deployments.rollback":
		operation = "rollback"
	}

	return &RefusalError{Action: action, Operation: operation, Cause: cause}
}

// CheckInstance refuses generic writes to every retained managed identity.
func (g *Guard) CheckInstance(ctx context.Context, key Key, action string) error {
	if strings.TrimSpace(key.TenantID) == "" || key.InstanceID.Prefix() != id.PrefixInstance {
		return refusal(action, ErrInvalid)
	}

	if g == nil {
		return refusal(action, ErrUnsupported)
	}

	if g.err != nil {
		return refusal(action, g.err)
	}

	p, err := g.protection.InspectManagedProtection(ctx, key)
	if err != nil {
		return refusal(action, fmt.Errorf("%w: %w", ErrUnavailable, err))
	}

	if p.Protected {
		return refusal(action, ErrBlocked)
	}

	if p.Phase != "" || p.Revision != 0 {
		return refusal(action, ErrAuthority)
	}

	return nil
}

// CheckOwner protects retained manifest ownership after mutable rows disappear.
func (g *Guard) CheckOwner(ctx context.Context, key OwnerKey, action string) error {
	if err := key.Validate(); err != nil {
		return refusal(action, err)
	}

	if g == nil || g.owner == nil {
		return refusal(action, ErrUnsupported)
	}

	if g.err != nil {
		return refusal(action, g.err)
	}

	p, err := g.owner.InspectManagedOwnerProtection(ctx, key)
	if err != nil {
		return refusal(action, fmt.Errorf("%w: %w", ErrUnavailable, err))
	}

	if p.Protected {
		return refusal(action, ErrBlocked)
	}

	if p.Phase != "" || p.Revision != 0 {
		return refusal(action, ErrAuthority)
	}

	return nil
}

// CheckTenant refuses removal while the authority retains managed ownership.
func (g *Guard) CheckTenant(ctx context.Context, tenantID string) error {
	const action = "tenants.delete"
	if g == nil {
		return refusal(action, ErrUnsupported)
	}

	if g.err != nil {
		return refusal(action, g.err)
	}

	protected, err := g.protection.HasManagedObligations(ctx, tenantID)
	if err != nil {
		return refusal(action, fmt.Errorf("%w: %w", ErrUnavailable, err))
	}

	if protected {
		return refusal(action, ErrBlocked)
	}

	reader, ok := g.authority.(interface {
		ListManagedTargets(ctx context.Context, request PageRequest) (Page, error)
	})
	if !ok {
		return refusal(action, ErrUnsupported)
	}

	page, err := reader.ListManagedTargets(ctx, PageRequest{TenantID: tenantID, Limit: 1})
	if err != nil {
		return refusal(action, fmt.Errorf("%w: %w", ErrUnavailable, err))
	}

	if len(page.Items) != 0 || page.NextCursor != "" {
		return refusal(action, ErrBlocked)
	}

	return nil
}

// UsesAuthority proves identity of the actual persistence object without exposing it.
// Value-shaped, nil and otherwise indeterminate authorities cannot prove identity.
func (g *Guard) UsesAuthority(candidate any) bool {
	if g == nil || g.err != nil || candidate == nil || g.authority == nil {
		return false
	}

	actual, wanted := reflect.ValueOf(g.authority), reflect.ValueOf(candidate)

	return actual.Kind() == reflect.Pointer && wanted.Kind() == reflect.Pointer && !actual.IsNil() && !wanted.IsNil() && actual.Type() == wanted.Type() && actual.Pointer() == wanted.Pointer()
}
