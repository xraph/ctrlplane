# CLAUDE.md — Ctrl Plane

> Composable control plane library for deploying and managing SaaS instances at scale.

**Module:** `github.com/xraph/ctrlplane` (Go 1.25.7)
**Foundation:** `github.com/xraph/forge`
**Status:** Active development — architecture docs in `_project_files/` are the source of truth.

---

## Build & Lint

```bash
go build ./...
go test ./...
golangci-lint run ./...
goimports -w -local github.com/xraph/ctrlplane .
```

The `.golangci.yml` at the project root is authoritative (v2 format, `default: none`, explicit enable list). It mirrors the Forge project's configuration.

---

## Project Layout

```
ctrlplane.go, config.go, errors.go, options.go, doc.go   # Root package
id/            # TypeID wrapper (prefix-qualified, UUIDv7-based identifiers)
auth/          # Auth abstraction + adapters (authsome/, forgeauth/)
instance/      # Instance lifecycle (entity, service, store, state machine)
deploy/        # Deployments & releases (strategies: rolling, blue-green, canary)
health/        # Health checks (http, tcp, grpc, command checkers)
telemetry/     # Metrics, logs, traces, resource snapshots
provider/      # Cloud provider abstraction + implementations
  kubernetes/, nomad/, docker/, aws/, gcp/, azure/, fly/
network/       # Domains, routes, TLS certificates
secrets/       # Secret management + vault interface
admin/         # Tenant management, quotas, audit
event/         # Event bus (inmemory, nats, redis) + webhooks
worker/        # Background workers (reconciler, health runner, GC, cert renewer)
store/         # Aggregate store interface + implementations
  postgres/, sqlite/, memory/
api/           # HTTP handlers + middleware
extension/     # Forge extension adapter
cmd/           # Reference binary
```

---

## Critical Coding Conventions

### Identity — TypeID Only

All entity IDs use `id.ID` (which wraps `typeid.TypeID` from `go.jetify.com/typeid/v2`). IDs are prefix-qualified, globally unique, sortable, and URL-safe in the format `prefix_suffix` (e.g., `inst_01h455vb4pex5vsknk084sn02q`). Never use `uuid.UUID` or `string` for entity identifiers.

```go
import "github.com/xraph/ctrlplane/id"

entity := Entity{ID: id.New(id.PrefixInstance)}
parsed, err := id.Parse(s)
parsedWithPrefix, err := id.ParseWithPrefix(s, id.PrefixInstance)
```

Each entity type has a dedicated prefix constant (`id.PrefixInstance`, `id.PrefixDeployment`, etc.). The `id.New()` function requires a prefix argument.

### Base Entity Embedding

Every domain entity embeds `ctrlplane.Entity`:

```go
type Instance struct {
    ctrlplane.Entity  // ID, CreatedAt, UpdatedAt

    TenantID string `json:"tenant_id" db:"tenant_id"`
    Name     string `json:"name" db:"name"`
}
```

Create entities with `ctrlplane.NewEntity(prefix)` which sets a prefix-qualified ID and timestamps (UTC).

### Struct Tags

Every exported struct field that is persisted or serialized must have tags:

```go
// Domain entity — json + db
Field string `json:"field_name" db:"field_name"`

// Config struct — json + env, optionally default
Field string `json:"field_name" env:"CP_FIELD_NAME" default:"value"`

// Request DTO — json, optionally validate
Field string `json:"field_name" validate:"required"`

// Internal/secret — suppress serialization
Value []byte `json:"-" db:"value"`
```

Use `snake_case` for json and db tags. Use `CP_` prefix for env tags.

### Option Pattern

The root `CtrlPlane` and extension use functional options:

```go
type Option func(*CtrlPlane) error

func WithStore(s store.Store) Option {
    return func(cp *CtrlPlane) error {
        cp.store = s
        return nil
    }
}
```

Options return `error`. Validate inside the option function.

### Constructor Pattern

Providers and implementations use variadic options with sane defaults:

```go
func New(opts ...Option) (*Provider, error) {
    p := &Provider{
        cfg: Config{
            Namespace: "default",  // sane default
            Region:    "local",
        },
    }
    for _, opt := range opts {
        if err := opt(p); err != nil {
            return nil, err
        }
    }
    // post-option validation and client initialization
    return p, nil
}
```

