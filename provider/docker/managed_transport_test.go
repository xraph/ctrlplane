//go:build linux || darwin

package docker

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

type custodyStartupFixture struct{}

func (*custodyStartupFixture) ResolveManagedDockerStartup(_ context.Context, _ managed.ProvisionService) (ManagedDockerStartup, error) {
	return ManagedDockerStartup{Entrypoint: []string{"/pinned-worker"}, Arguments: []string{"serve"}, Environment: []string{"PRIVATE_BOOTSTRAP=fixture-secret"}}, nil
}

func TestManagedDockerSDKCreationAndRemovalRetainExactIDs(t *testing.T) {
	var (
		mu    sync.Mutex
		paths []string
	)

	actualID := strings.Repeat("a", 64)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()

		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/networks/create"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"Id":"` + actualID + `"}`))
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/networks/"+actualID):
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected SDK request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	transport, err := NewSDKManagedDockerTransport("tcp://"+server.Listener.Addr().String(), "1.52", &http.Transport{}, &custodyStartupFixture{})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()

	journal, directory := custodyJournalFixture(t)
	authority := &custodyAuthorityFixture{requests: map[string]ManagedDockerRequest{}}
	create := custodyRequestFixture(managed.StepCreateNetwork)
	authority.trust(create)
	executor := custodyExecutorFixture(t, journal, transport, authority)

	created, err := executor.Execute(t.Context(), create)
	if err != nil || created.Response == nil || created.Response.ResourceID != actualID {
		t.Fatal("actual create ID missing", err)
	}

	reopened, err := OpenFileManagedDockerJournal(directory)
	if err != nil {
		t.Fatal(err)
	}

	executor = custodyExecutorFixture(t, reopened, transport, authority)
	if _, err = executor.Execute(t.Context(), create); err != nil {
		t.Fatal(err)
	}

	remove := custodyRequestFixture(managed.StepRemoveNetwork)
	authority.trust(remove)

	removed, err := executor.Execute(t.Context(), remove)
	if err != nil || removed.Response == nil || removed.Response.ResourceID != actualID {
		t.Fatal("exact removal failed", err)
	}

	if _, err = executor.Execute(t.Context(), remove); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(paths) != 2 {
		t.Fatalf("mutations replayed: %v", paths)
	}
}

func TestManagedDockerSDKContainerFixedInputsAndNoImplicitStart(t *testing.T) {
	var (
		creates    atomic.Int32
		imageReads atomic.Int32
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/") {
			imageReads.Add(1)

			_, _ = w.Write([]byte(`{"Id":"sha256:` + strings.Repeat("f", 64) + `"}`))

			return
		}

		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/containers/create") {
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		creates.Add(1)

		var body struct {
			Image      string   `json:"Image"`
			Entrypoint []string `json:"Entrypoint"`
			HostConfig struct {
				NetworkMode   string `json:"NetworkMode"`
				NanoCpus      int64  `json:"NanoCpus"`
				Memory        int64  `json:"Memory"`
				PidsLimit     int64  `json:"PidsLimit"`
				RestartPolicy struct {
					Name string `json:"Name"`
				} `json:"RestartPolicy"`
			} `json:"HostConfig"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}

		if body.Image != "example/worker@sha256:"+strings.Repeat("f", 64) || body.HostConfig.NetworkMode != strings.Repeat("e", 64) || body.HostConfig.NanoCpus != 100000000 || body.HostConfig.Memory != 64*1048576 || body.HostConfig.PidsLimit != 32 || body.HostConfig.RestartPolicy.Name != "no" || len(body.Entrypoint) != 1 {
			t.Errorf("wrong fixed inputs: %+v", body)
		}

		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"` + strings.Repeat("a", 64) + `"}`))
	}))
	defer server.Close()

	transport, err := NewSDKManagedDockerTransport("tcp://"+server.Listener.Addr().String(), "1.52", &http.Transport{}, &custodyStartupFixture{})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()

	journal, directory := custodyJournalFixture(t)
	authority := &custodyAuthorityFixture{requests: map[string]ManagedDockerRequest{}}
	request := custodyRequestFixture(managed.StepCreateContainer)
	authority.trust(request)

	record, err := custodyExecutorFixture(t, journal, transport, authority).Execute(t.Context(), request)
	if err != nil || record.State != "complete" {
		t.Fatal(err)
	}

	if creates.Load() != 1 || imageReads.Load() != 1 {
		t.Fatalf("creates=%d image reads=%d", creates.Load(), imageReads.Load())
	}

	raw, err := journal.Load(t.Context(), custodyKey(request.Permission))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(raw), "fixture-secret") {
		t.Fatalf("private startup leaked to %s", directory)
	}
}

