package managed

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
)

func schemaTwoCommand(originalSize, nativeSize int) PortCommand {
	command := PortCommand{SchemaVersion: "2", OperationID: id.New(PrefixOperation), RequestID: "exact-native-abort", Original: bytes.Repeat([]byte{0xff}, originalSize), Native: bytes.Repeat([]byte{0xff}, nativeSize)}
	command.OriginalDigest = Digest(command.Original)
	command.NativeDigest = Digest(command.Native)

	return command
}

func TestPortCommandSchemaTwoBounds(t *testing.T) {
	for _, test := range []struct {
		name     string
		schema   Decimal
		original int
		native   int
		valid    bool
	}{
		{"legacy boundary", "1", 4096, 4096, true},
		{"legacy native overflow", "1", 1, 4097, false},
		{"legacy original overflow", "1", 4097, 1, false},
		{"real abort size", "2", 251, 4700, true},
		{"combined boundary", "2", 251, 7941, true},
		{"combined overflow", "2", 251, 7942, false},
		{"native maximum", "2", 1, 8191, true},
		{"native overflow", "2", 1, 8192, false},
		{"balanced boundary", "2", 4096, 4096, true},
		{"original overflow", "2", 4097, 1, false},
		{"missing original", "2", 0, 8192, false},
		{"missing native", "2", 1, 0, false},
		{"unknown schema", "3", 1, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := schemaTwoCommand(test.original, test.native)
			command.SchemaVersion = test.schema

			if err := command.Validate(); (err == nil) != test.valid {
				t.Fatalf("valid=%v: %v", test.valid, err)
			}
		})
	}
}

func TestPortCommandSchemaTwoExactBytes(t *testing.T) {
	command := schemaTwoCommand(251, 4700)

	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}

	var decoded PortCommand
	if err = json.Unmarshal(raw, &decoded); err != nil || decoded.Validate() != nil || !reflect.DeepEqual(decoded, command) {
		t.Fatal("exact schema two round trip", err)
	}

	for _, field := range []string{"original", "native", "original digest", "native digest"} {
		t.Run(field, func(t *testing.T) {
			changed := schemaTwoCommand(251, 4700)

			switch field {
			case "original":
				changed.Original[0] ^= 1
			case "native":
				changed.Native[0] ^= 1
			case "original digest":
				changed.OriginalDigest = strings.Repeat("0", 64)
			case "native digest":
				changed.NativeDigest = strings.Repeat("0", 64)
			}

			if changed.Validate() == nil {
				t.Fatal("changed digest domain accepted")
			}
		})
	}
}

func TestPortCommandSchemaTwoAttemptRecordCapacity(t *testing.T) {
	for _, originalSize := range []int{1, 251, 4096} {
		command := schemaTwoCommand(originalSize, 8192-originalSize)
		command.RequestID = strings.Repeat("r", 256)
		binding := AttemptBindingKey{AttemptKey: AttemptKey{Key: Key{TenantID: strings.Repeat("t", 256), InstanceID: id.New("inst")}, AttemptID: id.New(PrefixOperation), Generation: "9223372036854775807"}, ReservationID: id.New(PrefixReservation)}
		digest := strings.Repeat("a", 64)
		record := AttemptCommandRecord{
			SchemaVersion: "1", Key: AttemptCommandKey{AttemptBindingKey: binding, Kind: AttemptAbort}, DescriptorDigest: digest,
			BindingReference:      AttemptBindingReference{ReservationID: binding.ReservationID, ReservationVersion: "9223372036854775807", OriginalRegistrationOperationID: id.New(PrefixOperation), OriginalRegistrationDigest: digest, ProcessDigest: digest},
			ExpectedNativeVersion: "9223372036854775807", NativeCommand: command, AuthorizationID: id.New(PrefixOperation), AuthorizationDigest: digest,
			IssuedAt: time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC), CurrentBindingDigest: digest,
		}

		raw, err := json.Marshal(record)
		if err != nil || command.Validate() != nil || len(raw) > MaxAttemptCommand {
			t.Fatalf("original=%d record=%d cap=%d: %v", originalSize, len(raw), MaxAttemptCommand, err)
		}

		request := AttemptCommandRequest{AttemptRequest: AttemptRequest{Command: Command{Key: binding.Key, ID: record.AuthorizationID, Revision: 1, LeaseEpoch: 1, Owner: "owner"}, AttemptID: binding.AttemptID, Generation: binding.Generation}, ReservationID: binding.ReservationID, Kind: AttemptAbort, ExpectedNativeVersion: record.ExpectedNativeVersion, NativeCommand: command}

		requestRaw, marshalErr := json.Marshal(request)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}

		var snapshot AttemptCommandRequest
		if decodeErr := json.Unmarshal(requestRaw, &snapshot); decodeErr != nil || snapshot.NativeCommand.Validate() != nil || !reflect.DeepEqual(snapshot, request) {
			t.Fatal("recovery snapshot preserves exact schema two command", decodeErr)
		}

		t.Logf("original=%d native=%d record=%d unchanged_cap=%d", originalSize, len(command.Native), len(raw), MaxAttemptCommand)
	}
}
