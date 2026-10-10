package docker

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

type compatTransport func(*http.Request) (*http.Response, error)

func (f compatTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type compatBody struct {
	io.Reader

	closed atomic.Bool
	done   chan struct{}
}

func (b *compatBody) Close() error {
	if !b.closed.Swap(true) {
		close(b.done)

		if closer, ok := b.Reader.(io.Closer); ok {
			return closer.Close()
		}
	}

	return nil
}

func compatResponse(r *http.Request, status int, data string) (*http.Response, *compatBody) {
	b := &compatBody{Reader: strings.NewReader(data), done: make(chan struct{})}

	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: b, Request: r}, b
}

func compatProvider(t *testing.T, transport compatTransport) *Provider {
	t.Helper()

	cli, err := client.New(client.WithHost("tcp://localhost:2375"), client.WithAPIVersion("1.56"), client.WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := cli.Close(); err != nil {
			t.Error(err)
		}
	})

	return &Provider{cli: cli}
}

func clearDockerEnv(t *testing.T) {
	t.Helper()

	for _, key := range []string{"DOCKER_HOST", "DOCKER_API_VERSION", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY"} {
		t.Setenv(key, "")
	}
}

func TestClientNegotiationAndEnvironment(t *testing.T) {
	tests := []struct {
		name, daemon, override, want string
		bad                          bool
	}{
		{name: "minimum", daemon: "1.40", want: "1.40"},
		{name: "older supported", daemon: "1.45", want: "1.45"},
		{name: "maximum", daemon: "1.56", want: "1.56"},
		{name: "newer capped", daemon: "1.99", want: "1.56"},
		{name: "explicit environment", daemon: "1.45", override: "1.48", want: "1.48"},
		{name: "below minimum", daemon: "1.39", bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearDockerEnv(t)

			var (
				pings     atomic.Int32
				requested string
			)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/_ping" {
					pings.Add(1)
					w.Header().Set("Api-Version", tt.daemon)

					return
				}

				requested = r.URL.Path

				w.Header().Set("Content-Type", "application/json")

				if _, err := io.WriteString(w, "[]"); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(srv.Close)
			t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
			t.Setenv("DOCKER_API_VERSION", tt.override)

			p, err := New(WithHost("tcp://" + strings.TrimPrefix(srv.URL, "http://")))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() {
				if err := p.cli.Close(); err != nil {
					t.Error(err)
				}
			})

			_, err = p.listProjectContainers(t.Context(), id.New(id.PrefixInstance))
			if tt.bad {
				// The SDK's lazy path ignores a negotiation error and keeps its maximum.
				// Explicit negotiation rejects the unsupported daemon; do not claim that
				// ordinary provider requests are a minimum-version enforcement gate.
				if err != nil || requested != "/v1.56/containers/json" {
					t.Fatalf("below-minimum fallback request=%q err=%v", requested, err)
				}

				if _, err := p.cli.Ping(t.Context(), client.PingOptions{NegotiateAPIVersion: true}); !cerrdefs.IsInvalidArgument(err) {
					t.Fatalf("explicit below-minimum negotiation=%v", err)
				}

				return
			}

			if err != nil || requested != "/v"+tt.want+"/containers/json" {
				t.Fatalf("path=%q err=%v", requested, err)
			}

			expected := int32(1)
			if tt.override != "" {
				expected = 0
			}

			if pings.Load() != expected {
				t.Fatalf("negotiation pings=%d want=%d", pings.Load(), expected)
			}
		})
	}
}

func TestHealthPingDoesNotNegotiate(t *testing.T) {
	clearDockerEnv(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_ping" {
			t.Errorf("unexpected health path %s", r.URL.Path)
		}

		w.Header().Set("Api-Version", "1.40")
	}))
	t.Cleanup(srv.Close)

	p, err := New(WithHost("tcp://" + strings.TrimPrefix(srv.URL, "http://")))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := p.cli.Close(); err != nil {
			t.Error(err)
		}
	})

	status, err := p.HealthCheck(t.Context())
	if err != nil || !status.Healthy || p.cli.ClientVersion() != "1.56" {
		t.Fatalf("health=%+v err=%v version=%s", status, err, p.cli.ClientVersion())
	}
}

