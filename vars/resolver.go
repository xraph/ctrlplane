package vars

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/xraph/ctrlplane/provider"
)

// Resolver turns variable definitions and caller-supplied values into a
// resolved Scope plus the secret bindings the provider must materialize.
type Resolver struct{}

// NewResolver returns a Resolver.
func NewResolver() *Resolver {
	return &Resolver{}
}

// Resolve validates definitions, applies values and defaults, evaluates
// computed expressions, and collects secret bindings. derived seeds the
// instance, tenant, and region context. The returned Scope's Var map holds
// every plain and computed variable; secret variables are excluded from Var
// and returned as bindings.
func (r *Resolver) Resolve(
	_ context.Context,
	defs []Definition,
	values map[string]any,
	derived Scope,
) (Scope, []provider.SecretBinding, error) {
	scope := Scope{
		Var:      make(map[string]any, len(defs)),
		Instance: derived.Instance,
		Tenant:   derived.Tenant,
		Region:   derived.Region,
	}

	var (
		bindings []provider.SecretBinding
		computed []Definition
	)

	seen := make(map[string]struct{}, len(defs))

	for _, def := range defs {
		if err := validateDefinition(def); err != nil {
			return Scope{}, nil, err
		}

		if _, dup := seen[def.Name]; dup {
			return Scope{}, nil, fmt.Errorf("%w: duplicate variable %q", ErrInvalidDefinition, def.Name)
		}

		seen[def.Name] = struct{}{}

		switch {
		case def.Type == TypeSecret:
			bindings = append(bindings, provider.SecretBinding{
				VarName: def.Name,
				EnvKey:  secretEnvKey(def),
				Ref:     *def.Secret,
			})
		case def.Expression != "":
			computed = append(computed, def)
		default:
			val, err := resolvePlain(def, values[def.Name])
			if err != nil {
				return Scope{}, nil, err
			}

			if val != nil {
				scope.Var[def.Name] = val
			}
		}
	}

	if err := resolveComputed(scope, computed); err != nil {
		return Scope{}, nil, err
	}

	return scope, bindings, nil
}

// validateDefinition enforces structural invariants independent of values.
func validateDefinition(def Definition) error {
	if strings.TrimSpace(def.Name) == "" {
		return fmt.Errorf("%w: empty name", ErrInvalidDefinition)
	}

	if def.Default != nil && def.Expression != "" {
		return fmt.Errorf("%w: %q sets both default and expression", ErrInvalidDefinition, def.Name)
	}

	switch def.Type {
	case TypeString, TypeInt, TypeBool:
	case TypeEnum:
		if len(def.Enum) == 0 {
			return fmt.Errorf("%w: enum variable %q has no members", ErrInvalidDefinition, def.Name)
		}
	case TypeSecret:
		if def.Secret == nil {
			return fmt.Errorf("%w: secret variable %q has no source", ErrInvalidDefinition, def.Name)
		}

		if def.Expression != "" {
			return fmt.Errorf("%w: secret variable %q cannot be computed", ErrInvalidDefinition, def.Name)
		}
	default:
		return fmt.Errorf("%w: %q has unknown type %q", ErrInvalidDefinition, def.Name, def.Type)
	}

	if def.Pattern != "" {
		if _, err := regexp.Compile(def.Pattern); err != nil {
			return fmt.Errorf("%w: %q has invalid pattern %q: %w", ErrInvalidDefinition, def.Name, def.Pattern, err)
		}
	}

	if def.Expression != "" {
		if _, err := template.New(def.Name).Parse(def.Expression); err != nil {
			return fmt.Errorf("%w: %q has invalid expression: %w", ErrInvalidDefinition, def.Name, err)
		}
	}

	return nil
}

