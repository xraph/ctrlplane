package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/managed"
)

func exclusionE627Identity(t *testing.T) string {
	t.Helper()

	binary := os.Getenv("CTRLPLANE_OLD_E627_BINARY")
	if binary == "" {
		if os.Getenv("CTRLPLANE_WRITER_EXCLUSION_REQUIRED") == "1" || os.Getenv("CTRLPLANE_OLD_E627_REQUIRED") == "1" {
			t.Fatal("required e627 binary missing")
		}

		t.Skip("e627 binary not configured")
	}

	raw, err := exec.CommandContext(t.Context(), binary, "identity").Output()
	if err != nil {
		t.Fatal(err)
	}

	var identity struct {
		SchemaVersion string `json:"schema_version"`
		Module        string `json:"module"`
		Version       string `json:"version"`
		Sum           string `json:"sum"`
		Replaced      bool   `json:"replaced"`
		GoVersion     string `json:"go_version"`
	}

	if err = json.Unmarshal(raw, &identity); err != nil || identity.SchemaVersion != "1" || identity.Module != "github.com/xraph/ctrlplane" || identity.Version != "v1.7.1-0.20261011163110-e627f3173e76" || identity.Sum != "h1:CUOpUx7/ESFCdNZTGETIikEEAJPyq+iU0PDkr0/HfXU=" || identity.Replaced {
		t.Fatal("wrong e627 identity", identity, err)
	}

	artifact, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}

	hash := sha256.New()
	_, hashErr := io.Copy(hash, artifact)

	closeErr := artifact.Close()
	if hashErr != nil || closeErr != nil {
		t.Fatal(hashErr, closeErr)
	}

	t.Logf("e627 module=%s version=%s sum=%s go=%s binary_sha256=%s", identity.Module, identity.Version, identity.Sum, identity.GoVersion, hex.EncodeToString(hash.Sum(nil)))

	return binary
}

type exclusionE627Result struct {
	SchemaVersion string `json:"schema_version"`
	Sequence      string `json:"sequence"`
	Operation     string `json:"operation"`
	Status        string `json:"status"`
	ErrorClass    string `json:"error_class"`
	SQLState      string `json:"sql_state"`
	ResultNonzero bool   `json:"result_nonzero"`
	ResultDigest  string `json:"result_digest"`
}

func exclusionE627Call(t *testing.T, binary, dsn, operation string, payload any) exclusionE627Result {
	t.Helper()

	raw, err := json.Marshal(struct {
		SchemaVersion string `json:"schema_version"`
		Sequence      string `json:"sequence"`
		Operation     string `json:"operation"`
		Payload       any    `json:"payload"`
	}{"1", "1", operation, payload})
	if err != nil || len(raw)+1 > 65536 {
		t.Fatal("e627 request bound", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, binary)
	command.Env = []string{"CTRLPLANE_OLD_E627_DSN=" + dsn}
	command.Stdin = bytes.NewReader(append(raw, '\n'))

	line, err := command.Output()
	if err != nil || len(line) > 8192 {
		t.Fatal("e627 response bound or child failure", err)
	}

	var out exclusionE627Result

	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()

	if err = decoder.Decode(&out); err != nil {
		t.Fatal(err)
	}

	if out.SchemaVersion != "1" || out.Sequence != "1" || out.Operation != operation {
		t.Fatal("unexpected e627 response", out)
	}

	t.Logf("e627 operation=%s status=%s error_class=%s sql_state=%s result_nonzero=%t", operation, out.Status, out.ErrorClass, out.SQLState, out.ResultNonzero)

	return out
}

func TestWriterExclusionActualOldE627Desired(t *testing.T) {
	binary := exclusionE627Identity(t)
	positivePassed := false

	t.Run("unfenced_actual_positive_control", func(t *testing.T) {
		f := newExclusionFixture(t)
		target := newManagedFixture(t, f.admin)
		before := target.target()
		q := managed.DesiredRequest{Command: target.command(), Mode: managed.Paused}

		out := exclusionE627Call(t, binary, f.dsn(f.legacy), "set_desired", q)
		if out.Status != "ok" || out.ErrorClass != "" || out.SQLState != "" || !out.ResultNonzero {
			t.Fatal("actual e627 mutation positive control failed", out)
		}

		after := target.target()
		if after.Desired != managed.Paused || after.Revision != before.Revision+1 || after.DesiredRevision != before.DesiredRevision+1 {
			t.Fatal("e627 did not commit the expected desired-state mutation")
		}

		var saved []byte

		if err := pgdriver.Unwrap(f.admin.DB()).NewRaw("SELECT receipt FROM cp_managed_commands WHERE target_id=$1 AND operation_id=$2", q.InstanceID.String(), q.ID.String()).Scan(t.Context(), &saved); err != nil {
			t.Fatal(err)
		}

		var receipt managed.Receipt

		if err := json.Unmarshal(saved, &receipt); err != nil || receipt.ID != q.ID || receipt.Revision != after.Revision || receipt.LeaseEpoch != q.LeaseEpoch || receipt.AcceptedAt.IsZero() {
			t.Fatal("e627 receipt association missing", receipt, err)
		}

		canonical, err := json.Marshal(receipt)
		if err != nil || managed.Digest(canonical) != out.ResultDigest {
			t.Fatal("e627 returned receipt differs from retained receipt", err)
		}

		positivePassed = true
	})

	if !positivePassed {
		t.Fatal("no passing actual e627 positive control; refusal evidence invalid")
	}

	t.Run("fenced_same_old_operation", func(t *testing.T) {
		f := newExclusionFixture(t)
		target := newManagedFixture(t, f.admin)
		intact := target.target()

		f.drain()

		if _, err := f.activate(t.Context(), f.activation()); err != nil {
			t.Fatal(err)
		}

		readOnly := f.readOnly(f.legacy)
		if err := f.closeStore(readOnly); err != nil {
			t.Fatal(err)
		}

		q := managed.DesiredRequest{Command: target.command(), Mode: managed.Paused}
		before := f.snapshot()

		reached := exclusionE627Call(t, binary, f.dsn(f.legacy), "read_target", target.k)
		if reached.Status != "ok" || !reached.ResultNonzero {
			t.Fatal("fenced e627 child did not reach intended target", reached)
		}

		out := exclusionE627Call(t, binary, f.dsn(f.legacy), "set_desired", q)
		if out.Status != "error" || out.SQLState != "42501" || out.ResultNonzero {
			t.Fatal("expected e627 database permission denial without a receipt", out)
		}

		f.unchanged(before)

		if after := target.target(); !reflect.DeepEqual(after, intact) {
			t.Fatal("e627 denied mutation changed current target")
		}
	})
}