func TestClientTLSEnvironment(t *testing.T) {
	clearDockerEnv(t)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			t.Error("missing TLS")
		}

		w.Header().Set("Api-Version", "1.56")
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})

	key, err := x509.MarshalPKCS8PrivateKey(srv.TLS.Certificates[0].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}

	for name, data := range map[string][]byte{"ca.pem": cert, "cert.pem": cert, "key.pem": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("DOCKER_CERT_PATH", dir)
	t.Setenv("DOCKER_TLS_VERIFY", "1")

	p, err := New(WithHost("tcp://" + strings.TrimPrefix(srv.URL, "https://")))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := p.cli.Close(); err != nil {
			t.Error(err)
		}
	})

	status, err := p.HealthCheck(t.Context())
	if err != nil || !status.Healthy {
		t.Fatalf("TLS health=%+v err=%v", status, err)
	}

	t.Setenv("DOCKER_CERT_PATH", t.TempDir())

	if _, err := New(); err == nil {
		t.Fatal("missing certificates accepted")
	}
}

func TestProjectNetworkListAndLifecycleWire(t *testing.T) {
	iid := id.New(id.PrefixInstance)

	var calls []string

	p := compatProvider(t, func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		status, data := http.StatusNoContent, ""

		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			var filters map[string]map[string]bool
			if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters); err != nil {
				t.Fatal(err)
			}

			if r.URL.Query().Get("all") != "1" || !filters["label"]["ctrlplane.project="+projectName(iid)] {
				t.Fatalf("query=%v", r.URL.Query())
			}

			status = http.StatusOK
			data = `[{"Id":"main","Names":["/app"],"State":"running","Labels":{"ctrlplane.service":"api","ctrlplane.role":"main"}},{"Id":"init","Labels":{"ctrlplane.role":"init"}}]`
		case strings.HasSuffix(r.URL.Path, "/networks/"+projectNetwork(iid)) && r.Method == http.MethodGet:
			status = http.StatusNotFound
			data = `{"message":"missing"}`
		case strings.HasSuffix(r.URL.Path, "/networks/create"):
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}

			labels := req["Labels"].(map[string]any)
			if req["Name"] != projectNetwork(iid) || req["Driver"] != "bridge" || labels["ctrlplane.project"] != projectName(iid) || labels["ctrlplane.tenant"] != "tenant" {
				t.Fatalf("network=%v", req)
			}

			status = http.StatusCreated
			data = `{"Id":"net"}`
		case r.Method == http.MethodPost && (strings.HasSuffix(r.URL.Path, "/stop") || strings.HasSuffix(r.URL.Path, "/restart")):
			if r.URL.Query().Has("t") {
				t.Fatal("nil stop timeout changed")
			}
		}

		resp, _ := compatResponse(r, status, data)

		return resp, nil
	})
	if err := p.ensureProjectNetwork(t.Context(), iid, "tenant", nil); err != nil {
		t.Fatal(err)
	}

	containers, err := p.listProjectContainers(t.Context(), iid)
	if err != nil || containers[0].Name != "app" || containers[0].State != "running" {
		t.Fatalf("list=%v err=%v", containers, err)
	}

	for _, op := range []func(context.Context, id.ID) error{p.Start, p.Stop, p.Restart, p.Deprovision} {
		if err := op(t.Context(), iid); err != nil {
			t.Fatal(err)
		}
	}

	for _, call := range calls {
		if strings.Contains(call, "/init/start") || strings.Contains(call, "/init/stop") || strings.Contains(call, "/init/restart") {
			t.Fatalf("init lifecycle changed: %s", call)
		}
	}

	if err := p.removeIfExists(t.Context(), "already-gone"); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleNotFoundAndOtherErrors(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			p := compatProvider(t, func(r *http.Request) (*http.Response, error) {
				resp, _ := compatResponse(r, code, `{"message":"daemon error"}`)

				return resp, nil
			})

			err := p.removeIfExists(t.Context(), "x")
			if (code == http.StatusNotFound && err != nil) || (code != http.StatusNotFound && err == nil) {
				t.Fatalf("remove err=%v", err)
			}

			_, err = p.cli.ContainerInspect(t.Context(), "x", client.ContainerInspectOptions{})
			if code == http.StatusNotFound && !cerrdefs.IsNotFound(err) {
				t.Fatalf("not-found lost: %v", err)
			}
		})
	}
}

