package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/xraph/ctrlplane/managed"
)

func TestDecodeRejectsAmbiguousEnvelope(t *testing.T) {
	for _, raw := range []string{
		`{"schema_version":"1","schema_version":"2"}`,
		`{"unknown":true}`,
		`{"payload":{"mode":"active","mode":"paused"}}`,
		`{} {}`,
		`null`,
	} {
		if _, err := decode[request]([]byte(raw)); err == nil {
			t.Errorf("accepted invalid request %s", raw)
		}
	}
}

func TestExecuteRejectsUnsupportedOperation(t *testing.T) {
	if _, err := execute(context.Background(), nil, request{Operation: "arbitrary_sql"}); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal(err)
	}
}

type denial struct{}

func (denial) Error() string    { return "permission denied" }
func (denial) SQLState() string { return "42501" }

func TestSQLStatePreservesWrappedDenial(t *testing.T) {
	if got := sqlState(fmt.Errorf("wrapped: %w", denial{})); got != "42501" {
		t.Fatal(got)
	}

	if got := sqlState(managed.ErrConflict); got != "" {
		t.Fatal(got)
	}
}
