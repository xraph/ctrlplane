package contract

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	dash "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"
	"github.com/xraph/forge/extensions/dashboard/contract/loader"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/id"
)

//go:embed manifest.yaml
var manifestYAML []byte

// Deps resolves initialized services without coupling registration to startup.
type Deps struct {
	ControlPlane func() *app.CtrlPlane
	Ready        func() bool
	Logger       *slog.Logger
}

// Register binds an initialized standalone control plane to the dashboard.
func Register(d *dispatcher.Dispatcher, reg dash.Registry, wreg dash.WardenRegistry, cp *app.CtrlPlane) error {
	if cp == nil {
		return errors.New("ctrlplane contract: control plane is required")
	}

	return RegisterWithResolver(d, reg, wreg, Deps{ControlPlane: func() *app.CtrlPlane { return cp }})
}

// RegisterWithResolver validates bindings before publishing a lazy contributor.
func RegisterWithResolver(d *dispatcher.Dispatcher, reg dash.Registry, wreg dash.WardenRegistry, deps Deps) error {
	if deps.ControlPlane == nil {
		return errors.New("ctrlplane contract: resolver is required")
	}

	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}

	if err := wreg.Register("ctrlplane.auth", authorizer{}); err != nil {
		return fmt.Errorf("register ctrlplane authorizer: %w", err)
	}

	manifest, err := loader.Load(bytes.NewReader(manifestYAML), "ctrlplane/extension/contract/manifest.yaml")
	if err != nil {
		return fmt.Errorf("load ctrlplane manifest: %w", err)
	}

	if err := loader.Validate(manifest, wreg); err != nil {
		return fmt.Errorf("validate ctrlplane manifest: %w", err)
	}

	b := &bindings{deps: deps, handlers: make(map[string]binding), invalidates: make(map[string][]string)}
	registerQueries(b)
	registerCommands(b)

	if b.err != nil {
		return b.err
	}

	if err := validateBindings(manifest.Intents, b); err != nil {
		return err
	}

	if err := reg.Register(manifest); err != nil {
		return fmt.Errorf("register ctrlplane manifest: %w", err)
	}

	for _, in := range manifest.Intents {
		b.invalidates[in.Name] = append([]string(nil), in.Invalidates...)
		if err := d.Register("ctrlplane", in.Name, 1, b.handlers[in.Name].handler); err != nil {
			return fmt.Errorf("bind ctrlplane intent %s: %w", in.Name, err)
		}
	}

	return nil
}

func validateBindings(intents []dash.Intent, b *bindings) error {
	if len(intents) != len(b.handlers) {
		return errors.New("ctrlplane contract: manifest and bindings differ")
	}

	names := make(map[string]bool)

	for _, in := range intents {
		if names[in.Name] {
			return fmt.Errorf("ctrlplane contract: duplicate intent %s", in.Name)
		}

		names[in.Name] = true

		bound, ok := b.handlers[in.Name]
		if !ok || dash.IntentKind(bound.kind) != in.Kind || in.Version != 1 {
			return fmt.Errorf("ctrlplane contract: invalid binding %s", in.Name)
		}

		seen := make(map[string]bool)

		for _, target := range in.Invalidates {
			dependency, ok := b.handlers[target]
			if !ok || dependency.kind != dash.KindQuery || seen[target] {
				return fmt.Errorf("ctrlplane contract: invalid dependency %s for %s", target, in.Name)
			}

			seen[target] = true
		}
	}

	return nil
}

type binding struct {
	kind    dash.Kind
	handler dispatcher.Handler
}
type bindings struct {
	deps        Deps
	handlers    map[string]binding
	invalidates map[string][]string
	err         error
}

func query[I any](b *bindings, name string, fn func(context.Context, *app.CtrlPlane, I) (any, error)) {
	bind(b, name, dash.KindQuery, wire(b, name, dash.KindQuery, fn))
}
func command[I any](b *bindings, name string, fn func(context.Context, *app.CtrlPlane, I) (any, error)) {
	bind(b, name, dash.KindCommand, wire(b, name, dash.KindCommand, fn))
}
func bind(b *bindings, name string, kind dash.Kind, handler dispatcher.Handler) {
	if _, ok := b.handlers[name]; ok {
		b.err = fmt.Errorf("ctrlplane contract: duplicate binding %s", name)

		return
	}

	b.handlers[name] = binding{kind: kind, handler: handler}
}
func (deps Deps) available(cp *app.CtrlPlane) bool {
	return (deps.Ready == nil || deps.Ready()) && cp != nil && cp.Instances != nil && cp.Workloads != nil && cp.Templates != nil && cp.Admin != nil
}
func (deps Deps) resolve() (*app.CtrlPlane, error) {
	cp := deps.ControlPlane()
	if !deps.available(cp) {
		return nil, unavailable("Ctrlplane is not ready.")
	}

	return cp, nil
}

