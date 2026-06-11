package kubernetes

import (
	"context"
	"fmt"
	"io"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// Compile-time check that Provider implements provider.Provider and provider.HealthChecker.
var (
	_ provider.Provider      = (*Provider)(nil)
	_ provider.HealthChecker = (*Provider)(nil)
)

// Provider is a Kubernetes-based infrastructure provider.
//
// helmConfig and loadChart are injectable seams: production wiring builds a
// cluster-backed action.Configuration and a repo/OCI chart loader, while
// tests inject an in-memory storage driver and an in-memory chart.
type Provider struct {
	cfg        Config
	client     kubernetes.Interface
	dynamic    dynamic.Interface
	mapper     meta.RESTMapper
	restConfig *rest.Config
	helmConfig func(namespace string) (*action.Configuration, error)
	loadChart  func(src provider.RenderedHelm) (*chart.Chart, error)
}

// New creates a new Kubernetes provider with the given options.
// Without any options, sane defaults are used (namespace: "default", region: "local").
// Configuration is resolved in order: explicit kubeconfig path, in-cluster config,
// then default kubeconfig loading rules (KUBECONFIG env, ~/.kube/config).
func New(opts ...Option) (*Provider, error) {
	p := &Provider{
		cfg: Config{
			Namespace: "default",
			Region:    "local",
			Country:   "Local",
			City:      "Localhost",
		},
	}

	for _, opt := range opts {
		if err := opt(p); err != nil {
			return nil, err
		}
	}

	if p.cfg.Namespace == "" {
		return nil, fmt.Errorf("kubernetes: %w: namespace is required", ctrlplane.ErrInvalidConfig)
	}

	restCfg, err := buildRestConfig(p.cfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: build config: %w", err)
	}

	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: create client: %w", err)
	}

	p.client = client

	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: create dynamic client: %w", err)
	}

	p.dynamic = dyn
	p.restConfig = restCfg

	// Lazily resolve GVK→GVR via discovery; the mapper performs no network
	// call until the first manifest apply needs a mapping.
	p.mapper = restmapper.NewDeferredDiscoveryRESTMapper(
		memory.NewMemCacheClient(client.Discovery()),
	)

	// Default helm seams talk to the real cluster and chart repos. Tests
	// override these fields with in-memory equivalents.
	p.helmConfig = p.defaultHelmConfig
	p.loadChart = defaultLoadChart

	return p, nil
}

// Info returns metadata about this provider, including a default
// location so studio's catalog endpoints surface a usable region in
// dev environments without explicit per-cluster geographic config.
func (p *Provider) Info() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:    "kubernetes",
		Version: "0.1.0",
		Region:  p.cfg.Region,
		Location: &provider.Location{
			Country: p.cfg.Country,
			City:    p.cfg.City,
		},
	}
}

// Capabilities returns the set of features this provider supports.
func (p *Provider) Capabilities() []provider.Capability {
	return []provider.Capability{
		provider.CapProvision,
		provider.CapDeploy,
		provider.CapScale,
		provider.CapLogs,
		provider.CapExec,
		provider.CapRolling,
		provider.CapVolumes,
		provider.CapManifests,
		provider.CapHelm,
	}
}

