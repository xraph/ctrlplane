package contract

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

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

// Register binds the Ctrlplane manifest and handlers to the dashboard transport.
func Register(d *dispatcher.Dispatcher, reg dash.Registry, wreg dash.WardenRegistry, cp *app.CtrlPlane) error {
	if cp == nil {
		return errors.New("ctrlplane contract: control plane is required")
	}

	if err := wreg.Register("ctrlplane.auth", authorizer{cp: cp}); err != nil {
		return fmt.Errorf("register ctrlplane authorizer: %w", err)
	}

	manifest, err := loader.Load(bytes.NewReader(manifestYAML), "ctrlplane/extension/contract/manifest.yaml")
	if err != nil {
		return fmt.Errorf("load ctrlplane manifest: %w", err)
	}

	if err := loader.Validate(manifest, wreg); err != nil {
		return fmt.Errorf("validate ctrlplane manifest: %w", err)
	}

	if err := reg.Register(manifest); err != nil {
		return fmt.Errorf("register ctrlplane manifest: %w", err)
	}

	b := &bindings{d: d, cp: cp}

	for _, in := range manifest.Intents {
		if in.Kind == dash.IntentKindQuery {
			b.invalidates = append(b.invalidates, in.Name)
		}
	}

	registerQueries(b)
	registerCommands(b)

	return b.err
}

type bindings struct {
	d           *dispatcher.Dispatcher
	cp          *app.CtrlPlane
	err         error
	invalidates []string
}

func query[I any](b *bindings, name string, fn func(context.Context, I) (any, error)) {
	if b.err != nil {
		return
	}

	b.err = b.d.Register("ctrlplane", name, 1, wire(b, name, dash.KindQuery, fn))
}

func command[I any](b *bindings, name string, fn func(context.Context, I) (any, error)) {
	if b.err != nil {
		return
	}

	b.err = b.d.Register("ctrlplane", name, 1, wire(b, name, dash.KindCommand, fn))
}

func wire[I any](b *bindings, name string, kind dash.Kind, fn func(context.Context, I) (any, error)) dispatcher.Handler {
	return func(ctx context.Context, payload json.RawMessage, params map[string]any, p dash.Principal) (*dispatcher.Result, error) {
		ctx, err := principalContext(ctx, p)
		if err != nil {
			return nil, err
		}

		decision, err := (authorizer{cp: b.cp}).Authorize(ctx, p, dash.Action{Intent: name, Kind: kind})
		if err != nil {
			return nil, unavailable("Authorization provider unavailable.")
		}

		if !decision.Allow {
			return nil, &dash.Error{Code: dash.CodePermissionDenied, Message: decision.Reason}
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

		out, err := fn(ctx, in)
		if err != nil {
			return nil, contractError(err)
		}

		data, err := json.Marshal(out)
		if err != nil {
			return nil, contractError(err)
		}

		result := &dispatcher.Result{Data: data}
		if kind == dash.KindCommand {
			result.ExtraInvalidates = append([]string(nil), b.invalidates...)
		}

		return result, nil
	}
}

func contractError(err error) error {
	var ce *dash.Error
	if errors.As(err, &ce) {
		return err
	}

	code := dash.CodeInternal

	switch {
	case errors.Is(err, ctrlplane.ErrNotFound):
		code = dash.CodeNotFound
	case errors.Is(err, ctrlplane.ErrForbidden), errors.Is(err, ctrlplane.ErrUnauthorized), errors.Is(err, auth.ErrUnauthorized):
		code = dash.CodePermissionDenied
	case errors.Is(err, ctrlplane.ErrInvalidState), errors.Is(err, ctrlplane.ErrAlreadyExists):
		code = dash.CodeConflict
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		code = dash.CodeUnavailable
	}

	return &dash.Error{Code: code, Message: err.Error(), Retryable: code == dash.CodeUnavailable}
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
	Items    any  `json:"items"`
	Total    int  `json:"total"`
	Complete bool `json:"complete"`
}

func ack(err error) (any, error) {
	return struct {
		OK bool `json:"ok"`
	}{OK: err == nil}, err
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