Each provider has an `options.go` with `type Option func(*Provider) error` and `With*` functions. A `WithConfig(cfg Config)` option bridges env/file-based configuration.

### Context-First

Every method that does I/O or may block takes `context.Context` as first parameter:

```go
func (s *service) Get(ctx context.Context, id id.ID) (*Instance, error)
```

### Service Interface Pattern

Each subsystem has a `Service` interface following CRUD + domain actions:

```go
type Service interface {
    Create(ctx context.Context, req CreateRequest) (*Entity, error)
    Get(ctx context.Context, id id.ID) (*Entity, error)
    List(ctx context.Context, opts ListOptions) (*ListResult, error)
    Update(ctx context.Context, id id.ID, req UpdateRequest) (*Entity, error)
    Delete(ctx context.Context, id id.ID) error
}
```

### Store Pattern

Store methods always accept `tenantID` for multi-tenant scoping:

```go
type Store interface {
    Insert(ctx context.Context, entity *Entity) error
    GetByID(ctx context.Context, tenantID string, id id.ID) (*Entity, error)
    List(ctx context.Context, tenantID string, opts ListOptions) (*ListResult, error)
    Update(ctx context.Context, entity *Entity) error
    Delete(ctx context.Context, tenantID string, id id.ID) error
}
```

### Registry Pattern

Thread-safe with `sync.RWMutex`. Use `defer` for unlocking, `RLock` for reads, `Lock` for writes:

```go
type Registry struct {
    mu        sync.RWMutex
    providers map[string]Provider
}
```

### State Machine Pattern

Valid transitions defined as maps. Always validate before updating state:

```go
var validTransitions = map[State][]State{
    StateProvisioning: {StateStarting, StateFailed, StateDestroying},
    StateStarting:     {StateRunning, StateFailed},
}
```

### Request/Response DTOs

- `CreateRequest`, `UpdateRequest`, `ScaleRequest`, `DeployRequest`
- `ListOptions` (cursor-based pagination), `ListResult` (items + next cursor + total)
- Pointer fields in update requests for partial updates: `Name *string`

---

## Error Handling

### Sentinel Errors

Defined in root `errors.go`. Always use as wrapping targets:

```go
var (
    ErrNotFound         = errors.New("ctrlplane: resource not found")
    ErrAlreadyExists    = errors.New("ctrlplane: resource already exists")
    ErrInvalidState     = errors.New("ctrlplane: invalid state transition")
    ErrProviderNotFound = errors.New("ctrlplane: provider not registered")
    ErrUnauthorized     = errors.New("ctrlplane: unauthorized")
    ErrForbidden        = errors.New("ctrlplane: forbidden")
    ErrQuotaExceeded    = errors.New("ctrlplane: quota exceeded")
)
```

### Wrapping Rules

Always wrap with `%w` and add context:

```go
// Correct
return fmt.Errorf("instance %s: %w", instanceID, ctrlplane.ErrNotFound)

// Wrong — loses error chain
return fmt.Errorf("failed: %v", err)
```

### Error Checking

Use `errors.Is` and `errors.As`. Never compare error strings.

---

## golangci-lint Compliance

### Enabled Linters — Key Rules

