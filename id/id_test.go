package id

import (
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	got := New(PrefixInstance)
	if got.IsNil() {
		t.Fatal("New() returned nil ID")
	}

	if got.Prefix() != PrefixInstance {
		t.Fatalf("New(PrefixInstance).Prefix() = %q, want %q", got.Prefix(), PrefixInstance)
	}
}

func TestNewUnique(t *testing.T) {
	a := New(PrefixInstance)
	b := New(PrefixInstance)

	if a == b {
		t.Fatalf("New() produced duplicate IDs: %s", a.String())
	}
}

func TestAllPrefixConstants(t *testing.T) {
	prefixes := []struct {
		name   string
		prefix Prefix
	}{
		{"Instance", PrefixInstance},
		{"Deployment", PrefixDeployment},
		{"Release", PrefixRelease},
		{"HealthCheck", PrefixHealthCheck},
		{"HealthResult", PrefixHealthResult},
		{"Domain", PrefixDomain},
		{"Route", PrefixRoute},
		{"Certificate", PrefixCertificate},
		{"Secret", PrefixSecret},
		{"Webhook", PrefixWebhook},
		{"WebhookDelivery", PrefixWebhookDelivery},
		{"Tenant", PrefixTenant},
		{"AuditEntry", PrefixAuditEntry},
		{"Event", PrefixEvent},
	}

	for _, tt := range prefixes {
		t.Run(tt.name, func(t *testing.T) {
			got := New(tt.prefix)

			if got.IsNil() {
				t.Fatal("New() returned nil ID")
			}

			if got.Prefix() != tt.prefix {
				t.Errorf("Prefix() = %q, want %q", got.Prefix(), tt.prefix)
			}

			wantPrefix := string(tt.prefix) + "_"
			if !strings.HasPrefix(got.String(), wantPrefix) {
				t.Errorf("String() = %q, want prefix %q", got.String(), wantPrefix)
			}
		})
	}
}

func TestParseRoundTrip(t *testing.T) {
	original := New(PrefixInstance)

	parsed, err := Parse(original.String())
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", original.String(), err)
	}

	if parsed != original {
		t.Fatalf("Parse round-trip: got %s, want %s", parsed, original)
	}
}

func TestParseInvalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"too short", "abc"},
		{"invalid chars", "!!!invalid!!!chars!!"},
		{"missing suffix", "inst_"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.input)
			if err == nil {
				t.Errorf("Parse(%q) expected error, got nil", tt.input)
			}
		})
	}
}

func TestParseWithPrefix(t *testing.T) {
	original := New(PrefixInstance)

	t.Run("matching prefix", func(t *testing.T) {
		parsed, err := ParseWithPrefix(original.String(), PrefixInstance)
		if err != nil {
			t.Fatalf("ParseWithPrefix() error = %v", err)
		}

		if parsed != original {
			t.Fatalf("ParseWithPrefix round-trip: got %s, want %s", parsed, original)
		}
	})

	t.Run("mismatched prefix", func(t *testing.T) {
		_, err := ParseWithPrefix(original.String(), PrefixDeployment)
		if err == nil {
			t.Fatal("ParseWithPrefix() expected error for mismatched prefix, got nil")
		}
	})
}

func TestNilIsZeroValue(t *testing.T) {
	var zero ID

	if Nil != zero {
		t.Fatal("Nil is not the zero value")
	}
}

func TestIsNil(t *testing.T) {
	if !Nil.IsNil() {
		t.Fatal("Nil.IsNil() = false, want true")
	}

	got := New(PrefixInstance)
	if got.IsNil() {
		t.Fatal("New(PrefixInstance).IsNil() = true, want false")
	}
}

func TestMarshalTextRoundTrip(t *testing.T) {
	original := New(PrefixDeployment)

	data, err := original.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText() error = %v", err)
	}

	var parsed ID

	err = parsed.UnmarshalText(data)
	if err != nil {
		t.Fatalf("UnmarshalText() error = %v", err)
	}

	if parsed != original {
		t.Fatalf("MarshalText round-trip: got %s, want %s", parsed, original)
	}
}

func TestMarshalTextNil(t *testing.T) {
	data, err := Nil.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText() error = %v", err)
	}

	if len(data) != 0 {
		t.Fatalf("MarshalText() for Nil = %q, want empty", string(data))
	}
}

func TestUnmarshalTextEmpty(t *testing.T) {
	var parsed ID

	err := parsed.UnmarshalText([]byte{})
	if err != nil {
		t.Fatalf("UnmarshalText(empty) error = %v", err)
	}

	if parsed != Nil {
		t.Fatalf("UnmarshalText(empty) = %v, want Nil", parsed)
	}
}

func TestValueScanRoundTrip(t *testing.T) {
	original := New(PrefixRelease)

	val, err := original.Value()
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}

	var scanned ID

	err = scanned.Scan(val)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if scanned != original {
		t.Fatalf("Value/Scan round-trip: got %s, want %s", scanned, original)
	}
}

func TestScanNil(t *testing.T) {
	var scanned ID

	err := scanned.Scan(nil)
	if err != nil {
		t.Fatalf("Scan(nil) error = %v", err)
	}

	if scanned != Nil {
		t.Fatalf("Scan(nil) = %v, want Nil", scanned)
	}
}

func TestValueNil(t *testing.T) {
	val, err := Nil.Value()
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}

	if val != nil {
		t.Fatalf("Nil.Value() = %v, want nil", val)
	}
}

func TestEquality(t *testing.T) {
	a := New(PrefixInstance)
	b := New(PrefixInstance)

	if a == b {
		t.Fatal("two different IDs should not be equal")
	}

	// Parse the same ID twice — should be equal.
	s := a.String()

	p1, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	p2, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if p1 != p2 {
		t.Fatal("two parses of the same string should be equal")
	}
}

func TestStringFormat(t *testing.T) {
	tests := []struct {
		name       string
		prefix     Prefix
		wantPrefix string
	}{
		{"instance", PrefixInstance, "inst_"},
		{"deployment", PrefixDeployment, "dep_"},
		{"tenant", PrefixTenant, "ten_"},
		{"event", PrefixEvent, "evt_"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New(tt.prefix)
			str := got.String()

			if !strings.HasPrefix(str, tt.wantPrefix) {
				t.Errorf("String() = %q, want prefix %q", str, tt.wantPrefix)
			}
		})
	}
}

func TestNilString(t *testing.T) {
	if s := Nil.String(); s != "" {
		t.Fatalf("Nil.String() = %q, want empty string", s)
	}
}

func TestNilPrefix(t *testing.T) {
	if p := Nil.Prefix(); p != "" {
		t.Fatalf("Nil.Prefix() = %q, want empty string", p)
	}
}
