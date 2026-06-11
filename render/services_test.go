package render

import (
	"testing"

	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/vars"
)

func scopeWith(v map[string]any) vars.Scope {
	return vars.Scope{
		Var:      v,
		Instance: vars.InstanceContext{ID: "inst_1", Name: "web"},
		Tenant:   vars.TenantContext{ID: "tnt_1"},
		Region:   "us-east",
	}
}

func TestRender_Services(t *testing.T) {
	src := provider.DeploymentSource{
		Type: provider.SourceServices,
		Services: []provider.ServiceSpec{{
			Name:    "web",
			Image:   "nginx:{{ .var.tag }}",
			Env:     map[string]string{"REGION": "{{ .region }}", "STATIC": "x"},
			Command: []string{"run", "--name={{ .instance.name }}"},
			Args:    []string{"--tenant={{ .tenant.id }}"},
		}},
	}

	out, err := Render(src, scopeWith(map[string]any{"tag": "1.25"}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if out.Type != provider.SourceServices || len(out.Services) != 1 {
		t.Fatalf("unexpected output: %+v", out)
	}

	svc := out.Services[0]
	if svc.Image != "nginx:1.25" {
		t.Errorf("image = %q, want nginx:1.25", svc.Image)
	}

	if svc.Env["REGION"] != "us-east" || svc.Env["STATIC"] != "x" {
		t.Errorf("env = %v", svc.Env)
	}

	if svc.Command[1] != "--name=web" {
		t.Errorf("command = %v", svc.Command)
	}

	if svc.Args[0] != "--tenant=tnt_1" {
		t.Errorf("args = %v", svc.Args)
	}
}

func TestRender_Services_MissingVar(t *testing.T) {
	src := provider.DeploymentSource{
		Type:     provider.SourceServices,
		Services: []provider.ServiceSpec{{Name: "web", Image: "nginx:{{ .var.absent }}"}},
	}

	if _, err := Render(src, scopeWith(nil)); err == nil {
		t.Fatal("expected error for missing variable, got nil")
	}
}
