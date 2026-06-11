package provider

import (
	"encoding/json"
	"testing"

	"github.com/xraph/ctrlplane/secrets"
)

func TestSecretBinding_JSONRoundTrip(t *testing.T) {
	in := SecretBinding{
		VarName: "db-password",
		EnvKey:  "DB_PASSWORD",
		Ref:     SecretRef{Key: "tenant/db/password", Type: secrets.SecretEnvVar},
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out SecretBinding
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.VarName != in.VarName || out.EnvKey != in.EnvKey || out.Ref.Key != in.Ref.Key || out.Ref.Type != in.Ref.Type {
		t.Errorf("round-trip mismatch: got %+v, want %+v", out, in)
	}
}
