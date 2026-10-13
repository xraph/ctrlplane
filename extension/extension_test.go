package extension

import (
	"slices"
	"testing"
)

// TestNew_DependsOnGrove makes sure Forge orders the grove extension
// before this one, since Register resolves *grove.DB from the container.
func TestNew_DependsOnGrove(t *testing.T) {
	t.Parallel()

	ext := New()

	if !slices.Contains(ext.Dependencies(), GroveExtensionName) {
		t.Fatalf("Dependencies() = %v, want it to contain %q", ext.Dependencies(), GroveExtensionName)
	}
}