func TestPartialDeployReusesInspectAndLeavesOtherServices(t *testing.T) {
	iid := id.New(id.PrefixInstance)

	var (
		calls   []string
		created container.CreateRequest
	)

	p := compatProvider(t, func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		code, data := http.StatusNoContent, ""

		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			code = http.StatusOK
			data = `[{"Id":"api-old","Names":["/api"],"Labels":{"ctrlplane.service":"api","ctrlplane.role":"main"}},{"Id":"sidecar","Labels":{"ctrlplane.service":"proxy","ctrlplane.role":"sidecar"}}]`
		case strings.HasSuffix(r.URL.Path, "/api-old/json"):
			code = http.StatusOK
			data = `{"Config":{"Image":"old:v1","Env":["KEEP=yes"],"Labels":{"ctrlplane.service":"api"},"ExposedPorts":{"8080/tcp":{}},"Entrypoint":["entry"],"Cmd":["arg"]},"HostConfig":{"Binds":["volume:/data"],"NetworkMode":"project-net","PortBindings":{"8080/tcp":[{"HostIp":"0.0.0.0","HostPort":"9000"}]}}}`
		case strings.HasSuffix(r.URL.Path, "/images/create"):
			code = http.StatusOK
			data = `{"status":"pulled"}`
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Fatal(err)
			}

			if r.URL.Query().Get("name") != "api" || r.URL.Query().Has("platform") {
				t.Fatalf("create query=%v", r.URL.Query())
			}

			code = http.StatusCreated
			data = `{"Id":"api-new"}`
		}

		resp, _ := compatResponse(r, code, data)

		return resp, nil
	})

	result, err := p.Deploy(t.Context(), provider.DeployRequest{InstanceID: iid, Services: []provider.ServiceDeploySpec{{Name: "api", Image: "new:v2"}}})
	if err != nil || result.Status != "succeeded" {
		t.Fatalf("deploy=%+v err=%v", result, err)
	}

	if created.Image != "new:v2" || !reflect.DeepEqual(created.Env, []string{"KEEP=yes"}) || !reflect.DeepEqual(created.Entrypoint, []string{"entry"}) || !reflect.DeepEqual(created.Cmd, []string{"arg"}) || !reflect.DeepEqual(created.HostConfig.Binds, []string{"volume:/data"}) || created.HostConfig.NetworkMode != "project-net" || created.Labels["ctrlplane.service"] != "api" || len(created.ExposedPorts) != 1 {
		t.Fatalf("preserved config=%+v host=%+v", created.Config, created.HostConfig)
	}

	if !reflect.DeepEqual(created.NetworkingConfig.EndpointsConfig[projectNetwork(iid)].Aliases, []string{"api"}) {
		t.Fatalf("aliases=%v", created.NetworkingConfig)
	}

	for _, call := range calls {
		if strings.Contains(call, "/sidecar/") {
			t.Fatalf("untargeted service touched: %s", call)
		}
	}
}

