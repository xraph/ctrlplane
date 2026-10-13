package managedstate

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

func TestAttemptIdentityDigestTracksExactContent(t *testing.T) {
	identity := managed.Identity{
		Key:                  managed.Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)},
		ProvisionOperationID: id.New(managed.PrefixOperation),
		Manifest:             managed.Manifest{Version: 1, Spec: []byte("opaque"), Members: []managed.Member{{Name: "member"}}},
	}

	changes := map[string]func(*managed.Identity){
		"tenant":        func(v *managed.Identity) { v.TenantID = "other" },
		"instance":      func(v *managed.Identity) { v.InstanceID = id.New(id.PrefixInstance) },
		"operation":     func(v *managed.Identity) { v.ProvisionOperationID = id.New(managed.PrefixOperation) },
		"physical":      func(v *managed.Identity) { v.Physical.Generation = "other" },
		"version":       func(v *managed.Identity) { v.Manifest.Version++ },
		"spec digest":   func(v *managed.Identity) { v.Manifest.SpecDigest = "other" },
		"spec bytes":    func(v *managed.Identity) { v.Manifest.Spec[0] = 'X' },
		"member":        func(v *managed.Identity) { v.Manifest.Members[0].Queue = "other" },
		"nil members":   func(v *managed.Identity) { v.Manifest.Members = nil },
		"empty members": func(v *managed.Identity) { v.Manifest.Members = []managed.Member{} },
		"nil spec":      func(v *managed.Identity) { v.Manifest.Spec = nil },
		"empty spec":    func(v *managed.Identity) { v.Manifest.Spec = []byte{} },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			reads := newAttemptCheckedReads(nil)

			first, err := originalIdentityDigest(t.Context(), reads, identity)
			if err != nil {
				t.Fatal(err)
			}

			changed := identity
			changed.Manifest.Spec = bytes.Clone(identity.Manifest.Spec)
			changed.Manifest.Members = slices.Clone(identity.Manifest.Members)
			change(&changed)

			want, err := managed.OriginalIdentityDigest(changed)
			if err != nil {
				t.Fatal(err)
			}

			got, err := originalIdentityDigest(t.Context(), reads, changed)
			if err != nil || got != want || got == first {
				t.Fatalf("changed identity: got=%s want=%s original=%s err=%v", got, want, first, err)
			}

			again, err := originalIdentityDigest(t.Context(), reads, identity)
			if err != nil || again != first {
				t.Fatal("caller mutation changed retained identity", err)
			}
		})
	}
}

func TestAttemptIdentityDigestNullAndEmpty(t *testing.T) {
	reads := newAttemptCheckedReads(nil)

	var identity managed.Identity

	for _, members := range [][]managed.Member{nil, {}, nil} {
		for _, spec := range [][]byte{nil, {}, nil} {
			identity.Manifest.Members = members
			identity.Manifest.Spec = spec

			want, err := managed.OriginalIdentityDigest(identity)
			if err != nil {
				t.Fatal(err)
			}

			got, err := originalIdentityDigest(t.Context(), reads, identity)
			if err != nil || got != want {
				t.Fatal("null and empty lost their wire identity", err)
			}
		}
	}
}

func TestAttemptIdentityDigestCancellationAndBounds(t *testing.T) {
	reads := newAttemptCheckedReads(nil)

	identity := managed.Identity{Manifest: managed.Manifest{Spec: []byte("original"), Members: []managed.Member{{Name: "original"}}}}
	if _, err := originalIdentityDigest(t.Context(), reads, identity); err != nil {
		t.Fatal(err)
	}

	identity.Manifest.Spec[0] = 'X'

	identity.Manifest.Members[0].Name = "changed"
	if string(reads.digestIdentity.Manifest.Spec) != "original" || reads.digestIdentity.Manifest.Members[0].Name != "original" {
		t.Fatal("cached identity borrows caller state")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := originalIdentityDigest(ctx, reads, *reads.digestIdentity); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled cache hit", err)
	}

	for _, oversized := range []managed.Manifest{
		{Spec: make([]byte, managed.MaxManifestSpec+1)},
		{Members: make([]managed.Member, managed.MaxMembers+1)},
	} {
		fresh := newAttemptCheckedReads(nil)
		if _, err := originalIdentityDigest(t.Context(), fresh, managed.Identity{Manifest: oversized}); err != nil || fresh.digestIdentity != nil {
			t.Fatal("oversized identity retained", err)
		}
	}
}