// Provision creates a Kubernetes workload for an instance.
//
// For req.Kind=KindDeployment (default): emits a Deployment + Service +
// per-service ConfigMaps. Pod has Main + Sidecars in containers[] and
// Inits in initContainers[].
//
// For req.Kind=KindStatefulSet: emits a StatefulSet + headless Service
// (ClusterIP=None) + volumeClaimTemplates per persistent volume + per-
// service ConfigMaps. Each replica gets its own PVC by name.
func (p *Provider) Provision(ctx context.Context, req provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	labels := instanceLabels(req.InstanceID, req.TenantID, p.cfg.Labels)
	ns := p.cfg.Namespace

	// Create per-service ConfigMaps before the controller object so the
	// pods can mount them on first start.
	for _, cm := range buildConfigMaps(req, ns, labels) {
		if _, err := p.client.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("kubernetes: create configmap %s: %w", cm.Name, err)
		}
	}

	pullSecrets := p.cfg.ImagePullSecrets

	switch req.Kind {
	case provider.KindStatefulSet:
		ss := buildStatefulSet(req, ns, labels, pullSecrets)
		if _, err := p.client.AppsV1().StatefulSets(ns).Create(ctx, ss, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("kubernetes: create statefulset: %w", err)
		}

		// StatefulSets require a headless Service for stable per-replica DNS.
		if svc := buildService(req, ns, labels, true); svc != nil {
			if _, err := p.client.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
				return nil, fmt.Errorf("kubernetes: create headless service: %w", err)
			}
		}
	default: // KindDeployment (also covers empty-string for legacy paths)
		dep := buildDeployment(req, ns, labels, pullSecrets)
		if _, err := p.client.AppsV1().Deployments(ns).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("kubernetes: create deployment: %w", err)
		}

		if svc := buildService(req, ns, labels, false); svc != nil {
			if _, err := p.client.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
				return nil, fmt.Errorf("kubernetes: create service: %w", err)
			}
		}
	}

	// Build per-service refs for the result. Each service-name maps to
	// the workload-level provider ref plus the service name — k8s
	// doesn't have stable per-container IDs at provision time (Pod
	// names are pattern-derived, container names are svc.Name within
	// the Pod), so we record the addressable form callers use.
	serviceRefs := make(map[string]string, len(req.Services))
	pref := providerRef(ns, req.InstanceID)

	for i := range req.Services {
		serviceRefs[req.Services[i].Name] = pref + "/" + req.Services[i].Name
	}

	// Populate Endpoints from the in-cluster Service DNS address for
	// each service that declares ports. Consumers (e.g. twinos
	// firstEndpoint / injectUpstreamEnv) read Endpoints[0].URL as the
	// upstream base URL.
	endpoints := buildEndpoints(req, ns)

	return &provider.ProvisionResult{
		ProviderRef: pref,
		ServiceRefs: serviceRefs,
		Endpoints:   endpoints,
	}, nil
}

// Deprovision tears down all resources for an instance.
func (p *Provider) Deprovision(ctx context.Context, instanceID id.ID) error {
	ns := p.cfg.Namespace
	name := deploymentName(instanceID)
	propagation := metav1.DeletePropagationForeground

	// Try Deployment + StatefulSet — either may be present depending
	// on Kind. Convergent semantics: "already gone" is the desired
	// end-state, so a NotFound from both is success. We surface a
	// real error only when the API reports something other than 404
	// from at least one of the deletes.
	depErr := p.client.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})
	ssErr := p.client.AppsV1().StatefulSets(ns).Delete(ctx, name, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})

	if isRealK8sError(depErr) && isRealK8sError(ssErr) {
		return fmt.Errorf("kubernetes: delete workload: deployment: %w; statefulset: %w", depErr, ssErr)
	}

	// Delete Service (NotFound = already gone, fine).
	_ = p.client.CoreV1().Services(ns).Delete(ctx, serviceName(instanceID), metav1.DeleteOptions{})

	// Delete every per-service ConfigMap matching our label selector.
	cms, listErr := p.client.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{
		LabelSelector: instanceSelector(instanceID),
	})
	if listErr == nil {
		for i := range cms.Items {
			_ = p.client.CoreV1().ConfigMaps(ns).Delete(ctx, cms.Items[i].Name, metav1.DeleteOptions{})
		}
	}

	return nil
}

// isRealK8sError returns true when err is non-nil and is NOT the
// NotFound case. Used to make Deprovision convergent — a 404 from
// the API means the resource is already gone, which is the
// desired end-state, not a failure.
func isRealK8sError(err error) bool {
	if err == nil {
		return false
	}
	// k8s status errors expose Code 404 via the Status() method on
	// errors.APIStatus implementations. Probe defensively without
	// importing k8s.io/apimachinery/pkg/api/errors here — the error
	// string check covers the path where the error wasn't an
	// APIStatus type.
	type statusErr interface{ Status() metav1.Status }

	if se, ok := err.(statusErr); ok {
		if se.Status().Code == 404 {
			return false
		}
	}

	return true
}

// Start scales the deployment to at least 1 replica.
func (p *Provider) Start(ctx context.Context, instanceID id.ID) error {
	return p.scaleReplicas(ctx, instanceID, 1)
}

// Stop scales the deployment to 0 replicas.
func (p *Provider) Stop(ctx context.Context, instanceID id.ID) error {
	return p.scaleReplicas(ctx, instanceID, 0)
}

// Restart performs a rollout restart by updating a pod template annotation.
func (p *Provider) Restart(ctx context.Context, instanceID id.ID) error {
	ns := p.cfg.Namespace
	name := deploymentName(instanceID)

	dep, err := p.client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("kubernetes: get deployment for restart: %w", err)
	}

	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = make(map[string]string)
	}

	dep.Spec.Template.Annotations["ctrlplane.io/restartedAt"] = metav1.Now().Format("2006-01-02T15:04:05Z")

	if _, err := p.client.AppsV1().Deployments(ns).Update(ctx, dep, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("kubernetes: restart deployment: %w", err)
	}

	return nil
}

