package managed

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
)

func writerFloorFixture(t *testing.T) WriterFloor {
	t.Helper()

	floor, err := NewWriterFloor(WriterFloorActivation{Schema: WriterFloorSchema, AuthorityID: "authority-one", MinimumProtocol: "1", Epoch: "1", ActivationID: id.New(PrefixOperation)}, time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	return floor
}

func TestWriterFloorCanonicalCodecs(t *testing.T) {
	floor := writerFloorFixture(t)

	raw, err := EncodeWriterFloor(floor)
	if err != nil {
		t.Fatal(err)
	}

	got, err := DecodeWriterFloor(raw)
	if err != nil || got != floor {
		t.Fatal("canonical round trip", got, err)
	}

	requestRaw, err := EncodeWriterFloorActivation(floor.Activation())
	if err != nil {
		t.Fatal(err)
	}

	request, err := DecodeWriterFloorActivation(requestRaw)
	if err != nil || request != floor.Activation() {
		t.Fatal("request round trip", err)
	}

	for name, mutation := range map[string]func([]byte) []byte{
		"trailing":        func(raw []byte) []byte { return append(bytes.Clone(raw), []byte(" {}")...) },
		"space":           func(raw []byte) []byte { return append([]byte(" "), raw...) },
		"duplicate":       func(raw []byte) []byte { return append([]byte(`{"schema":"`+WriterFloorSchema+`",`), raw[1:]...) },
		"unknown":         func(raw []byte) []byte { return append([]byte(`{"allow":true,`), raw[1:]...) },
		"numeric_counter": func(raw []byte) []byte { return bytes.Replace(raw, []byte(`"epoch":"1"`), []byte(`"epoch":1`), 1) },
		"cap_plus_one":    func([]byte) []byte { return bytes.Repeat([]byte(" "), MaxWriterFloor+1) },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeWriterFloor(mutation(raw)); err == nil {
				t.Fatal("invalid record accepted")
			}

			if _, err := DecodeWriterFloorActivation(mutation(requestRaw)); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}

	changed := floor

	changed.ActivatedAt = changed.ActivatedAt.Add(time.Second)
	if _, err = EncodeWriterFloor(changed); !errors.Is(err, ErrInvalid) {
		t.Fatal("timestamp outside digest", err)
	}

	changed = floor

	changed.ActivationDigest = strings.Repeat("0", 64)
	if _, err = EncodeWriterFloor(changed); !errors.Is(err, ErrInvalid) {
		t.Fatal("request hash outside validation", err)
	}
}

func TestWriterFloorCountersAndWidths(t *testing.T) {
	request := writerFloorFixture(t).Activation()

	for _, bad := range []Decimal{"", "0", "01", "+1", "-1", "1.0", "9223372036854775808", "18446744073709551615"} {
		for _, field := range []string{"epoch", "protocol"} {
			q := request
			if field == "epoch" {
				q.Epoch = bad
			} else {
				q.MinimumProtocol = bad
			}

			if _, err := WriterFloorActivationDigest(q); err == nil {
				t.Fatal("invalid counter", field, bad)
			}
		}
	}

	request.AuthorityID = strings.Repeat("a", 256)
	request.MinimumProtocol = "9223372036854775807"
	request.Epoch = "9223372036854775807"
	request.ExpectedDigest = strings.Repeat("f", 64)

	floor, err := NewWriterFloor(request, time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC))
	if err != nil {
		t.Fatal("joint maximum field widths", err)
	}

	raw, err := EncodeWriterFloor(floor)
	if err != nil || len(raw) > MaxWriterFloor {
		t.Fatal("maximum encoded record", len(raw), err)
	}

	request.AuthorityID += "a"
	if _, err = WriterFloorActivationDigest(request); !errors.Is(err, ErrInvalid) {
		t.Fatal("authority width", err)
	}

	request = writerFloorFixture(t).Activation()
	if _, err = NewWriterFloor(request, time.Now().In(time.FixedZone("offset", 3600))); !errors.Is(err, ErrInvalid) {
		t.Fatal("non UTC", err)
	}

	request.Schema = "ctrlplane.managed.writer-floor.v99"
	if _, err = WriterFloorActivationDigest(request); !errors.Is(err, ErrUnsupported) {
		t.Fatal("unknown schema", err)
	}
}

func TestWriterFloorExternalRestoreComparison(t *testing.T) {
	first := writerFloorFixture(t)
	q := first.Activation()
	q.Epoch, q.ExpectedDigest, q.ActivationID = "2", first.Digest, id.New(PrefixOperation)

	second, err := NewWriterFloor(q, first.ActivatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	if err = MatchWriterFloor(first, second.Reference()); !errors.Is(err, ErrAuthority) {
		t.Fatal("pre-activation restore matched newer external epoch", err)
	}

	if err = MatchWriterFloor(second, second.Reference()); err != nil {
		t.Fatal(err)
	}

	wrong := second.Reference()

	wrong.AuthorityID = "another-authority"
	if err = MatchWriterFloor(second, wrong); !errors.Is(err, ErrAuthority) {
		t.Fatal("cross authority", err)
	}
	// Equal activation is necessary only. A same-epoch snapshot missing a later effect
	// still needs external delegation/authority/native reconciliation before reopening.
}