// resolvePlain applies a value or default and validates by type. A nil
// return with a nil error means the variable is optional and unset, so the
// caller omits it from the scope.
func resolvePlain(def Definition, raw any) (any, error) {
	if raw == nil {
		raw = def.Default
	}

	if raw == nil {
		if def.Required {
			return nil, fmt.Errorf("%w: %q", ErrMissingRequired, def.Name)
		}

		return nil, nil //nolint:nilnil // (nil, nil) intentionally signals "optional unset"
	}

	switch def.Type {
	case TypeString, TypeEnum:
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %q expects string, got %T", ErrInvalidValue, def.Name, raw)
		}

		if def.Type == TypeEnum && !slices.Contains(def.Enum, s) {
			return nil, fmt.Errorf("%w: %q=%q not in enum %v", ErrInvalidValue, def.Name, s, def.Enum)
		}

		if err := matchPattern(def, s); err != nil {
			return nil, err
		}

		return s, nil
	case TypeInt:
		n, err := toInt(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %w", ErrInvalidValue, def.Name, err)
		}

		return n, nil
	case TypeBool:
		b, err := toBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %w", ErrInvalidValue, def.Name, err)
		}

		return b, nil
	default:
		return nil, fmt.Errorf("%w: %q has unsupported type %q", ErrInvalidValue, def.Name, def.Type)
	}
}

// matchPattern validates s against def.Pattern when one is set.
func matchPattern(def Definition, s string) error {
	if def.Pattern == "" {
		return nil
	}

	re, err := regexp.Compile(def.Pattern)
	if err != nil {
		return fmt.Errorf("%w: %q pattern %q: %w", ErrInvalidValue, def.Name, def.Pattern, err)
	}

	if !re.MatchString(s) {
		return fmt.Errorf("%w: %q=%q does not match pattern %q", ErrInvalidValue, def.Name, s, def.Pattern)
	}

	return nil
}

// toInt coerces a JSON-decoded or native value to an int.
func toInt(raw any) (int, error) {
	switch v := raw.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64: // JSON numbers decode to float64
		return int(v), nil
	case string:
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("not an integer: %q", v)
		}

		return n, nil
	default:
		return 0, fmt.Errorf("expected integer, got %T", raw)
	}
}

// toBool coerces a native or string value to a bool.
func toBool(raw any) (bool, error) {
	switch v := raw.(type) {
	case bool:
		return v, nil
	case string:
		b, err := strconv.ParseBool(v)
		if err != nil {
			return false, fmt.Errorf("not a boolean: %q", v)
		}

		return b, nil
	default:
		return false, fmt.Errorf("expected boolean, got %T", raw)
	}
}

// secretEnvKey returns the env-var name a secret binding is exposed as,
// defaulting to UPPER_SNAKE of the variable name.
func secretEnvKey(def Definition) string {
	return strings.ToUpper(strings.ReplaceAll(def.Name, "-", "_"))
}

// resolveComputed evaluates computed expressions via an iterative fixpoint.
// Each round evaluates every still-unresolved expression against the current
// scope; an expression referencing a not-yet-resolved variable fails
// (missingkey=error) and is retried next round. A round that makes no
// progress while expressions remain indicates a cycle or undefined reference.
func resolveComputed(scope Scope, computed []Definition) error {
	remaining := slices.Clone(computed)

	for len(remaining) > 0 {
		progressed := false

		var next []Definition

		for _, def := range remaining {
			val, err := evalExpression(def.Expression, scope)
			if err != nil {
				next = append(next, def)

				continue
			}

			scope.Var[def.Name] = val
			progressed = true
		}

		if !progressed {
			names := make([]string, 0, len(next))
			for _, d := range next {
				names = append(names, d.Name)
			}

			return fmt.Errorf("%w: %v", ErrCycle, names)
		}

		remaining = next
	}

	return nil
}

// evalExpression renders a computed expression against the scope. A missing
// key (unresolved reference) is an error so the fixpoint can retry.
func evalExpression(expr string, scope Scope) (string, error) {
	tmpl, err := template.New("expr").Option("missingkey=error").Parse(expr)
	if err != nil {
		return "", fmt.Errorf("parse expression: %w", err)
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, scope.root()); err != nil {
		return "", fmt.Errorf("eval expression: %w", err)
	}

	return sb.String(), nil
}
