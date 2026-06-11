package vars

import (
	"context"
	"errors"
	"testing"

	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/secrets"
)

func derived() Scope {
	return Scope{
		Instance: InstanceContext{ID: "inst_1", Name: "web"},
		Tenant:   TenantContext{ID: "tnt_1"},
		Region:   "us-east",
	}
}

func TestResolve_PlainStringDefaultAndOverride(t *testing.T) {
	defs := []Definition{
		{Name: "image_tag", Type: TypeString, Default: "latest"},
		{Name: "log_level", Type: TypeString, Default: "info"},
	}

	scope, bindings, err := NewResolver().Resolve(context.Background(), defs,
		map[string]any{"log_level": "debug"}, derived())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(bindings) != 0 {
		t.Fatalf("expected no bindings, got %d", len(bindings))
	}

	if scope.Var["image_tag"] != "latest" {
		t.Errorf("image_tag = %v, want latest (default)", scope.Var["image_tag"])
	}

	if scope.Var["log_level"] != "debug" {
		t.Errorf("log_level = %v, want debug (override)", scope.Var["log_level"])
	}
}

func TestResolve_RequiredMissing(t *testing.T) {
	defs := []Definition{{Name: "host", Type: TypeString, Required: true}}

	_, _, err := NewResolver().Resolve(context.Background(), defs, nil, derived())
	if !errors.Is(err, ErrMissingRequired) {
		t.Fatalf("expected ErrMissingRequired, got %v", err)
	}
}

func TestResolve_OptionalUnsetOmitted(t *testing.T) {
	defs := []Definition{{Name: "note", Type: TypeString}}

	scope, _, err := NewResolver().Resolve(context.Background(), defs, nil, derived())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if _, ok := scope.Var["note"]; ok {
		t.Errorf("optional unset variable should be omitted from scope, got %v", scope.Var["note"])
	}
}

func TestValidateDefinition_Errors(t *testing.T) {
	tests := []struct {
		name string
		def  Definition
	}{
		{"empty name", Definition{Type: TypeString}},
		{"default and expression", Definition{Name: "a", Type: TypeString, Default: "x", Expression: "{{ .region }}"}},
		{"enum without members", Definition{Name: "a", Type: TypeEnum}},
		{"secret without source", Definition{Name: "a", Type: TypeSecret}},
		{"secret computed", Definition{Name: "a", Type: TypeSecret, Secret: &provider.SecretRef{Key: "k"}, Expression: "{{ .region }}"}},
		{"unknown type", Definition{Name: "a", Type: Type("weird")}},
		{"bad pattern", Definition{Name: "a", Type: TypeString, Pattern: "([a-z"}},
		{"bad expression", Definition{Name: "a", Type: TypeString, Expression: "{{ .region "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := NewResolver().Resolve(context.Background(), []Definition{tt.def}, nil, derived())
			if !errors.Is(err, ErrInvalidDefinition) {
				t.Errorf("expected ErrInvalidDefinition, got %v", err)
			}
		})
	}
}

func TestResolve_DuplicateName(t *testing.T) {
	defs := []Definition{
		{Name: "x", Type: TypeString, Default: "a"},
		{Name: "x", Type: TypeString, Default: "b"},
	}

	_, _, err := NewResolver().Resolve(context.Background(), defs, nil, derived())
	if !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("expected ErrInvalidDefinition for duplicate, got %v", err)
	}
}

func TestResolve_IntCoercion(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want int
	}{
		{"native int", 5, 5},
		{"json float64", float64(7), 7},
		{"string", "9", 9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defs := []Definition{{Name: "replicas", Type: TypeInt}}

			scope, _, err := NewResolver().Resolve(context.Background(), defs,
				map[string]any{"replicas": tt.raw}, derived())
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}

			if scope.Var["replicas"] != tt.want {
				t.Errorf("replicas = %v (%T), want %d", scope.Var["replicas"], scope.Var["replicas"], tt.want)
			}
		})
	}
}

func TestResolve_IntInvalid(t *testing.T) {
	defs := []Definition{{Name: "replicas", Type: TypeInt}}

	_, _, err := NewResolver().Resolve(context.Background(), defs,
		map[string]any{"replicas": "notanint"}, derived())
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("expected ErrInvalidValue, got %v", err)
	}
}

func TestResolve_BoolCoercion(t *testing.T) {
	defs := []Definition{{Name: "tls", Type: TypeBool}}

	scope, _, err := NewResolver().Resolve(context.Background(), defs,
		map[string]any{"tls": "true"}, derived())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if scope.Var["tls"] != true {
		t.Errorf("tls = %v, want true", scope.Var["tls"])
	}
}