func TestInitOrderingAndFailures(t *testing.T) {
	for _, tt := range []struct {
		name, wait, want string
		code             int
		cancel           bool
	}{{name: "success", wait: `{"StatusCode":0}`, code: http.StatusOK}, {name: "nonzero", wait: `{"StatusCode":7}`, want: "init exited 7", code: http.StatusOK}, {name: "runtime", wait: `{"StatusCode":0,"Error":{"Message":"runtime failed"}}`, want: "runtime failed", code: http.StatusOK}, {name: "daemon", wait: `{"message":"wait failed"}`, want: "wait init", code: http.StatusInternalServerError}, {name: "cancel", want: "init timed out after 10m0s", cancel: true}} {
		t.Run(tt.name, func(t *testing.T) {
			var order []string

			p := compatProvider(t, func(r *http.Request) (*http.Response, error) {
				code, data := http.StatusNoContent, ""

				switch {
				case strings.HasSuffix(r.URL.Path, "/containers/create"):
					name := r.URL.Query().Get("name")
					order = append(order, "create "+name)
					code = http.StatusCreated
					data = `{"Id":"` + name + `"}`
				case strings.HasSuffix(r.URL.Path, "/images/create"):
					code = http.StatusOK
					data = `{"status":"ok"}`
				case strings.HasSuffix(r.URL.Path, "/start"):
					order = append(order, "start")
				case strings.HasSuffix(r.URL.Path, "/wait"):
					order = append(order, "wait")

					if r.URL.Query().Get("condition") != "not-running" {
						t.Fatalf("wait query=%v", r.URL.Query())
					}

					deadline, ok := r.Context().Deadline()
					if !ok || time.Until(deadline) > initTimeout || time.Until(deadline) < initTimeout-time.Minute {
						t.Fatal("ten minute wait deadline changed")
					}

					if tt.cancel {
						return nil, context.Canceled
					}

					code, data = tt.code, tt.wait
				}

				resp, _ := compatResponse(r, code, data)

				return resp, nil
			})
			iid := id.New(id.PrefixInstance)

			err := p.runInits(t.Context(), provider.ProvisionRequest{InstanceID: iid}, []provider.ServiceSpec{{Name: "first", Image: "alpine:3", Role: provider.RoleInit}, {Name: "second", Image: "alpine:3", Role: provider.RoleInit}})
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}

				want := []string{"create " + serviceContainerName(iid, "first"), "start", "wait", "create " + serviceContainerName(iid, "second"), "start", "wait"}
				if !reflect.DeepEqual(order, want) {
					t.Fatalf("order=%v", order)
				}
			} else if err == nil || (!strings.Contains(err.Error(), tt.want) && (!tt.cancel || !errors.Is(err, context.Canceled))) {
				t.Fatalf("error=%v want=%q", err, tt.want)
			}

			if tt.want != "" && len(order) > 3 {
				t.Fatalf("second init ran after failure: %v", order)
			}
		})
	}
}

func TestLogsAndPullStreams(t *testing.T) {
	var bodies []*compatBody

	stamp := "2026-10-10T12:00:00Z"
	frame := func(kind byte, line string) []byte {
		buf := make([]byte, 8+len(line))
		buf[0] = kind
		binary.BigEndian.PutUint32(buf[4:], uint32(len(line)))
		copy(buf[8:], line)

		return buf
	}
	raw := append(frame(1, stamp+" out\n"), frame(2, stamp+" err\n")...)
	p := compatProvider(t, func(r *http.Request) (*http.Response, error) {
		data := "[]"

		if strings.HasSuffix(r.URL.Path, "/logs") {
			q := r.URL.Query()
			if q.Get("stdout") != "1" || q.Get("stderr") != "1" || q.Get("timestamps") != "1" || q.Get("follow") != "1" || q.Get("tail") != "12" || q.Get("since") != "1791633600.000000000" {
				t.Fatalf("logs query=%v", q)
			}

			data = string(raw)
		} else if strings.HasSuffix(r.URL.Path, "/images/create") {
			data = `{"error":"retained drain-only policy"}`
		}

		resp, b := compatResponse(r, http.StatusOK, data)
		bodies = append(bodies, b)

		return resp, nil
	})

	since, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		t.Fatal(err)
	}

	rc, err := p.Logs(t.Context(), id.New(id.PrefixInstance), provider.LogOptions{Follow: true, Tail: 12, Since: since})
	if err != nil {
		t.Fatal(err)
	}

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}

	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}

	var events []logEvent

	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var ev logEvent
		if err := dec.Decode(&ev); err != nil {
			t.Fatal(err)
		}

		events = append(events, ev)
	}

	if len(events) != 2 || events[0].Stream != "stdout" || events[0].Line != "out" || events[1].Stream != "stderr" || events[1].Timestamp != stamp {
		t.Fatalf("events=%v", events)
	}

	if err := p.pullImage(t.Context(), "alpine:3"); err != nil {
		t.Fatalf("drain policy changed: %v", err)
	}

	for _, body := range bodies {
		select {
		case <-body.done:
		case <-time.After(time.Second):
			t.Fatal("opened stream not closed")
		}
	}
}

