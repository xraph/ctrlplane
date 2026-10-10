package docker

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"testing"

	cerrdefs "github.com/containerd/errdefs"

	"github.com/xraph/ctrlplane/id"
)

func TestStatsOneShotWireDecodeAndClosure(t *testing.T) {
	for _, tt := range []struct {
		name, data  string
		cpu         float64
		used, limit int
		rx, tx      float64
	}{
		{name: "known sample", data: `{"cpu_stats":{"cpu_usage":{"total_usage":200},"system_cpu_usage":1000,"online_cpus":2},"precpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":500},"memory_stats":{"usage":4194304,"limit":8388608,"stats":{"cache":1048576}},"networks":{"eth0":{"rx_bytes":1048576,"tx_bytes":2097152},"eth1":{"rx_bytes":1048576,"tx_bytes":1048576}}}`, cpu: 40, used: 3, limit: 8, rx: 2, tx: 3},
		{name: "no prior sample preserves formula", data: `{"cpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":1000,"online_cpus":2}}`, cpu: 20},
		{name: "no CPU delta", data: `{"cpu_stats":{"cpu_usage":{"total_usage":100}}}`},
		{name: "empty", data: ""}, {name: "malformed", data: "not json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var body *compatBody

			p := compatProvider(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Query().Get("stream") != "false" || r.URL.Query().Get("one-shot") != "true" {
					t.Fatalf("one-shot query=%v", r.URL.Query())
				}

				resp, b := compatResponse(r, http.StatusOK, tt.data)
				body = b

				return resp, nil
			})

			usage, err := p.Resources(t.Context(), id.New(id.PrefixInstance))
			if err != nil {
				t.Fatal(err)
			}

			if usage.CPUPercent != tt.cpu || usage.MemoryUsedMB != tt.used || usage.MemoryLimitMB != tt.limit || usage.NetworkInMB != tt.rx || usage.NetworkOutMB != tt.tx {
				t.Fatalf("usage=%+v", usage)
			}

			if body == nil || !body.closed.Load() {
				t.Fatal("stats body not closed")
			}
		})
	}
}

func TestStatsDaemonErrorRetainsUpstreamBodyOwnershipLimit(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		var body *compatBody

		p := compatProvider(t, func(r *http.Request) (*http.Response, error) {
			resp, b := compatResponse(r, code, `{"message":"stats failed"}`)
			body = b

			return resp, nil
		})

		usage, err := p.Resources(t.Context(), id.New(id.PrefixInstance))
		if code == http.StatusNotFound {
			if err != nil || usage == nil || usage.CPUPercent != 0 {
				t.Fatalf("missing sample=%v err=%v", usage, err)
			}
		} else if err == nil {
			t.Fatal("daemon failure lost")
		}

		if body.closed.Load() {
			t.Fatal("synthetic transport unexpectedly closes upstream error body")
		}
	}
}

// The SDK stops reading API error bodies at 1 MiB. Keep the server response
// open to prove that Resources releases the real HTTP connection on return.
func TestStatsCappedErrorCancelsHTTPResponse(t *testing.T) {
	for _, size := range []int{1 << 20, (1 << 20) + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			clearDockerEnv(t)
			t.Setenv("DOCKER_API_VERSION", "1.56")

			canceled := make(chan struct{}, 3)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, strings.Repeat("x", size))
				w.(http.Flusher).Flush()
				<-r.Context().Done()

				canceled <- struct{}{}
			}))
			t.Cleanup(srv.Close)

			p, err := New(WithHost("tcp://" + strings.TrimPrefix(srv.URL, "http://")))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = p.cli.Close() })

			for range 3 {
				_, err = p.Resources(t.Context(), id.New(id.PrefixInstance))
				if !cerrdefs.IsInternal(err) || !strings.Contains(err.Error(), "1048576 bytes") {
					t.Fatalf("capped SDK error classification/message: %v", err)
				}

				select {
				case <-canceled:
				case <-time.After(5 * time.Second):
					t.Fatal("stats error connection remained open")
				}
			}
		})
	}
}

func TestStatsHTTPResponseBehavior(t *testing.T) {
	for _, tt := range []struct {
		name, data string
		code       int
		wantError  bool
	}{
		{"short missing", `{"message":"stats failed"}`, 404, false},
		{"short error", `{"message":"stats failed"}`, 500, true},
		{"empty error", "", 500, true},
		{"success", `{"memory_stats":{"usage":4194304,"limit":8388608}}`, 200, false},
		{"malformed sample", "not json", 200, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clearDockerEnv(t)
			t.Setenv("DOCKER_API_VERSION", "1.56")

			var connections atomic.Int32

			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.code)
				_, _ = io.WriteString(w, tt.data)
			}))
			srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
			}
			srv.Start()
			t.Cleanup(srv.Close)

			p, err := New(WithHost("tcp://" + strings.TrimPrefix(srv.URL, "http://")))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = p.cli.Close() })

			for range 3 {
				usage, gotErr := p.Resources(t.Context(), id.New(id.PrefixInstance))
				if (gotErr != nil) != tt.wantError {
					t.Fatalf("usage=%+v error=%v", usage, gotErr)
				}

				if tt.name == "short error" && (!cerrdefs.IsInternal(gotErr) || !strings.Contains(gotErr.Error(), "stats failed")) {
					t.Fatalf("SDK error changed: %v", gotErr)
				}

				if tt.name == "success" && (usage.MemoryUsedMB != 4 || usage.MemoryLimitMB != 8) {
					t.Fatalf("sample=%+v", usage)
				}
			}

			if tt.code >= 400 && connections.Load() != 1 {
				t.Fatalf("short/empty errors did not release connection for reuse: %d", connections.Load())
			}
		})
	}
}

func TestStatsCallerCancellationReachesHTTPServer(t *testing.T) {
	clearDockerEnv(t)
	t.Setenv("DOCKER_API_VERSION", "1.56")

	started, finished := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(finished)
	}))
	t.Cleanup(srv.Close)

	p, err := New(WithHost("tcp://" + strings.TrimPrefix(srv.URL, "http://")))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = p.cli.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	result := make(chan error, 1)

	go func() { _, callErr := p.Resources(ctx, id.New(id.PrefixInstance)); result <- callErr }()

	<-started
	cancel()

	select {
	case got := <-result:
		if !errors.Is(got, context.Canceled) {
			t.Fatalf("cancel error=%v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stats did not stop")
	}

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not observe cancellation")
	}
}

func TestStatsHTTPBodyReadError(t *testing.T) {
	clearDockerEnv(t)
	t.Setenv("DOCKER_API_VERSION", "1.56")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "partial")
	}))
	t.Cleanup(srv.Close)

	p, err := New(WithHost("tcp://" + strings.TrimPrefix(srv.URL, "http://")))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = p.cli.Close() })

	_, err = p.Resources(t.Context(), id.New(id.PrefixInstance))
	if !errors.Is(err, io.ErrUnexpectedEOF) || !cerrdefs.IsInternal(err) {
		t.Fatalf("body read error/classification changed: %v", err)
	}
}
