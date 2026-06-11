package vars

import "github.com/xraph/ctrlplane/provider"

// Type enumerates the variable value kinds.
type Type string

const (
	// TypeString is a free-form string variable.
	TypeString Type = "string"

	// TypeInt is an integer variable.
	TypeInt Type = "int"

	// TypeBool is a boolean variable.
	TypeBool Type = "bool"

	// TypeEnum is a string variable constrained to its Enum members.
	TypeEnum Type = "enum"

	// TypeSecret is a secret reference resolved to a binding, never inlined.
	TypeSecret Type = "secret"
)

// Definition declares a single template variable. It is serialized into a
// template's variables JSONB column, so only JSON tags are needed.
type Definition struct {
	Name        string              `json:"name"                 validate:"required"`
	Description string              `json:"description,omitempty"`
	Type        Type                `json:"type"                 validate:"required"`
	Required    bool                `json:"required,omitempty"`
	Default     any                 `json:"default,omitempty"`
	Enum        []string            `json:"enum,omitempty"`
	Secret      *provider.SecretRef `json:"secret,omitempty"`
	Expression  string              `json:"expression,omitempty"`
	Pattern     string              `json:"pattern,omitempty"`
}

// InstanceContext is the per-instance derived context exposed to templates
// as {{ .instance.id }} and {{ .instance.name }}.
type InstanceContext struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TenantContext is the tenant-scoped derived context exposed as
// {{ .tenant.id }}.
type TenantContext struct {
	ID string `json:"id"`
}

// Scope is the resolved variable context handed to the renderer. Var holds
// every resolved plain and computed variable; secret variables are excluded
// (they are returned as bindings instead).
type Scope struct {
	Var      map[string]any
	Instance InstanceContext
	Tenant   TenantContext
	Region   string
}

// root builds the case-sensitive template root so expressions use lowercase
// selectors: {{ .var.x }}, {{ .instance.id }}, {{ .tenant.id }}, {{ .region }}.
func (s Scope) root() map[string]any {
	return map[string]any{
		"var": s.Var,
		"instance": map[string]any{
			"id":   s.Instance.ID,
			"name": s.Instance.Name,
		},
		"tenant": map[string]any{
			"id": s.Tenant.ID,
		},
		"region": s.Region,
	}
}
