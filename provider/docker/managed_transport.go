package docker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/xraph/ctrlplane/managed"
)

// ManagedDockerStartup is private fixed startup material. It is never journaled or
// emitted as evidence. The configured resolver authenticates the complete descriptor.
type ManagedDockerStartup struct {
	Entrypoint  []string `json:"entrypoint"`
	Arguments   []string `json:"arguments"`
	Environment []string `json:"environment"`
}

// ManagedDockerStartupResolver is read-only trusted host configuration. It must
// refuse stale enrollment, descriptor substitution or missing private startup inputs.
type ManagedDockerStartupResolver interface {
	ResolveManagedDockerStartup(ctx context.Context, service managed.ProvisionService) (ManagedDockerStartup, error)
}

// SDKManagedDockerTransport implements the fixed exact mutation profile. The client
// must be pinned by trusted configuration to the executor's backend authority. A raw
// client/socket is an authority-bearing dependency and must never be request supplied.
type SDKManagedDockerTransport struct {
	client        *client.Client
	httpTransport *http.Transport
	startup       ManagedDockerStartupResolver
}

// NewSDKManagedDockerTransport fixes the host/API version without environment lookup.
// The supplied transport carries trusted TLS credentials. Only canonical Unix sockets
// and explicit TCP host:port endpoints are supported. Custom dialers and proxies refuse;
// the adapter owns exact endpoint dialing. TLS uses verified HTTPS without fallback.
// Connection reuse, HTTP/2 and redirects are disabled to prevent transparent replay.
func NewSDKManagedDockerTransport(host, apiVersion string, transport *http.Transport, startup ManagedDockerStartupResolver) (*SDKManagedDockerTransport, error) {
	if apiVersion == "" || absent(startup) {
		return nil, managed.ErrUnsupported
	}

	owned, scheme, err := managedDockerHTTPTransport(host, transport)
	if err != nil {
		return nil, err
	}

	owned.DisableKeepAlives = true
	owned.ForceAttemptHTTP2 = false
	owned.Protocols = new(http.Protocols)
	owned.Protocols.SetHTTP1(true)

	owned.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	if owned.TLSClientConfig != nil {
		owned.TLSClientConfig.NextProtos = []string{"http/1.1"}
	}

	api, err := client.New(client.WithHost(host), client.WithScheme(scheme), client.WithAPIVersion(apiVersion), client.WithHTTPClient(&http.Client{Transport: managedDockerRoundTripper{base: owned}, CheckRedirect: func(*http.Request, []*http.Request) error { return managed.ErrUnavailable }}))
	if err != nil {
		return nil, err
	}

	return &SDKManagedDockerTransport{client: api, httpTransport: owned, startup: startup}, nil
}

// Configure the owned transport before wrapping it. The SDK cannot infer TLS or
// configure Unix dialing through a RoundTripper wrapper, so both are explicit here.
func managedDockerHTTPTransport(host string, source *http.Transport) (*http.Transport, string, error) {
	//nolint:staticcheck // Reject deprecated dial hooks too; they must not bypass endpoint custody.
	if source == nil || source.Proxy != nil || source.Dial != nil || source.DialContext != nil || source.DialTLS != nil || source.DialTLSContext != nil {
		return nil, "", managed.ErrUnsupported
	}

	endpoint, err := url.Parse(host)
	if err != nil || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.Opaque != "" || endpoint.RawPath != "" {
		return nil, "", managed.ErrUnsupported
	}
	// Transport.Clone initializes HTTP/2 defaults and can create a TLS config on
	// its source. Copy only the supported settings so absent TLS stays absent,
	// including when the same configuration is used for another constructor.
	owned := &http.Transport{
		TLSHandshakeTimeout:    source.TLSHandshakeTimeout,
		ResponseHeaderTimeout:  source.ResponseHeaderTimeout,
		ExpectContinueTimeout:  source.ExpectContinueTimeout,
		MaxResponseHeaderBytes: source.MaxResponseHeaderBytes,
		DisableCompression:     source.DisableCompression,
		MaxConnsPerHost:        source.MaxConnsPerHost,
		WriteBufferSize:        source.WriteBufferSize,
		ReadBufferSize:         source.ReadBufferSize,
	}
	if source.TLSClientConfig != nil {
		owned.TLSClientConfig = source.TLSClientConfig.Clone()
	}

	dialer := &net.Dialer{}
	scheme := "http"

	switch endpoint.Scheme {
	case "unix":
		if endpoint.Host != "" || !filepath.IsAbs(endpoint.Path) || filepath.Clean(endpoint.Path) != endpoint.Path || source.TLSClientConfig != nil {
			return nil, "", managed.ErrUnsupported
		}

		socket := endpoint.Path
		owned.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		}
	case "tcp":
		if endpoint.Path != "" || endpoint.Hostname() == "" {
			return nil, "", managed.ErrUnsupported
		}

		_, port, splitErr := net.SplitHostPort(endpoint.Host)

		number, portErr := strconv.ParseUint(port, 10, 16)
		if splitErr != nil || portErr != nil || number == 0 || strconv.FormatUint(number, 10) != port {
			return nil, "", managed.ErrUnsupported
		}

		if owned.TLSClientConfig != nil {
			if owned.TLSClientConfig.InsecureSkipVerify || owned.TLSClientConfig.ServerName != "" && owned.TLSClientConfig.ServerName != endpoint.Hostname() {
				return nil, "", managed.ErrUnsupported
			}

			scheme = "https"
		}

		address := endpoint.Host
		owned.DialContext = func(ctx context.Context, network, addressRequested string) (net.Conn, error) {
			if network != "tcp" || addressRequested != address {
				return nil, managed.ErrAuthority
			}

			return dialer.DialContext(ctx, "tcp", address)
		}
	default:
		return nil, "", managed.ErrUnsupported
	}

	return owned, scheme, nil
}