func TestResolve_EnumValidAndInvalid(t *testing.T) {
	defs := []Definition{{Name: "tier", Type: TypeEnum, Enum: []string{"free", "pro"}}}

	scope, _, err := NewResolver().Resolve(context.Background(), defs,
		map[string]any{"tier": "pro"}, derived())
	if err != nil {
		t.Fatalf("resolve valid: %v", err)
	}

	if scope.Var["tier"] != "pro" {
		t.Errorf("tier = %v, want pro", scope.Var["tier"])
	}

	_, _, err = NewResolver().Resolve(context.Background(), defs,
		map[string]any{"tier": "enterprise"}, derived())
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("expected ErrInvalidValue for bad enum, got %v", err)
	}
}

func TestResolve_PatternValidAndInvalid(t *testing.T) {
	defs := []Definition{{Name: "slug", Type: TypeString, Pattern: "^[a-z]+$"}}

	if _, _, err := NewResolver().Resolve(context.Background(), defs,
		map[string]any{"slug": "web"}, derived()); err != nil {
		t.Fatalf("resolve valid: %v", err)
	}

	_, _, err := NewResolver().Resolve(context.Background(), defs,
		map[string]any{"slug": "Web1"}, derived())
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("expected ErrInvalidValue for bad pattern, got %v", err)
	}
}

func TestResolve_SecretProducesBindingNotInScope(t *testing.T) {
	defs := []Definition{
		{Name: "db-password", Type: TypeSecret, Secret: &provider.SecretRef{Key: "tenant/db/password", Type: secrets.SecretEnvVar}},
		{Name: "image_tag", Type: TypeString, Default: "latest"},
	}

	scope, bindings, err := NewResolver().Resolve(context.Background(), defs, nil, derived())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if _, ok := scope.Var["db-password"]; ok {
		t.Errorf("secret variable must NOT appear in scope.Var")
	}

	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}

	b := bindings[0]
	if b.VarName != "db-password" || b.EnvKey != "DB_PASSWORD" || b.Ref.Key != "tenant/db/password" {
		t.Errorf("unexpected binding: %+v", b)
	}
}

func TestResolve_ComputedOverVarsAndContext(t *testing.T) {
	defs := []Definition{
		{Name: "sub", Type: TypeString, Default: "web"},
		{Name: "host", Type: TypeString, Expression: "{{ .var.sub }}.{{ .region }}.example.com"},
	}

	scope, _, err := NewResolver().Resolve(context.Background(), defs, nil, derived())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if scope.Var["host"] != "web.us-east.example.com" {
		t.Errorf("host = %v, want web.us-east.example.com", scope.Var["host"])
	}
}

func TestResolve_ComputedOrderingIndependentOfSlicePosition(t *testing.T) {
	// "a" depends on "b" but is declared first; the fixpoint must still resolve.
	defs := []Definition{
		{Name: "a", Type: TypeString, Expression: "{{ .var.b }}-a"},
		{Name: "b", Type: TypeString, Expression: "{{ .region }}-b"},
	}

	scope, _, err := NewResolver().Resolve(context.Background(), defs, nil, derived())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if scope.Var["b"] != "us-east-b" {
		t.Errorf("b = %v, want us-east-b", scope.Var["b"])
	}

	if scope.Var["a"] != "us-east-b-a" {
		t.Errorf("a = %v, want us-east-b-a", scope.Var["a"])
	}
}

func TestResolve_ComputedCycle(t *testing.T) {
	defs := []Definition{
		{Name: "a", Type: TypeString, Expression: "{{ .var.b }}"},
		{Name: "b", Type: TypeString, Expression: "{{ .var.a }}"},
	}

	_, _, err := NewResolver().Resolve(context.Background(), defs, nil, derived())
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("expected ErrCycle, got %v", err)
	}
}

func TestValidateDefinitions(t *testing.T) {
	if err := ValidateDefinitions([]Definition{
		{Name: "a", Type: TypeString, Default: "x"},
		{Name: "b", Type: TypeInt},
	}); err != nil {
		t.Errorf("valid definitions rejected: %v", err)
	}

	if err := ValidateDefinitions([]Definition{
		{Name: "a", Type: TypeEnum},
	}); !errors.Is(err, ErrInvalidDefinition) {
		t.Errorf("expected ErrInvalidDefinition for enum without members, got %v", err)
	}

	if err := ValidateDefinitions([]Definition{
		{Name: "a", Type: TypeString},
		{Name: "a", Type: TypeString},
	}); !errors.Is(err, ErrInvalidDefinition) {
		t.Errorf("expected ErrInvalidDefinition for duplicate, got %v", err)
	}
}
