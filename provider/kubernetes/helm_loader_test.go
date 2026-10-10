package kubernetes

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/provenance"
	"helm.sh/helm/v3/pkg/repo"

	"github.com/xraph/ctrlplane/provider"
)

func savedLoaderChart(t *testing.T, dir, version string) string {
	t.Helper()

	path, err := chartutil.Save(&chart.Chart{Metadata: &chart.Metadata{APIVersion: "v2", Name: "task3-loader", Version: version}, Templates: []*chart.File{{Name: "templates/configmap.yaml", Data: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: task3\n")}}}, dir)
	if err != nil {
		t.Fatal(err)
	}

	return path
}

func TestDefaultHelmLoaderLocalAndRepositoryVersion(t *testing.T) {
	dir := t.TempDir()
	cache := t.TempDir()
	t.Setenv("HELM_REPOSITORY_CACHE", cache)
	t.Setenv("HELM_REPOSITORY_CONFIG", filepath.Join(t.TempDir(), "repositories.yaml"))
	first := savedLoaderChart(t, dir, "1.0.0")
	savedLoaderChart(t, dir, "2.0.0")

	ch, err := defaultLoadChart(provider.RenderedHelm{Chart: first})
	if err != nil || ch.Metadata.Version != "1.0.0" || len(ch.Templates) != 1 {
		t.Fatalf("local chart=%v err=%v", ch, err)
	}

	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(srv.Close)

	index, err := repo.IndexDirectory(dir, srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	if err := index.WriteFile(filepath.Join(dir, "index.yaml"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, version := range []string{"1.0.0", "2.0.0"} {
		ch, err := defaultLoadChart(provider.RenderedHelm{Repo: srv.URL, Chart: "task3-loader", Version: version})
		if err != nil || ch.Metadata.Version != version {
			t.Fatalf("repo version=%s chart=%v err=%v", version, ch, err)
		}

		cached := filepath.Join(cache, "task3-loader-"+version+".tgz")
		if _, err := os.Stat(cached); err != nil {
			t.Fatalf("missing downloaded archive: %v", err)
		}
		// A cached archive is still accepted through the provider's local path.
		local, err := defaultLoadChart(provider.RenderedHelm{Chart: cached})
		if err != nil || local.Metadata.Version != version {
			t.Fatalf("cached local chart=%v err=%v", local, err)
		}
	}

	if _, err := defaultLoadChart(provider.RenderedHelm{Repo: srv.URL, Chart: "task3-loader", Version: "9.0.0"}); err == nil {
		t.Fatal("missing repository version accepted")
	}

	if _, err := defaultLoadChart(provider.RenderedHelm{Chart: filepath.Join(dir, "missing.tgz")}); err == nil {
		t.Fatal("missing local chart accepted")
	}
}

// TestUpstreamHelmLoaderProvenance checks upstream Verify/Keyring behavior.
// The public provider source contract does not expose that verification policy.
func TestUpstreamHelmLoaderProvenance(t *testing.T) {
	dir := t.TempDir()
	path := savedLoaderChart(t, dir, "1.0.0")

	entity, err := openpgp.NewEntity("Task3 fixture", "", "fixture@example.invalid", &packet.Config{RSABits: 2048})
	if err != nil {
		t.Fatal(err)
	}

	keyring := filepath.Join(dir, "keyring.gpg")

	f, err := os.Create(keyring)
	if err != nil {
		t.Fatal(err)
	}

	if err := entity.Serialize(f); err != nil {
		if closeErr := f.Close(); closeErr != nil {
			t.Error(closeErr)
		}

		t.Fatal(err)
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	signer := provenance.Signatory{Entity: entity, KeyRing: openpgp.EntityList{entity}}

	signature, err := signer.ClearSign(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path+".prov", []byte(signature), 0o600); err != nil {
		t.Fatal(err)
	}

	cpo := action.ChartPathOptions{Verify: true, Keyring: keyring}

	located, err := cpo.LocateChart(path, cli.New())
	if err != nil {
		t.Fatalf("valid signed chart rejected: %v", err)
	}

	if _, err := loader.Load(located); err != nil {
		t.Fatal(err)
	}
	// Add trailing bytes to change the archive checksum without signing it again.
	archive, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, append(archive, []byte("tampered")...), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := cpo.LocateChart(path, cli.New()); err == nil {
		t.Fatal("tampered chart accepted")
	}

	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(path + ".prov"); err != nil {
		t.Fatal(err)
	}

	if _, err := cpo.LocateChart(path, cli.New()); err == nil {
		t.Fatal("missing provenance accepted")
	}
}