type managedDockerRoundTripper struct{ base *http.Transport }

func (t managedDockerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return response, err
	}

	expected := http.StatusNoContent
	if request.Method == http.MethodPost && (strings.HasSuffix(request.URL.Path, "/networks/create") || strings.HasSuffix(request.URL.Path, "/containers/create")) {
		expected = http.StatusCreated
	}

	if request.Method != http.MethodGet && response.StatusCode != expected {
		_ = response.Body.Close()

		return nil, managed.ErrUnavailable
	}

	return response, nil
}
func (t managedDockerRoundTripper) CloseIdleConnections() { t.base.CloseIdleConnections() }

// Close releases this adapter's client connections without changing retained custody.
func (t *SDKManagedDockerTransport) Close() error {
	t.httpTransport.CloseIdleConnections()

	return t.client.Close()
}

// Execute issues one mutation and returns the actual create response ID or exact
// removal/start target ID. Docker 404 remains uncertain, never terminal success.
func (t *SDKManagedDockerTransport) Execute(ctx context.Context, command ManagedDockerCommand) (string, error) {
	command = custodyClone(command)
	if err := validateDockerCommand(command); err != nil {
		return "", err
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}

	switch command.Kind {
	case managed.StepCreateNetwork:
		result, err := t.client.NetworkCreate(ctx, command.Name, client.NetworkCreateOptions{Driver: "bridge", Scope: "local"})

		return result.ID, err
	case managed.StepCreateContainer:
		service := *command.Service

		startup, err := t.startup.ResolveManagedDockerStartup(ctx, service)
		if err != nil {
			return "", err
		}

		startup = custodyClone(startup)

		raw, err := json.Marshal(startup)
		if err != nil || len(raw) > 32768 || len(startup.Entrypoint) == 0 || len(startup.Entrypoint) > 64 || len(startup.Arguments) > 64 || len(startup.Environment) > 128 {
			return "", managed.ErrCapacity
		}
		// A missing pinned image is a refusal. Pulling would create an unjournaled effect.
		if _, err = t.client.ImageInspect(ctx, service.Image); err != nil {
			return "", err
		}

		cpu, _ := service.CPUMillis.Uint64()
		memory, _ := service.MemoryMiB.Uint64()

		pids, _ := service.PIDLimit.Uint64()
		if cpu > (1<<63-1)/1000000 || memory > (1<<63-1)/1048576 || pids > 1<<63-1 {
			return "", managed.ErrCapacity
		}

		pidLimit := int64(pids)
		result, err := t.client.ContainerCreate(ctx, client.ContainerCreateOptions{
			Name:             command.Name,
			Config:           &container.Config{Image: service.Image, Entrypoint: slices.Clone(startup.Entrypoint), Cmd: slices.Clone(startup.Arguments), Env: slices.Clone(startup.Environment)},
			HostConfig:       &container.HostConfig{NetworkMode: container.NetworkMode(command.NetworkID), RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled}, Resources: container.Resources{NanoCPUs: int64(cpu) * 1000000, Memory: int64(memory) * 1048576, PidsLimit: &pidLimit}},
			NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{command.NetworkID: {}}},
		})

		return result.ID, err
	case managed.StepStartContainer:
		_, err := t.client.ContainerStart(ctx, command.ResourceID, client.ContainerStartOptions{})

		return command.ResourceID, err
	case managed.StepRemoveContainer:
		_, err := t.client.ContainerRemove(ctx, command.ResourceID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: false})

		return command.ResourceID, err
	case managed.StepRemoveNetwork:
		_, err := t.client.NetworkRemove(ctx, command.ResourceID, client.NetworkRemoveOptions{})

		return command.ResourceID, err
	default:
		return "", managed.ErrUnsupported
	}
}