| Linter | What to Do |
|--------|-----------|
| **errcheck** | Handle every returned error. Use `_ =` only with justifying comment. |
| **errorlint** | Use `%w` in `fmt.Errorf`. Use `errors.Is`/`errors.As` not `==`. |
| **errname** | Error vars use `Err` prefix: `ErrNotFound`, not `NotFoundError`. |
| **errchkjson** | Check errors from `json.Marshal`/`json.Unmarshal`. |
| **govet** | Watch struct field alignment, printf format strings. |
| **staticcheck** | No deprecated APIs, proper context cancellation. |
| **bodyclose** | Always `defer resp.Body.Close()` after nil-error check. |
| **noctx** | Use `http.NewRequestWithContext`, never `http.Get`/`http.Post`. |
| **nilerr** | Never `return nil` inside `if err != nil`. |
| **nilnil** | Avoid returning `(nil, nil)` from functions with `(*T, error)` signature where confusing. |
| **ineffassign** | Remove dead assignments. |
| **unconvert** | Remove redundant type conversions. |
| **misspell** | Check comments and strings for typos. |
| **gocritic** | Prefer `strings.Contains` over `strings.Index >= 0`, etc. |
| **gosec** | No hardcoded creds, correct `crypto/rand` vs `math/rand`. |
| **fatcontext** | Never `ctx = context.WithValue(...)` in a loop. Build context outside. |
| **containedctx** | Do not store `context.Context` as a struct field. |
| **sqlclosecheck** | Always `defer rows.Close()`. |
| **makezero** | Use `make([]T, 0, cap)` then `append`, not `make([]T, n)` then `append`. |
| **godot** | End exported doc comments with a period. |
| **dupword** | No duplicate words in comments ("the the", "is is"). |
| **durationcheck** | Never multiply two `time.Duration` values together. |
| **perfsprint** | Use `strconv.Itoa` over `fmt.Sprintf("%d", n)` when simple. |
| **nlreturn** | Add blank line before `return` in multi-statement functions. |
| **wsl_v5** | Blank line after block close, before conditionals. |
| **gochecknoinits** | Do not use `func init()`. Use explicit constructors. |
| **nakedret** | Never use naked returns. Always return explicit values. |
| **decorder** | Keep type, const, var, func in consistent order. |
| **copyloopvar** | Loop variables are copied in Go 1.22+. No manual copies needed. |
| **intrange** | Use `for i := range n` instead of `for i := 0; i < n; i++`. |
| **modernize** | Use modern Go idioms (e.g., `slices` package, `any` over `interface{}`). |
| **usestdlibvars** | Use `http.StatusOK` not `200`, `http.MethodGet` not `"GET"`. |
| **usetesting** | Use `t.TempDir()` not `os.MkdirTemp`, `t.Setenv()` not `os.Setenv`. |
| **canonicalheader** | Use `http.CanonicalHeaderKey` for header names. |
| **sloglint** | Structured log attributes must be key-value pairs. |
| **tagalign** | Struct tags must be aligned within a struct. |
| **reassign** | Do not reassign package-level variables. |
| **spancheck** | Always `defer span.End()` after starting a trace span. |
| **rowserrcheck** | Always check `rows.Err()` after iterating. |
| **protogetter** | Use getter methods on protobuf messages, not direct field access. |
| **importas** | Use consistent import aliases. |
| **predeclared** | Do not shadow built-in identifiers (`len`, `cap`, `error`, etc.). |
| **bidichk** | No invisible Unicode bidirectional characters. |
| **asciicheck** | No non-ASCII identifiers. |

### Disabled Linters (do not worry about)

`exhaustive`, `exhaustruct`, `wrapcheck`, `ireturn`, `funlen`, `cyclop`, `gocognit`, `gocyclo`, `dupl`, `lll`, `mnd`, `prealloc`, `paralleltest`, `testpackage`, `revive`, `unused`, `unparam`, `err113`, `depguard`, `contextcheck`, `forcetypeassert`, `goconst`, `gochecknoglobals`, `godox`, `nestif`, `noinlineerr`, `nosprintfhostport`, `interfacebloat`, `iface`, `funcorder`, `maintidx`, `unqueryvet`, `testifylint`, `thelper`, `nonamedreturns`, `tagliatelle`, `varnamelen`

### Import Ordering

Three groups separated by blank lines. Use `goimports` with local prefix:

```go
import (
    // Standard library
    "context"
    "fmt"
    "time"

    // Third-party
    "go.jetify.com/typeid/v2"
    "github.com/uptrace/bun"
    "go.uber.org/zap"

    // Local modules
    "github.com/xraph/ctrlplane/id"
    "github.com/xraph/ctrlplane/provider"
)
```

### Formatting

The project uses `gofumpt` with `extra-rules: true`:
- No empty lines at start/end of function body.
- No empty lines around a lone statement in a block.
- Grouped `var`/`const` blocks for related declarations.

---

## Testing Conventions

