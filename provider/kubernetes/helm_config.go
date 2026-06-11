package kubernetes

import (
	"fmt"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	memorycache "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/xraph/ctrlplane/provider"
)

// helmDriverSecret stores Helm releases as Kubernetes Secrets in the
// release namespace — the default Helm 3 storage backend. Releases live in
// the cluster, never in ctrlplane's store.
const helmDriverSecret = "secret"

// helmRESTClientGetter adapts an existing rest.Config (plus discovery and
// RESTMapper) to the genericclioptions.RESTClientGetter that the Helm SDK
// expects, so the engine reuses the provider's cluster connection rather
// than re-loading a kubeconfig.
type helmRESTClientGetter struct {
	restConfig *rest.Config
	discovery  discovery.CachedDiscoveryInterface
	mapper     meta.RESTMapper
	namespace  string
}

// ToRESTConfig returns the underlying REST config.
func (g *helmRESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	return g.restConfig, nil
}

// ToDiscoveryClient returns a cached discovery client.
func (g *helmRESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return g.discovery, nil
}

// ToRESTMapper returns the REST mapper.
func (g *helmRESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	return g.mapper, nil
}

// ToRawKubeConfigLoader returns a minimal client config carrying the target
// namespace, used by Helm for namespace defaulting.
func (g *helmRESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	overrides := &clientcmd.ConfigOverrides{
		Context: clientcmdapi.Context{Namespace: g.namespace},
	}

	return clientcmd.NewDefaultClientConfig(*clientcmdapi.NewConfig(), overrides)
}

// defaultHelmConfig builds a cluster-backed action.Configuration for a
// namespace using the secret storage driver. This path requires a live
// cluster and is exercised by integration tests, not unit tests.
func (p *Provider) defaultHelmConfig(namespace string) (*action.Configuration, error) {
	getter := &helmRESTClientGetter{
		restConfig: p.restConfig,
		discovery:  memorycache.NewMemCacheClient(p.client.Discovery()),
		mapper:     p.mapper,
		namespace:  namespace,
	}

	cfg := new(action.Configuration)
	if err := cfg.Init(getter, namespace, helmDriverSecret, func(string, ...any) {}); err != nil {
		return nil, fmt.Errorf("helm config init: %w", err)
	}

	return cfg, nil
}

// defaultLoadChart locates and loads a chart from an https chart repo (or a
// local path when Repo is empty). OCI registries need additional client
// setup and are a follow-up. Network-dependent; integration-exercised.
func defaultLoadChart(src provider.RenderedHelm) (*chart.Chart, error) {
	cpo := action.ChartPathOptions{RepoURL: src.Repo, Version: src.Version}

	path, err := cpo.LocateChart(src.Chart, cli.New())
	if err != nil {
		return nil, fmt.Errorf("locate chart %q: %w", src.Chart, err)
	}

	ch, err := loader.Load(path)
	if err != nil {
		return nil, fmt.Errorf("load chart %q: %w", path, err)
	}

	return ch, nil
}