func TestProviderStreamCancellationClosesBodies(t *testing.T) {
	for _, kind := range []string{"stats", "logs"} {
		t.Run(kind, func(t *testing.T) {
			opened := make(chan *compatBody, 1)
			p := compatProvider(t, func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/containers/json") {
					resp, _ := compatResponse(r, http.StatusOK, "[]")

					return resp, nil
				}

				reader, writer := io.Pipe()

				t.Cleanup(func() {
					if err := writer.Close(); err != nil {
						t.Error(err)
					}
				})

				body := &compatBody{Reader: reader, done: make(chan struct{})}
				opened <- body

				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body, Request: r}, nil
			})
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)

			finished := make(chan error, 1)

			go func() {
				if kind == "stats" {
					_, err := p.Resources(ctx, id.New(id.PrefixInstance))
					finished <- err

					return
				}

				rc, err := p.Logs(ctx, id.New(id.PrefixInstance), provider.LogOptions{Follow: true})
				if err != nil {
					finished <- err

					return
				}

				_, readErr := io.Copy(io.Discard, rc)

				closeErr := rc.Close()
				finished <- errors.Join(readErr, closeErr)
			}()

			var body *compatBody
			select {
			case body = <-opened:
			case <-time.After(time.Second):
				t.Fatal("stream not opened")
			}

			cancel()

			select {
			case <-body.done:
			case <-time.After(time.Second):
				t.Fatal("cancel did not close source body")
			}

			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("cancel did not finish provider stream")
			}
		})
	}
}

func TestLifecycleErrorsRemainVisible(t *testing.T) {
	for _, op := range []struct {
		name string
		run  func(*Provider, context.Context, id.ID) error
	}{
		{name: "start", run: (*Provider).Start}, {name: "stop", run: (*Provider).Stop}, {name: "restart", run: (*Provider).Restart}, {name: "deprovision", run: (*Provider).Deprovision},
	} {
		t.Run(op.name, func(t *testing.T) {
			p := compatProvider(t, func(r *http.Request) (*http.Response, error) {
				code, data := http.StatusInternalServerError, `{"message":"mutation failed"}`
				if strings.HasSuffix(r.URL.Path, "/containers/json") {
					code = http.StatusOK
					data = `[{"Id":"main","Labels":{"ctrlplane.role":"main"}}]`
				}

				resp, _ := compatResponse(r, code, data)

				return resp, nil
			})
			if err := op.run(p, t.Context(), id.New(id.PrefixInstance)); err == nil || !strings.Contains(err.Error(), "mutation failed") {
				t.Fatalf("lost lifecycle error: %v", err)
			}
		})
	}
}