- Tests in `*_test.go` in the same package (white-box testing).
- Use table-driven tests as the default pattern.
- Use `t.Helper()` in all test helpers.
- Use `t.Cleanup()` for teardown, not `defer`.
- Use `t.TempDir()` and `t.Setenv()` instead of `os.*` equivalents.
- In-memory store (`store/memory/`) is the default for unit tests.
- Prefer stdlib `testing` — use `testify` only if already in the dependency tree.

```go
func TestValidTransition(t *testing.T) {
    tests := []struct {
        name    string
        from    InstanceState
        to      InstanceState
        wantErr bool
    }{
        {"provisioning to starting", StateProvisioning, StateStarting, false},
        {"running to destroyed", StateRunning, StateDestroyed, true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := ValidateTransition(tt.from, tt.to)
            if (err != nil) != tt.wantErr {
                t.Errorf("ValidateTransition(%s, %s) error = %v, wantErr %v",
                    tt.from, tt.to, err, tt.wantErr)
            }
        })
    }
}
```

---

## Documentation

- Every package has a `doc.go` with a package-level comment.
- Every exported type, function, method, const, and var has a doc comment.
- Doc comments start with the symbol name and end with a period.

```go
// Instance represents a managed application instance belonging to a tenant.
type Instance struct { ... }

// ErrNotFound indicates the requested resource does not exist.
var ErrNotFound = errors.New("ctrlplane: resource not found")
```

---

## Common Pitfalls

1. **Using UUID instead of TypeID.** All IDs are `id.ID` (TypeID). `id.New()` requires a prefix constant. Never use raw UUIDs or strings for entity identifiers.
2. **Forgetting tenant scoping.** Every query must filter by `tenantID`. Store methods enforce this.
3. **Storing `context.Context` in structs.** Pass it as a function parameter. `containedctx` catches this.
4. **Using `http.Get`/`http.Post`.** Use `http.NewRequestWithContext` + `client.Do`. `noctx` enforces this.
5. **Forgetting `defer rows.Close()` or `defer resp.Body.Close()`.** `sqlclosecheck`/`bodyclose` catch these.
6. **Using `%v` instead of `%w`.** Always use `%w` to preserve the error chain.
7. **Using `func init()`.** Prohibited. Use explicit constructors and options.
8. **Naked returns.** Always specify return values explicitly.
9. **Missing error checks.** Every returned error must be checked.
10. **Multiplying two `time.Duration` values.** `time.Second * time.Second` is wrong.
11. **`make([]T, n)` then `append`.** Use `make([]T, 0, n)` then `append`.
12. **Missing blank line before `return`.** Required by `nlreturn` in multi-statement functions.
13. **Comments without terminal period.** Required by `godot` on exported symbols.
14. **`context.WithValue` in a loop.** Build context outside the loop. `fatcontext` flags this.

---

## Multi-Tenancy

Every entity has `TenantID string`. All store methods require it. Auth middleware extracts tenant from claims and places it in context via `auth.ClaimsFrom(ctx)`.

---

## Architecture Docs Reference

| Doc | Covers |
|-----|--------|
| `_project_files/00-ARCHITECTURE.md` | High-level design, module layout, usage modes |
| `_project_files/01-CORE-TYPES.md` | Entity, Config, Errors, Options, CtrlPlane struct |
| `_project_files/02-AUTH-ABSTRACTION.md` | Auth interface, claims, context helpers, adapters |
| `_project_files/03-PROVIDER-LAYER.md` | Provider interface, registry, capability system |
| `_project_files/04-INSTANCE-DEPLOY.md` | Instance lifecycle, state machine, deploy strategies |
| `_project_files/05-HEALTH-TELEMETRY.md` | Health checks, telemetry collection, TimescaleDB |
| `_project_files/06-NETWORK-SECRETS-ADMIN.md` | Domains, routes, TLS, secrets vault, admin/quotas |
| `_project_files/07-EVENTS-WORKERS.md` | Event bus, worker scheduler, webhooks |
| `_project_files/08-API-STORE.md` | HTTP routes, middleware, store implementations |
| `_project_files/09-FORGE-EXTENSION.md` | Forge extension adapter, OpenAPI, integration |

Always read the relevant architecture doc before implementing a subsystem.