func TestManagedDockerSDKNotFoundAndDroppedResponseNeverClose(t *testing.T) {
	for _, mode := range []string{"not_found", "response_lost", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)

				switch mode {
				case "not_found":
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"missing"}`))
				case "redirect":
					w.Header().Set("Location", "/must-not-follow")
					w.WriteHeader(http.StatusTemporaryRedirect)
				case "response_lost":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)

						return
					}

					_ = conn.Close()
				}
			}))
			defer server.Close()

			transport, err := NewSDKManagedDockerTransport("tcp://"+server.Listener.Addr().String(), "1.52", &http.Transport{}, &custodyStartupFixture{})
			if err != nil {
				t.Fatal(err)
			}
			defer transport.Close()

			journal, _ := custodyJournalFixture(t)
			authority := &custodyAuthorityFixture{requests: map[string]ManagedDockerRequest{}}
			request := custodyRequestFixture(managed.StepRemoveContainer)
			authority.trust(request)
			e := custodyExecutorFixture(t, journal, transport, authority)

			result, err := e.Execute(t.Context(), request)
			if err == nil || result.State != "delegated" {
				t.Fatal("uncertainty lost", err)
			}

			if _, err = e.Execute(t.Context(), request); err == nil {
				t.Fatal("uncertainty replay authorized")
			}

			if _, err = e.WitnessManagedProviderNoDispatch(t.Context(), request.Invocation.Key); err == nil {
				t.Fatal("absence inferred no dispatch")
			}

			if calls.Load() != 1 {
				t.Fatalf("SDK retried %d requests", calls.Load())
			}
		})
	}
}

// The certificate fixture has a real CA and distinct server/client identities. It
// exercises the standard TLS transport, not a mocked RoundTripper or dialer.
func custodyTLSFixture(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()

	now := time.Now()

	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "custody-test-ca"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}

	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}

	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(ca)

	issue := func(serial int64, name string, usage x509.ExtKeyUsage) tls.Certificate {
		public, private, keyErr := ed25519.GenerateKey(rand.Reader)
		if keyErr != nil {
			t.Fatal(keyErr)
		}

		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		if usage == x509.ExtKeyUsageServerAuth {
			template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		}

		der, certErr := x509.CreateCertificate(rand.Reader, template, ca, public, caPrivate)
		if certErr != nil {
			t.Fatal(certErr)
		}

		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: private}
	}
	server := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{issue(2, "custody-server", x509.ExtKeyUsageServerAuth)}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	clientConfig := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, Certificates: []tls.Certificate{issue(3, "custody-client", x509.ExtKeyUsageClientAuth)}}

	return server, clientConfig
}

func TestManagedDockerSDKMutualTLSAuthenticatesExactEndpoint(t *testing.T) {
	serverTLS, clientTLS := custodyTLSFixture(t)

	var calls atomic.Int32

	actualID := strings.Repeat("a", 64)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 || r.TLS.PeerCertificates[0].Subject.CommonName != "custody-client" {
			t.Error("mutual TLS identity missing")
			w.WriteHeader(http.StatusForbidden)

			return
		}

		if r.Method != http.MethodPost || r.URL.Path != "/v1.52/networks/create" {
			t.Errorf("wrong endpoint %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"` + actualID + `"}`))
	}))
	server.TLS = serverTLS

	server.StartTLS()
	defer server.Close()

	command := ManagedDockerCommand{Kind: managed.StepCreateNetwork, Name: "mtls-fixture"}

	for _, mode := range []string{"authenticated", "missing_client", "untrusted_server"} {
		t.Run(mode, func(t *testing.T) {
			config := clientTLS.Clone()
			if mode == "missing_client" {
				config.Certificates = nil
			}

			if mode == "untrusted_server" {
				config.RootCAs = x509.NewCertPool()
			}

			transport, err := NewSDKManagedDockerTransport("tcp://"+server.Listener.Addr().String(), "1.52", &http.Transport{TLSClientConfig: config}, &custodyStartupFixture{})
			if err != nil {
				t.Fatal(err)
			}
			defer transport.Close()

			prior := calls.Load()

			result, executeErr := transport.Execute(t.Context(), command)
			if mode == "authenticated" {
				if executeErr != nil || result != actualID || calls.Load() != prior+1 {
					t.Fatalf("TLS mutation failed: id=%q err=%v calls=%d", result, executeErr, calls.Load())
				}
			} else if executeErr == nil || calls.Load() != prior {
				t.Fatalf("TLS refusal reached handler: err=%v calls=%d", executeErr, calls.Load())
			}
		})
	}
}

