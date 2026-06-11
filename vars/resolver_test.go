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

// _ ensures the secrets import is used even before secret tests are added.
var _ = secrets.SecretEnvVar
