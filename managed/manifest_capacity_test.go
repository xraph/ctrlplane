package managed_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

func maximalManifest(t *testing.T) managed.Identity {
	t.Helper()

	token := strings.Repeat("x", 256)
	digest := strings.Repeat("a", 64)
	identity := managed.Identity{Key: managed.Key{TenantID: token, InstanceID: id.New(id.PrefixInstance)}, ProvisionOperationID: id.New(managed.PrefixOperation), Physical: managed.Physical{Provider: token, Placement: token, Project: token, Generation: token}, Manifest: managed.Manifest{Version: math.MaxInt64}}
	provision := managed.ProvisionSpec{Version: "1"}
	roles := []managed.RuntimeRole{}

	for n := range 32 {
		name := strings.Repeat("s", 253) + fmt.Sprintf("%03d", n)
		identity.Manifest.Members = append(identity.Manifest.Members, managed.Member{Name: name, Service: name, OwnerID: id.New(id.PrefixWorkload), Installation: token, Namespace: token, Build: token, Queue: token, ArtifactDigest: digest, ConfigurationDigest: digest})

		role := "sidecar"
		if n == 0 {
			role = "main"
		}

		provision.Services = append(provision.Services, managed.ProvisionService{Name: name, Image: strings.Repeat("x", 184) + "@sha256:" + digest, Role: role, CPUMillis: managed.Decimal(strconv.FormatInt(math.MaxInt64/1000000, 10)), MemoryMiB: managed.Decimal(strconv.FormatInt(math.MaxInt64/1048576, 10)), PIDLimit: "9223372036854775807", Startup: managed.StartupBindingRef{BindingID: token, BindingGeneration: "9223372036854775807", BindingDigest: digest}})
		roles = append(roles, managed.Polling)
	}

	raw, err := json.Marshal(provision)
	if err != nil {
		t.Fatal(err)
	}

	identity.Manifest.Spec, err = json.Marshal(managed.ManagedSpec{Profile: "ctrlplane.managed.v1", RuntimeRoles: roles, Provision: raw})
	if err != nil {
		t.Fatal(err)
	}

	identity.Manifest.SpecDigest = managed.Digest(identity.Manifest.Spec)

	return identity
}
func TestFixedManifestCapacity(t *testing.T) {
	identity := maximalManifest(t)
	if _, err := managed.ParseProvisionSpec(identity.Manifest); err != nil {
		t.Fatal("maximal jointly valid spec", err)
	}

	t.Logf("actual maximal 32-service/32-member fixed spec=%d", len(identity.Manifest.Spec))

	before, err := json.Marshal(managed.IdentityWire(identity))
	if err != nil {
		t.Fatal(err)
	}

	local, err := managed.IdentityWire(identity).Value()
	if err != nil || !reflect.DeepEqual(local, identity) {
		t.Fatal("maximal identity wire roundtrip", err)
	}

	after, err := json.Marshal(managed.IdentityWire(local))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("wire bytes changed", err)
	}

	boundary := identity
	boundary.Manifest.Spec = append(bytes.Clone(identity.Manifest.Spec), bytes.Repeat([]byte(" "), managed.MaxManifestSpec-len(identity.Manifest.Spec))...)

	boundary.Manifest.SpecDigest = managed.Digest(boundary.Manifest.Spec)
	if _, err = managed.ParseProvisionSpec(boundary.Manifest); err != nil {
		t.Fatal("exact manifest bound", err)
	}

	if _, err = managed.IdentityWire(boundary).Value(); err != nil {
		t.Fatal("wire exact manifest bound", err)
	}

	overflow := boundary
	overflow.Manifest.Spec = append(bytes.Clone(boundary.Manifest.Spec), ' ')

	overflow.Manifest.SpecDigest = managed.Digest(overflow.Manifest.Spec)
	if _, err = managed.ParseProvisionSpec(overflow.Manifest); err == nil {
		t.Fatal("one-byte manifest overflow accepted")
	}

	if _, err = managed.IdentityWire(overflow).Value(); err == nil {
		t.Fatal("wire one-byte manifest overflow accepted")
	}

	for _, raw := range [][]byte{bytes.Repeat([]byte("a"), 4097), append([]byte(`{"profile":"legacy"}`), bytes.Repeat([]byte(" "), 4097)...), append([]byte(`{"profile":"ctrlplane.managed.v1","unknown":1}`), bytes.Repeat([]byte(" "), 4097)...)} {
		invalid := identity
		invalid.Manifest.Spec = raw

		invalid.Manifest.SpecDigest = managed.Digest(raw)
		if _, err = managed.IdentityWire(invalid).Value(); err == nil {
			t.Fatal("oversized legacy or malformed fixed profile accepted")
		}
	}

	command := managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: "request", Original: bytes.Repeat([]byte("a"), 4097), Native: []byte("native")}
	command.OriginalDigest = managed.Digest(command.Original)

	command.NativeDigest = managed.Digest(command.Native)
	if err = command.Validate(); err == nil {
		t.Fatal("manifest capacity expanded original native command")
	}

	t.Logf("unchanged native evidence bound=%d; containing maximum wire identity=%d; padded boundary wire identity measured separately", managed.MaxEvidence, len(before))
}
