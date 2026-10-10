package docker

import (
	"encoding/json"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/network"

	"github.com/xraph/ctrlplane/provider"
)

func TestPortConfigCompatibility(t *testing.T) {
	tests := []struct {
		name      string
		spec      provider.PortSpec
		key, host string
		bad       bool
	}{
		{name: "default TCP ephemeral", spec: provider.PortSpec{Container: 8080}, key: "8080/tcp"},
		{name: "fixed TCP", spec: provider.PortSpec{Container: 80, Host: 8080}, key: "80/tcp", host: "8080"},
		{name: "UDP", spec: provider.PortSpec{Container: 53, Host: 5353, Protocol: "udp"}, key: "53/udp", host: "5353"},
		{name: "zero container retained", spec: provider.PortSpec{}, key: "0/tcp"},
		{name: "negative host retained", spec: provider.PortSpec{Container: 80, Host: -1}, key: "80/tcp"},
		{name: "unvalidated protocol retained", spec: provider.PortSpec{Container: 80, Protocol: "other"}, key: "80/other"},
		{name: "negative container", spec: provider.PortSpec{Container: -1}, bad: true},
		{name: "overflow container", spec: provider.PortSpec{Container: 65536}, bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exposed, bindings, err := buildPortConfig([]provider.PortSpec{tt.spec})
			if tt.bad {
				if err == nil || exposed != nil || bindings != nil {
					t.Fatalf("invalid conversion: %v %v %v", exposed, bindings, err)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			key := network.MustParsePort(tt.key)
			if _, ok := exposed[key]; !ok {
				t.Fatalf("missing exposed %s", key)
			}

			want := []network.PortBinding{{HostIP: netip.AddrFrom4([4]byte{}), HostPort: tt.host}}
			if !reflect.DeepEqual(bindings[key], want) {
				t.Fatalf("bindings=%v want=%v", bindings[key], want)
			}

			data, err := json.Marshal(bindings)
			if err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(string(data), `"HostIp":"0.0.0.0"`) {
				t.Fatalf("IPv4 binding wire shape: %s", data)
			}
		})
	}

	exposed, bindings, err := buildPortConfig([]provider.PortSpec{{Container: 80, Host: 8000}, {Container: 80, Host: 9000}})
	if err != nil || len(exposed) != 1 || bindings[network.MustParsePort("80/tcp")][0].HostPort != "9000" {
		t.Fatalf("duplicate declaration behavior: %v %v %v", exposed, bindings, err)
	}
}

func TestEndpointsPreservePrivateDNSAndDeduplicateBindings(t *testing.T) {
	ports := network.PortMap{
		network.MustParsePort("8080/tcp"): {{HostIP: netip.MustParseAddr("0.0.0.0"), HostPort: "32000"}, {HostIP: netip.IPv6Unspecified(), HostPort: "32000"}, {HostPort: ""}, {HostPort: "32001"}},
		network.MustParsePort("53/udp"):   {{HostPort: "5353"}},
	}
	got := endpointsFromInspect("project-api", ports)

	want := []provider.Endpoint{{URL: "http://project-api:8080", Port: 8080, Protocol: "http"}, {URL: "http://localhost:32000", Port: 32000, Protocol: "http", Public: true}, {URL: "http://localhost:32001", Port: 32001, Protocol: "http", Public: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("endpoints=%+v want=%+v", got, want)
	}
}
