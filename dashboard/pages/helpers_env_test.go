package pages

import (
	"testing"

	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/template"
)

// TestTotalEnvAcrossTemplate asserts the helper sums env keys across
// every service in the template — used by the templates list-view
// "N envs" badge so operators see env presence at a glance without
// drilling into the detail page.
func TestTotalEnvAcrossTemplate(t *testing.T) {
	cases := []struct {
		name string
		tmpl *template.Template
		want int
	}{
		{
			name: "nil template",
			tmpl: nil,
			want: 0,
		},
		{
			name: "no services",
			tmpl: &template.Template{},
			want: 0,
		},
		{
			name: "single service with env",
			tmpl: &template.Template{
				Services: []provider.ServiceSpec{
					{Name: "main", Env: map[string]string{"A": "1", "B": "2"}},
				},
			},
			want: 2,
		},
		{
			name: "multi-service summed",
			tmpl: &template.Template{
				Services: []provider.ServiceSpec{
					{Name: "main", Env: map[string]string{"A": "1", "B": "2"}},
					{Name: "sidecar", Env: map[string]string{"C": "3"}},
					{Name: "init"},
				},
			},
			want: 3,
		},
		{
			name: "service with empty env contributes zero",
			tmpl: &template.Template{
				Services: []provider.ServiceSpec{
					{Name: "main"},
					{Name: "sidecar", Env: map[string]string{"X": "y"}},
				},
			},
			want: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := totalEnvAcrossTemplate(tc.tmpl); got != tc.want {
				t.Errorf("totalEnvAcrossTemplate = %d, want %d", got, tc.want)
			}
		})
	}
}
