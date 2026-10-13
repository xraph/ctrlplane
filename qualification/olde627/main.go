// Command olde627 characterizes a fixed published e627 library.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/postgres"
)

type request struct {
	SchemaVersion string          `json:"schema_version"`
	Sequence      string          `json:"sequence"`
	Operation     string          `json:"operation"`
	Payload       json.RawMessage `json:"payload"`
}

type response struct {
	SchemaVersion string `json:"schema_version"`
	Sequence      string `json:"sequence"`
	Operation     string `json:"operation"`
	Status        string `json:"status"`
	ErrorClass    string `json:"error_class"`
	ResultNonzero bool   `json:"result_nonzero"`
	SQLState      string `json:"sql_state"`
	ResultDigest  string `json:"result_digest,omitempty"`
}

type identity struct {
	SchemaVersion string `json:"schema_version"`
	Module        string `json:"module"`
	Version       string `json:"version"`
	Sum           string `json:"sum"`
	Replaced      bool   `json:"replaced"`
	GoVersion     string `json:"go_version"`
}

func buildIdentity() (identity, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return identity{}, errors.New("missing build information")
	}

	for _, dependency := range info.Deps {
		if dependency.Path == "github.com/xraph/ctrlplane" {
			return identity{"1", dependency.Path, dependency.Version, dependency.Sum, dependency.Replace != nil, info.GoVersion}, nil
		}
	}

	return identity{}, errors.New("missing pinned dependency")
}

func strictValue(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil || token == nil {
		return errors.New("invalid JSON")
	}

	switch token {
	case json.Delim('{'):
		seen := map[string]bool{}

		for dec.More() {
			token, err = dec.Token()
			if err != nil {
				return err
			}

			key, ok := token.(string)
			if !ok || seen[key] {
				return errors.New("duplicate key")
			}

			seen[key] = true

			if err = strictValue(dec); err != nil {
				return err
			}
		}

		token, err = dec.Token()
		if err != nil || token != json.Delim('}') {
			return errors.New("invalid object")
		}
	case json.Delim('['):
		for dec.More() {
			if err = strictValue(dec); err != nil {
				return err
			}
		}

		token, err = dec.Token()
		if err != nil || token != json.Delim(']') {
			return errors.New("invalid array")
		}
	}

	return nil
}

func decode[T any](raw []byte) (T, error) {
	var out T

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	if err := strictValue(dec); err != nil {
		return out, err
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return out, errors.New("trailing input")
	}

	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&out); err != nil {
		return out, err
	}

	return out, nil
}

func invoke[T any, R any](ctx context.Context, raw []byte, fn func(context.Context, T) (R, error)) (any, error) {
	q, err := decode[T](raw)
	if err != nil {
		return nil, managed.ErrInvalid
	}

	return fn(ctx, q)
}

func execute(ctx context.Context, s *postgres.Store, q request) (any, error) {
	switch q.Operation {
	case "read_target":
		return invoke(ctx, q.Payload, s.ReadManagedTarget)
	case "set_desired":
		return invoke(ctx, q.Payload, s.SetManagedDesired)
	default:
		return nil, managed.ErrUnsupported
	}
}

func sqlState(err error) string {
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		return state.SQLState()
	}

	return ""
}

func errorClass(err error) string {
	if err == nil {
		return ""
	}

	for _, entry := range []struct {
		err  error
		name string
	}{{managed.ErrUnsupported, "unsupported"}, {managed.ErrUnavailable, "unavailable"}, {managed.ErrInvalid, "invalid"}, {managed.ErrConflict, "conflict"}, {managed.ErrAuthority, "authority"}, {managed.ErrBlocked, "blocked"}, {managed.ErrNotFound, "not_found"}, {managed.ErrCapacity, "capacity"}} {
		if errors.Is(err, entry.err) {
			return entry.name
		}
	}

	return "other"
}

func run() error {
	if len(os.Args) == 2 && os.Args[1] == "identity" {
		value, err := buildIdentity()
		if err != nil {
			return err
		}

		return json.NewEncoder(os.Stdout).Encode(value)
	}

	if len(os.Args) != 1 {
		return errors.New("invalid arguments")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	driver := pgdriver.New()
	if err := driver.Open(ctx, os.Getenv("CTRLPLANE_OLD_E627_DSN")); err != nil {
		return err
	}

	db, err := grove.Open(driver)
	if err != nil {
		return err
	}

	s := postgres.New(db)
	runErr := exchange(ctx, s)
	closeErr := s.Close()

	return errors.Join(runErr, closeErr)
}

func exchange(ctx context.Context, s *postgres.Store) error {
	reader := bufio.NewReaderSize(os.Stdin, 65537)
	total := 0

	for sequence := uint64(1); sequence <= 128; sequence++ {
		raw, err := reader.ReadSlice('\n')
		if errors.Is(err, io.EOF) && len(raw) == 0 {
			return nil
		}

		if err != nil || len(raw) > 65536 {
			return errors.New("invalid request line")
		}

		q, err := decode[request](bytes.TrimSuffix(raw, []byte{'\n'}))
		if err != nil || q.SchemaVersion != "1" || q.Sequence != strconv.FormatUint(sequence, 10) {
			return errors.New("invalid request")
		}

		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		value, callErr := execute(callCtx, s, q)

		cancel()

		out := response{SchemaVersion: "1", Sequence: q.Sequence, Operation: q.Operation, Status: "ok", ErrorClass: errorClass(callErr), SQLState: sqlState(callErr)}
		if callErr != nil {
			out.Status = "error"
		}

		if value != nil {
			out.ResultNonzero = !reflect.ValueOf(value).IsZero()

			encoded, e := json.Marshal(value)
			if e != nil {
				return e
			}

			digest := sha256.Sum256(encoded)
			out.ResultDigest = hex.EncodeToString(digest[:])
		}

		encoded, err := json.Marshal(out)
		if err != nil {
			return err
		}

		encoded = append(encoded, '\n')

		total += len(encoded)
		if len(encoded) > 8192 || total > 1<<20 {
			return errors.New("response bound")
		}

		if _, err = os.Stdout.Write(encoded); err != nil {
			return err
		}
	}

	return errors.New("request bound")
}

func main() {
	if err := run(); err != nil {
		if _, writeErr := fmt.Fprintln(os.Stderr, "olde627 fixture failed"); writeErr != nil {
			os.Exit(2)
		}

		os.Exit(1)
	}
}
