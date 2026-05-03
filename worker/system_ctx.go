package worker

import (
	"context"

	"github.com/xraph/ctrlplane/auth"
)

// systemSubject is the synthetic SubjectID workers stamp on the
// claims they synthesize when calling a Service from a background
// goroutine. Audit log entries authored by GC sweeps / reconciler
// cycles will carry this subject so operators can distinguish
// system-driven actions from user-driven ones.
const systemSubject = "system:worker"

// withSystemClaims returns a context carrying claims sufficient to
// pass auth.RequireClaims gates from background workers. The claims
// are scoped to a single tenant and carry the system:admin role so
// downstream services don't reject the call on RBAC grounds.
//
// Workers cannot rely on user-supplied claims: their context originates
// from the scheduler goroutine and has no incoming request to pull
// auth from. Synthesizing claims is the standard escape hatch — the
// shape mirrors auth.NoopProvider's defaults.
func withSystemClaims(ctx context.Context, tenantID string) context.Context {
	return auth.WithClaims(ctx, &auth.Claims{
		SubjectID: systemSubject,
		TenantID:  tenantID,
		Roles:     []string{"system:admin"},
	})
}