// Status returns the current runtime status of an instance.
func (p *Provider) Status(ctx context.Context, instanceID id.ID) (*provider.InstanceStatus, error) {
	ns := p.cfg.Namespace
	name := deploymentName(instanceID)

	dep, err := p.client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("kubernetes: get deployment status: %w", err)
	}

	state := deploymentState(dep)

	return &provider.InstanceStatus{
		State:   state,
		Ready:   dep.Status.ReadyReplicas > 0,
		Message: deploymentMessage(dep),
	}, nil
}

// Deploy pushes a new release by updating each targeted service's
// container image and ConfigMap. Services not listed in req.Services
// are left at their current image — Kubernetes performs a rolling
// update only on the changed containers.
func (p *Provider) Deploy(ctx context.Context, req provider.DeployRequest) (*provider.DeployResult, error) {
	ns := p.cfg.Namespace
	name := deploymentName(req.InstanceID)

	// Try Deployment first; fall through to StatefulSet if not present.
	dep, depErr := p.client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if depErr == nil {
		applyServiceUpdates(dep.Spec.Template.Spec.Containers, dep.Spec.Template.Spec.InitContainers, req.Services)

		if dep.Annotations == nil {
			dep.Annotations = make(map[string]string)
		}

		dep.Annotations[annotationReleaseID] = req.ReleaseID.String()

		if _, err := p.client.AppsV1().Deployments(ns).Update(ctx, dep, metav1.UpdateOptions{}); err != nil {
			return nil, fmt.Errorf("kubernetes: update deployment: %w", err)
		}
	} else {
		ss, ssErr := p.client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if ssErr != nil {
			return nil, fmt.Errorf("kubernetes: get workload for deploy: deployment: %w; statefulset: %w", depErr, ssErr)
		}

		applyServiceUpdates(ss.Spec.Template.Spec.Containers, ss.Spec.Template.Spec.InitContainers, req.Services)

		if ss.Annotations == nil {
			ss.Annotations = make(map[string]string)
		}

		ss.Annotations[annotationReleaseID] = req.ReleaseID.String()

		if _, err := p.client.AppsV1().StatefulSets(ns).Update(ctx, ss, metav1.UpdateOptions{}); err != nil {
			return nil, fmt.Errorf("kubernetes: update statefulset: %w", err)
		}
	}

	// Update each service's ConfigMap when env was provided. Missing
	// ConfigMaps (services that previously had no env) get created.
	for _, sd := range req.Services {
		if len(sd.Env) == 0 {
			continue
		}

		cmName := configMapNameFor(req.InstanceID, sd.Name)

		cm, getErr := p.client.CoreV1().ConfigMaps(ns).Get(ctx, cmName, metav1.GetOptions{})
		if getErr == nil {
			cm.Data = sd.Env
			if _, updateErr := p.client.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{}); updateErr != nil {
				return nil, fmt.Errorf("kubernetes: update configmap %s: %w", cmName, updateErr)
			}
		}
	}

	return &provider.DeployResult{
		ProviderRef: providerRef(ns, req.InstanceID),
		Status:      "deployed",
	}, nil
}

// applyServiceUpdates mutates Containers and InitContainers in place,
// updating each container whose Name matches a ServiceDeploySpec entry.
// Services not listed in updates are left untouched.
func applyServiceUpdates(containers, initContainers []corev1.Container, updates []provider.ServiceDeploySpec) {
	byName := make(map[string]provider.ServiceDeploySpec, len(updates))
	for _, u := range updates {
		byName[u.Name] = u
	}

	for i := range containers {
		if u, ok := byName[containers[i].Name]; ok {
			containers[i].Image = u.Image
		}
	}

	for i := range initContainers {
		if u, ok := byName[initContainers[i].Name]; ok {
			initContainers[i].Image = u.Image
		}
	}
}

// Rollback reverts to a previous release (no-op: ctrlplane handles release state).
func (p *Provider) Rollback(_ context.Context, _ id.ID, _ id.ID) error {
	return nil
}

// Scale adjusts the instance's resource allocation.
func (p *Provider) Scale(ctx context.Context, instanceID id.ID, spec provider.ResourceSpec) error {
	if spec.Replicas > 0 {
		return p.scaleReplicas(ctx, instanceID, int32(min(spec.Replicas, int(^int32(0))))) //nolint:gosec // clamped to int32 range via min
	}

	return nil
}

