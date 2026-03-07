package kubernetes

import (
	"context"
	"fmt"
	"io"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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
type Provider struct {
	cfg    Config
	client kubernetes.Interface
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

	return p, nil
}

// Info returns metadata about this provider.
func (p *Provider) Info() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:    "kubernetes",
		Version: "0.1.0",
		Region:  p.cfg.Region,
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
	}
}

// Provision creates a Kubernetes Deployment, Service, and ConfigMap for an instance.
func (p *Provider) Provision(ctx context.Context, req provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	labels := instanceLabels(req.InstanceID, req.TenantID, p.cfg.Labels)
	ns := p.cfg.Namespace

	// Create ConfigMap if env vars are provided.
	if cm := buildConfigMap(req, ns, labels); cm != nil {
		if _, err := p.client.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("kubernetes: create configmap: %w", err)
		}
	}

	// Create Deployment.
	dep := buildDeployment(req, ns, labels)

	if _, err := p.client.AppsV1().Deployments(ns).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("kubernetes: create deployment: %w", err)
	}

	// Create Service if ports are defined.
	if svc := buildService(req, ns, labels); svc != nil {
		if _, err := p.client.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("kubernetes: create service: %w", err)
		}
	}

	return &provider.ProvisionResult{
		ProviderRef: providerRef(ns, req.InstanceID),
	}, nil
}

// Deprovision tears down all resources for an instance.
func (p *Provider) Deprovision(ctx context.Context, instanceID id.ID) error {
	ns := p.cfg.Namespace
	name := deploymentName(instanceID)
	propagation := metav1.DeletePropagationForeground

	// Delete Deployment.
	err := p.client.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})
	if err != nil {
		return fmt.Errorf("kubernetes: delete deployment: %w", err)
	}

	// Delete Service (ignore not found).
	_ = p.client.CoreV1().Services(ns).Delete(ctx, serviceName(instanceID), metav1.DeleteOptions{})

	// Delete ConfigMap (ignore not found).
	_ = p.client.CoreV1().ConfigMaps(ns).Delete(ctx, configMapName(instanceID), metav1.DeleteOptions{})

	return nil
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

// Deploy pushes a new release by updating the deployment image and ConfigMap.
func (p *Provider) Deploy(ctx context.Context, req provider.DeployRequest) (*provider.DeployResult, error) {
	ns := p.cfg.Namespace
	name := deploymentName(req.InstanceID)

	dep, err := p.client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("kubernetes: get deployment for deploy: %w", err)
	}

	// Update image on the first container.
	if len(dep.Spec.Template.Spec.Containers) > 0 {
		dep.Spec.Template.Spec.Containers[0].Image = req.Image
	}

	// Update release annotation.
	if dep.Annotations == nil {
		dep.Annotations = make(map[string]string)
	}

	dep.Annotations[annotationReleaseID] = req.ReleaseID.String()

	if _, err := p.client.AppsV1().Deployments(ns).Update(ctx, dep, metav1.UpdateOptions{}); err != nil {
		return nil, fmt.Errorf("kubernetes: update deployment: %w", err)
	}

	// Update ConfigMap if env vars are provided.
	if len(req.Env) > 0 {
		cmName := configMapName(req.InstanceID)

		cm, getErr := p.client.CoreV1().ConfigMaps(ns).Get(ctx, cmName, metav1.GetOptions{})
		if getErr == nil {
			cm.Data = req.Env

			if _, updateErr := p.client.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{}); updateErr != nil {
				return nil, fmt.Errorf("kubernetes: update configmap: %w", updateErr)
			}
		}
	}

	return &provider.DeployResult{
		ProviderRef: providerRef(ns, req.InstanceID),
		Status:      "deployed",
	}, nil
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

// Resources returns current resource utilization (stub).
func (p *Provider) Resources(_ context.Context, _ id.ID) (*provider.ResourceUsage, error) {
	return &provider.ResourceUsage{}, nil
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