func wire[I any](b *bindings, name string, kind dash.Kind, fn func(context.Context, *app.CtrlPlane, I) (any, error)) dispatcher.Handler {
	return func(ctx context.Context, payload json.RawMessage, params map[string]any, p dash.Principal) (*dispatcher.Result, error) {
		ctx, err := principalContext(ctx, p)
		if err != nil {
			return nil, err
		}

		cp, err := b.deps.resolve()
		if err != nil {
			return nil, err
		}

		decision, err := (authorizer{}).Authorize(ctx, p, dash.Action{Intent: name, Kind: kind})
		if err != nil {
			return nil, unavailable("Authorization provider unavailable.")
		}

		if !decision.Allow {
			return nil, &dash.Error{Code: dash.CodePermissionDenied, Message: decision.Reason}
		}

		claims := auth.ClaimsFrom(ctx)

		allowed, policyErr := cp.Auth().Authorize(ctx, auth.AuthzRequest{TenantID: claims.TenantID, SubjectID: claims.SubjectID, Resource: "ctrlplane", Action: name})
		if policyErr != nil {
			b.deps.Logger.ErrorContext(ctx, "ctrlplane dashboard authorization failed", "intent", name, "error", policyErr)

			return nil, unavailable("Authorization provider unavailable.")
		}

		if !allowed {
			return nil, &dash.Error{Code: dash.CodePermissionDenied, Message: "Ctrlplane authorization policy denied this operation."}
		}

		var in I

		if len(params) > 0 {
			data, err := json.Marshal(params)
			if err != nil {
				return nil, badRequest("Invalid query parameters.")
			}

			if err := json.Unmarshal(data, &in); err != nil {
				return nil, badRequest("Invalid query parameters.")
			}
		}

		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &in); err != nil {
				return nil, badRequest("Invalid request fields.")
			}
		}

		out, err := fn(ctx, cp, in)
		if err != nil {
			return nil, b.failure(ctx, name, err)
		}

		out, err = projectResponse(out)
		if err != nil {
			return nil, b.failure(ctx, name, err)
		}

		data, err := json.Marshal(out)
		if err != nil {
			return nil, b.failure(ctx, name, err)
		}

		result := &dispatcher.Result{Data: data, ExtraInvalidates: append([]string(nil), b.invalidates[name]...)}

		return result, nil
	}
}

func (b *bindings) failure(ctx context.Context, intent string, err error) error {
	b.deps.Logger.ErrorContext(ctx, "ctrlplane dashboard operation failed", "intent", intent, "error", err)

	return contractError(err)
}
func contractError(err error) error {
	var ce *dash.Error
	if errors.As(err, &ce) {
		return ce
	}

	code, message := dash.CodeInternal, "Ctrlplane could not complete the operation."

	switch {
	case errors.Is(err, ctrlplane.ErrInvalidConfig), errors.Is(err, ctrlplane.ErrInvalidSource), errors.Is(err, ctrlplane.ErrUnsupportedSource):
		code, message = dash.CodeBadRequest, "The resource configuration is invalid or unsupported."
	case errors.Is(err, ctrlplane.ErrProviderUnavail), errors.Is(err, ctrlplane.ErrNotImplemented), errors.Is(err, ctrlplane.ErrDatacenterUnavailable):
		code, message = dash.CodeUnavailable, "The requested service is unavailable."
	case errors.Is(err, ctrlplane.ErrNotFound), errors.Is(err, ctrlplane.ErrProviderNotFound):
		code, message = dash.CodeNotFound, "The requested resource was not found."
	case errors.Is(err, ctrlplane.ErrForbidden), errors.Is(err, ctrlplane.ErrUnauthorized), errors.Is(err, auth.ErrUnauthorized):
		code, message = dash.CodePermissionDenied, "You do not have permission for this operation."
	case errors.Is(err, ctrlplane.ErrInvalidState), errors.Is(err, ctrlplane.ErrAlreadyExists), errors.Is(err, ctrlplane.ErrQuotaExceeded):
		code, message = dash.CodeConflict, "The operation conflicts with the resource state or quota."
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		code, message = dash.CodeUnavailable, "The operation was interrupted. Retry when the service is available."
	}

	return &dash.Error{Code: code, Message: message, Retryable: code == dash.CodeUnavailable}
}

func parseID(value string, prefix id.Prefix) (id.ID, error) {
	parsed, err := id.ParseWithPrefix(value, prefix)
	if err != nil {
		return id.Nil, &dash.Error{Code: dash.CodeBadRequest, Message: "A valid " + string(prefix) + " identifier is required."}
	}

	return parsed, nil
}

func limit(value int) int {
	if value <= 0 {
		return 50
	}

	return min(value, 200)
}

type entityInput struct {
	ID string `json:"id"`
}
type targetInput struct {
	InstanceID string `json:"instance_id"`
	WorkloadID string `json:"workload_id"`
	Cursor     string `json:"cursor"`
	Limit      int    `json:"limit"`
}
type updateInput[T any] struct {
	ID      string `json:"id"`
	Request T      `json:"request"`
}
type namedInput struct {
	Name string `json:"name"`
}
type page struct {
	Items      any    `json:"items"`
	Total      int    `json:"total"`
	Complete   bool   `json:"complete"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func ack(err error) (any, error) {
	return ackDTO{OK: err == nil}, err
}

func requireTenant(ctx context.Context) error {
	if auth.ClaimsFrom(ctx).TenantID == "" {
		return &dash.Error{Code: dash.CodePermissionDenied, Message: "Select a tenant through your authenticated session before creating resources."}
	}

	return nil
}

func ownedInstance(ctx context.Context, cp *app.CtrlPlane, value string) (id.ID, error) {
	target, err := parseID(value, id.PrefixInstance)
	if err != nil {
		return target, err
	}

	_, err = cp.Instances.Get(ctx, target)

	return target, err
}