func TestManagedDockerSDKFailedTLSNeverFallsBackToPlaintext(t *testing.T) {
	var (
		calls       atomic.Int32
		connections atomic.Int32
	)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"` + strings.Repeat("a", 64) + `"}`))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}

	server.Start()
	defer server.Close()

	transport, err := NewSDKManagedDockerTransport("tcp://"+server.Listener.Addr().String(), "1.52", &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: x509.NewCertPool()}}, &custodyStartupFixture{})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()

	if _, err = transport.Execute(t.Context(), ManagedDockerCommand{Kind: managed.StepCreateNetwork, Name: "no-fallback"}); err == nil {
		t.Fatal("plaintext endpoint accepted TLS request")
	}

	if calls.Load() != 0 || connections.Load() != 1 {
		t.Fatalf("fallback/retry: handlers=%d connections=%d", calls.Load(), connections.Load())
	}
}

func TestManagedDockerSDKUnixSocketUsesConfiguredAuthority(t *testing.T) {
	// Keep the pathname short enough for Darwin's Unix socket limit.
	directory, err := os.MkdirTemp("", "custody-unix-") //nolint:usetesting // t.TempDir paths exceed Darwin Unix socket limits.
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)

	socket := filepath.Join(directory, "api.sock")

	listener, err := new(net.ListenConfig).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32

	actualID := strings.Repeat("b", 64)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		if r.Method != http.MethodPost || r.URL.Path != "/v1.52/networks/create" {
			t.Errorf("wrong Unix request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"` + actualID + `"}`))
	}))
	_ = server.Listener.Close()
	server.Listener = listener

	server.Start()
	defer server.Close()

	transport, err := NewSDKManagedDockerTransport("unix://"+socket, "1.52", &http.Transport{}, &custodyStartupFixture{})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()

	result, err := transport.Execute(t.Context(), ManagedDockerCommand{Kind: managed.StepCreateNetwork, Name: "unix-fixture"})
	if err != nil || result != actualID || calls.Load() != 1 {
		t.Fatalf("Unix authority failed: id=%q err=%v calls=%d", result, err, calls.Load())
	}
}

func TestManagedDockerSDKConflictingEndpointConfigurationRefused(t *testing.T) {
	cases := []struct {
		name, host string
		transport  *http.Transport
	}{
		{name: "unix_tls", host: "unix:///tmp/provider.sock", transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}},
		{name: "custom_dial", host: "tcp://127.0.0.1:2375", transport: &http.Transport{DialContext: (&net.Dialer{}).DialContext}},
		{name: "custom_tls_dial", host: "tcp://127.0.0.1:2376", transport: &http.Transport{DialTLSContext: (&net.Dialer{}).DialContext}},
		{name: "proxy", host: "tcp://127.0.0.1:2375", transport: &http.Transport{Proxy: http.ProxyFromEnvironment}},
		{name: "insecure_tls", host: "tcp://127.0.0.1:2376", transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}},
		{name: "wrong_tls_name", host: "tcp://127.0.0.1:2376", transport: &http.Transport{TLSClientConfig: &tls.Config{ServerName: "another-authority"}}},
		{name: "userinfo", host: "tcp://user@127.0.0.1:2375", transport: &http.Transport{}},
		{name: "path", host: "tcp://127.0.0.1:2375/other", transport: &http.Transport{}},
		{name: "query", host: "tcp://127.0.0.1:2375?other=true", transport: &http.Transport{}},
		{name: "fragment", host: "tcp://127.0.0.1:2375#other", transport: &http.Transport{}},
		{name: "missing_port", host: "tcp://127.0.0.1", transport: &http.Transport{}},
		{name: "zero_port", host: "tcp://127.0.0.1:0", transport: &http.Transport{}},
		{name: "noncanonical_socket", host: "unix:///tmp/../provider.sock", transport: &http.Transport{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if transport, err := NewSDKManagedDockerTransport(tc.host, "1.52", tc.transport, &custodyStartupFixture{}); err == nil {
				_ = transport.Close()

				t.Fatal("ambiguous authority accepted")
			}
		})
	}
}

func TestManagedDockerSDKPlainConfigurationDoesNotAcquireTLS(t *testing.T) {
	configuration := &http.Transport{}
	for range 2 {
		transport, err := NewSDKManagedDockerTransport("tcp://127.0.0.1:2375", "1.52", configuration, &custodyStartupFixture{})
		if err != nil {
			t.Fatal(err)
		}

		if configuration.TLSClientConfig != nil || transport.httpTransport.TLSClientConfig != nil {
			t.Fatal("plain endpoint acquired implicit TLS")
		}

		if err = transport.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
