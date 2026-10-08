package contract

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/secrets"
	"github.com/xraph/ctrlplane/telemetry"
	"github.com/xraph/ctrlplane/template"
	"github.com/xraph/ctrlplane/vars"
	"github.com/xraph/ctrlplane/worker"
)

func projectedJSON(t *testing.T, value any) []byte {
	t.Helper()

	out, err := projectResponse(value)
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}

	return data
}
func TestResponseProjectionHidesManagedSecretsAndDiagnostics(t *testing.T) {
	const private = "private-provider-credential"
	for _, test := range []struct {
		name  string
		value any
	}{
		{"secret", &secrets.Secret{Key: "TOKEN", Value: []byte(private)}},
		{"deployment", &deploy.Deployment{Error: private}},
		{"worker", worker.WorkerInfo{LastErr: private}},
		{"bootstrap", &bootstrap.BootstrapWorkload{LastError: private}},
		{"provider", &admin.ProviderHealthResult{Message: private}},
		{"health", &health.HealthResult{Message: private}},
		{"event", &event.Event{Payload: map[string]any{"error": private, "unexpected": map[string]any{"password": private}}}},
		{"audit", admin.AuditEntry{Details: map[string]any{"error": private, "password": private}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := projectedJSON(t, test.value)
			if bytes.Contains(data, []byte(private)) {
				t.Fatalf("private diagnostic exposed: %s", data)
			}
		})
	}
}
func TestResponseProjectionPreservesAuthoringAndUsesUTC(t *testing.T) {
	created := time.Date(2026, 10, 8, 9, 0, 0, 0, time.FixedZone("local", -5*3600))
	row := &template.Template{
		Entity: ctrlplane.Entity{ID: id.New(id.PrefixTemplate), CreatedAt: created, UpdatedAt: created},
		Name:   "API", Notes: "keep notes", Labels: map[string]string{"team": "platform"},
		Services:  []provider.ServiceSpec{{Name: "api", Image: "api:1", Env: map[string]string{"MODE": "test"}, Secrets: []provider.SecretRef{{Key: "TOKEN", Type: secrets.SecretEnvVar}}, ConfigFiles: []provider.ConfigFile{{Name: "settings", Path: "/etc/app.json", Content: `{"mode":"test"}`}}}},
		Source:    provider.DeploymentSource{Type: provider.SourceHelm, Helm: &provider.HelmSource{Chart: "api", Version: "1.2.3", Values: map[string]any{"replicas": float64(3)}}},
		Variables: []vars.Definition{{Name: "replicas", Default: float64(3)}},
	}
	data := projectedJSON(t, row)

	var got template.Template
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	if got.ID != row.ID || !got.CreatedAt.Equal(created) || !bytes.Contains(data, []byte("2026-10-08T14:00:00Z")) {
		t.Fatal("identity or UTC timestamp changed")
	}

	if got.Source.Helm.Version != "1.2.3" || got.Source.Helm.Values["replicas"] != float64(3) || got.Services[0].Secrets[0].Key != "TOKEN" || got.Services[0].ConfigFiles[0].Content != row.Services[0].ConfigFiles[0].Content || got.Variables[0].Default != float64(3) || got.Notes != row.Notes {
		t.Fatalf("lost authoring fields: %s", data)
	}
}
func TestResponseProjectionUnknownIsNotZero(t *testing.T) {
	data := projectedJSON(t, &health.InstanceHealth{Status: health.StatusUnknown})
	for _, want := range []string{`"checks":[]`, `"last_checked":null`, `"uptime_percent":null`, `"consecutive_failures":null`} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("missing %s: %s", want, data)
		}
	}

	data = projectedJSON(t, &telemetry.DashboardData{})
	if !bytes.Contains(data, []byte(`"resources":null`)) || bytes.Contains(data, []byte("request_rate")) {
		t.Fatalf("invented telemetry: %s", data)
	}

	if _, err := projectResponse(struct{ Password string }{"not-a-supported-response"}); err == nil {
		t.Fatal("accepted an undeclared response")
	}
}
