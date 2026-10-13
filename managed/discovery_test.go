package managed

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/id"
)

func discoveryMaxReference(n int) WorkReference {
	key := Key{TenantID: strings.Repeat("t", 256), InstanceID: id.New(id.PrefixInstance)}
	action := id.New(PrefixOperation)

	return WorkReference{Schema: DiscoverySchema, Key: key, Ordinal: Decimal(strconv.Itoa(n)), Operation: OperationKey{Key: key, ActionID: action}, Kind: "action", ID: action, Digest: strings.Repeat("a", 64)}
}

func TestDiscoveryCanonicalCapsAndMaximumPage(t *testing.T) {
	ref := discoveryMaxReference(1)

	raw, err := EncodeWorkReference(ref)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeWorkReference(raw)
	if err != nil || decoded != ref {
		t.Fatal(decoded, err)
	}

	for _, bad := range [][]byte{append([]byte(" "), raw...), append(bytes.Clone(raw), []byte(" {}")...), bytes.Replace(raw, []byte(`"schema":`), []byte(`"schema":"duplicate","schema":`), 1), bytes.Repeat([]byte("x"), MaxWorkReference+1)} {
		if _, err = DecodeWorkReference(bad); err == nil {
			t.Fatal("noncanonical/cap overflow accepted")
		}
	}

	page := WorkPage{Schema: DiscoverySchema, Key: ref.Key, Items: []WorkReference{}, NextCursor: strings.Repeat("c", MaxWorkCursor)}

	for n := 1; n <= MaxWorkPageItems; n++ {
		item := discoveryMaxReference(n)
		item.Key = ref.Key
		item.Operation.Key = ref.Key
		page.Items = append(page.Items, item)
	}

	encoded, err := EncodeWorkPage(page)
	if err != nil || len(encoded) > MaxWorkPage {
		t.Fatal(len(encoded), err)
	}

	t.Logf("maximum-width reference=%d page=%d", len(raw), len(encoded))

	page.Items = append(page.Items, discoveryMaxReference(33))
	if _, err = EncodeWorkPage(page); err == nil {
		t.Fatal("page count overflow accepted")
	}

	var value map[string]any
	if err = json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}

	value["ordinal"] = "9223372036854775808"

	overflow, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = DecodeWorkReference(overflow); err == nil {
		t.Fatal("signed sequence overflow accepted")
	}
}