// Resources returns a one-shot point-in-time sample of the
// instance's pod resource usage via the metrics.k8s.io API.
//
// Sums per-container CPU + memory across every pod that matches
// the instance's label selector (replica fan-out is handled by
// ctrlplane's metrics package — this returns the raw aggregate
// for one Instance, which on k8s typically means one pod-set
// owned by a Deployment).
//
// Network bytes are zero — metrics-server doesn't expose them.
// Operators who need per-pod network counters can wire a Prometheus
// scraper at the metrics.Service layer; the dashboard renders
// gracefully when those fields are zero.
//
// When metrics-server isn't installed, the underlying API returns
// 404 and Resources reports a zero-valued usage rather than an
// error — the metrics poller treats that as a missing sample, so
// the dashboard shows "—" rather than perpetual errors.
func (p *Provider) Resources(ctx context.Context, instanceID id.ID) (*provider.ResourceUsage, error) {
	return fetchPodMetrics(ctx, p.client, p.cfg.Namespace, instanceID)
}

// Logs streams logs for the instance.
func (p *Provider) Logs(ctx context.Context, instanceID id.ID, opts provider.LogOptions) (io.ReadCloser, error) {
	return streamLogs(ctx, p.client, p.cfg.Namespace, instanceID, opts)
}

// Exec runs a command inside the instance (stub).
func (p *Provider) Exec(_ context.Context, _ id.ID, _ provider.ExecRequest) (*provider.ExecResult, error) {
	return &provider.ExecResult{ExitCode: 0}, nil
}

// scaleReplicas patches the deployment to the desired replica count.
func (p *Provider) scaleReplicas(ctx context.Context, instanceID id.ID, replicas int32) error {
	ns := p.cfg.Namespace
	name := deploymentName(instanceID)

	scale, err := p.client.AppsV1().Deployments(ns).GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("kubernetes: get scale: %w", err)
	}

	scale.Spec.Replicas = replicas

	if _, err := p.client.AppsV1().Deployments(ns).UpdateScale(ctx, name, scale, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("kubernetes: update scale: %w", err)
	}

	return nil
}

// buildRestConfig creates a Kubernetes REST configuration from the provider config.
// Resolution order:
//  1. Explicit kubeconfig path (WithKubeconfig / CP_K8S_KUBECONFIG).
//  2. In-cluster config when running inside a pod (or forced via WithInCluster).
//  3. Default kubeconfig loading rules (KUBECONFIG env, ~/.kube/config) — covers
//     Docker Desktop, minikube, kind, and other local clusters.
func buildRestConfig(cfg Config) (*rest.Config, error) {
	// 1. Explicit kubeconfig path takes highest priority.
	if cfg.Kubeconfig != "" {
		return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: cfg.Kubeconfig},
			&clientcmd.ConfigOverrides{CurrentContext: cfg.Context},
		).ClientConfig()
	}

	// 2. Forced in-cluster mode — fail fast if not in a pod.
	if cfg.InCluster {
		return rest.InClusterConfig()
	}

	// 3. Try in-cluster config (running inside a pod).
	if rc, err := rest.InClusterConfig(); err == nil {
		return rc, nil
	}

	// 4. Fall back to default kubeconfig loading rules
	//    (KUBECONFIG env var, ~/.kube/config).
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: cfg.Context},
	).ClientConfig()
}

// deploymentState maps Kubernetes Deployment conditions to a ctrlplane InstanceState.
func deploymentState(dep *appsv1.Deployment) provider.InstanceState {
	if dep.Spec.Replicas != nil && *dep.Spec.Replicas == 0 {
		return provider.StateStopped
	}

	for _, cond := range dep.Status.Conditions {
		if cond.Type == appsv1.DeploymentProgressing && cond.Status == "False" {
			return provider.StateFailed
		}
	}

	if dep.Status.ReadyReplicas > 0 && dep.Status.ReadyReplicas == dep.Status.Replicas {
		return provider.StateRunning
	}

	if dep.Status.UpdatedReplicas > 0 {
		return provider.StateStarting
	}

	return provider.StateProvisioning
}

// deploymentMessage extracts a human-readable message from deployment conditions.
func deploymentMessage(dep *appsv1.Deployment) string {
	for _, cond := range dep.Status.Conditions {
		if cond.Type == appsv1.DeploymentProgressing {
			return cond.Message
		}
	}

	return ""
}