// TestTrustedDockerFixture uses only a cached, trusted image on an explicitly
// configured daemon. The fixture transport applies limits before creation;
// it does not add a production resource-policy claim to the provider.
func TestTrustedDockerFixture(t *testing.T) {
	host := os.Getenv("CTRLPLANE_TEST_DOCKER_HOST")
	if host == "" {
		t.Skip("CTRLPLANE_TEST_DOCKER_HOST is unset")
	}

	if !strings.HasPrefix(host, "unix://") {
		t.Fatal("trusted local fixture requires a Unix daemon socket")
	}

	clearDockerEnv(t)

	socket := strings.TrimPrefix(host, "unix://")
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	t.Cleanup(transport.CloseIdleConnections)

	bounded := compatTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/create") {
			var create container.CreateRequest
			if err := json.NewDecoder(r.Body).Decode(&create); err != nil {
				return nil, err
			}

			if err := r.Body.Close(); err != nil {
				return nil, err
			}

			create.HostConfig.Memory = 128 * 1024 * 1024
			create.HostConfig.NanoCPUs = 250000000
			pids := int64(32)
			create.HostConfig.PidsLimit = &pids

			data, err := json.Marshal(create)
			if err != nil {
				return nil, err
			}

			r = r.Clone(r.Context())
			r.Body = io.NopCloser(bytes.NewReader(data))
			r.ContentLength = int64(len(data))
			r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
		}

		return transport.RoundTrip(r)
	})

	cli, err := client.New(client.WithHost(host), client.WithHTTPClient(&http.Client{Transport: bounded}))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := cli.Close(); err != nil {
			t.Error(err)
		}
	})

	p := &Provider{cli: cli}
	iid := id.New(id.PrefixInstance)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		if err := p.Deprovision(ctx, iid); err != nil {
			t.Error(err)
		}
	})

	req := provider.ProvisionRequest{InstanceID: iid, TenantID: "task3-fixture", Labels: map[string]string{"task3.owner": "ctrlplane-dependency"}, Services: []provider.ServiceSpec{
		{Name: "init", Role: provider.RoleInit, Image: "alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc", Command: []string{"sh", "-c"}, Args: []string{"echo init-done"}},
		{Name: "api", Role: provider.RoleMain, Image: "alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc", Command: []string{"sh", "-c"}, Args: []string{"echo task3-live-log; sleep 120"}, Ports: []provider.PortSpec{{Container: 8080}}},
		{Name: "sidecar", Role: provider.RoleSidecar, Image: "alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc", Command: []string{"sleep"}, Args: []string{"120"}},
	}}

	result, err := p.Provision(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("owned project=%s network=%s service IDs=%v endpoints=%v negotiatedAPI=%s", projectName(iid), projectNetwork(iid), result.ServiceRefs, result.Endpoints, cli.ClientVersion())

	network, networkErr := cli.NetworkInspect(t.Context(), projectNetwork(iid), client.NetworkInspectOptions{})
	if networkErr != nil {
		t.Fatal(networkErr)
	}

	t.Logf("owned network ID=%s", network.Network.ID)

	if len(result.ServiceRefs) != 2 || len(result.Endpoints) < 2 {
		t.Fatalf("live project=%+v", result)
	}

	containers, err := p.listProjectContainers(t.Context(), iid)
	if err != nil {
		t.Fatal(err)
	}

	if len(containers) != 3 {
		t.Fatalf("project members=%v", containers)
	}

	for _, c := range containers {
		inspect, err := cli.ContainerInspect(t.Context(), c.ID, client.ContainerInspectOptions{})
		if err != nil {
			t.Fatal(err)
		}

		hc := inspect.Container.HostConfig
		if hc.Memory != 128*1024*1024 || hc.NanoCPUs != 250000000 || hc.PidsLimit == nil || *hc.PidsLimit != 32 {
			t.Fatalf("fixture not bounded: %+v", hc)
		}

		t.Logf("owned container=%s name=%s role=%s memory=%d nanoCPUs=%d pids=%d", c.ID, c.Name, c.Role, hc.Memory, hc.NanoCPUs, *hc.PidsLimit)
	}

	status, err := p.Status(t.Context(), iid)
	if err != nil || !status.Ready || status.State != provider.StateRunning {
		t.Fatalf("live status=%+v err=%v", status, err)
	}

	rc, err := p.Logs(t.Context(), iid, provider.LogOptions{ServiceName: "api", Tail: 10})
	if err != nil {
		t.Fatal(err)
	}

	logs, readErr := io.ReadAll(rc)

	closeErr := rc.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(logs), "task3-live-log") {
		t.Fatalf("live logs=%s", logs)
	}
	// Resources retains its legacy-name lookup; sample the actual project member
	// with the supported client, then apply the same provider decoder.
	stats, err := cli.ContainerStats(t.Context(), result.ServiceRefs["api"], client.ContainerStatsOptions{Stream: false, IncludePreviousSample: false})
	if err != nil {
		t.Fatal(err)
	}

	usage, decodeErr := decodeStats(stats.Body)

	closeErr = stats.Body.Close()
	if err := errors.Join(decodeErr, closeErr); err != nil {
		t.Fatal(err)
	}

	t.Logf("live one-shot usage=%+v", usage)

	for _, op := range []func(context.Context, id.ID) error{p.Stop, p.Start, p.Restart} {
		if err := op(t.Context(), iid); err != nil {
			t.Fatal(err)
		}
	}

	deployed, err := p.Deploy(t.Context(), provider.DeployRequest{InstanceID: iid, Services: []provider.ServiceDeploySpec{{Name: "api", Image: "alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc"}}})
	if err != nil || deployed.Status != "succeeded" {
		t.Fatalf("same-image deploy=%+v err=%v", deployed, err)
	}

	replacementContainers, replacementErr := p.listProjectContainers(t.Context(), iid)
	if replacementErr != nil {
		t.Fatal(replacementErr)
	}

	for _, c := range replacementContainers {
		t.Logf("owned replacement container=%s name=%s", c.ID, c.Name)
	}

	if err := p.Deprovision(t.Context(), iid); err != nil {
		t.Fatal(err)
	}

	remaining, err := p.listProjectContainers(t.Context(), iid)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("owned cleanup remaining=%v err=%v", remaining, err)
	}

	if _, err := cli.NetworkInspect(t.Context(), projectNetwork(iid), client.NetworkInspectOptions{}); !cerrdefs.IsNotFound(err) {
		t.Fatalf("owned network not removed: %v", err)
	}
}
