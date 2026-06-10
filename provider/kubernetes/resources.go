package kubernetes

import (
	"fmt"
	"maps"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

const (
	// labelInstanceID is the label key for the ctrlplane instance ID.
	labelInstanceID = "ctrlplane.io/instance-id"

	// labelTenantID is the label key for the ctrlplane tenant ID.
	labelTenantID = "ctrlplane.io/tenant-id"

	// labelManagedBy is the label key indicating the managing system.
	labelManagedBy = "ctrlplane.io/managed-by"

	// labelManagedByValue is the value for the managed-by label.
	labelManagedByValue = "ctrlplane"

	// annotationReleaseID is the annotation key for the current release ID.
	annotationReleaseID = "ctrlplane.io/release-id"

	// configMapSuffix is appended to per-service ConfigMap names.
	configMapSuffix = "-env"
)

// deploymentName derives a Kubernetes-safe resource name from an instance ID.
func deploymentName(instanceID id.ID) string {
	return strings.ReplaceAll(instanceID.String(), "_", "-")
}

// configMapNameFor returns the per-service ConfigMap name. Per-service
// ConfigMaps keep one service's env from leaking into another's
// container as bulk EnvFrom would do with a shared map.
func configMapNameFor(instanceID id.ID, serviceName string) string {
	return deploymentName(instanceID) + "-" + serviceName + configMapSuffix
}

// serviceName returns the Service name for a given instance.
func serviceName(instanceID id.ID) string {
	return deploymentName(instanceID)
}

// providerRef builds the canonical provider reference string.
func providerRef(namespace string, instanceID id.ID) string {
	return fmt.Sprintf("k8s:%s/%s", namespace, deploymentName(instanceID))
}

// instanceLabels builds the standard label set for a ctrlplane-managed resource.
func instanceLabels(instanceID id.ID, tenantID string, extra map[string]string) map[string]string {
	labels := map[string]string{
		labelInstanceID: instanceID.String(),
		labelTenantID:   tenantID,
		labelManagedBy:  labelManagedByValue,
	}

	maps.Copy(labels, extra)

	return labels
}

// instanceSelector returns a label selector that matches resources for a given instance.
func instanceSelector(instanceID id.ID) string {
	return fmt.Sprintf("%s=%s", labelInstanceID, instanceID.String())
}

// replicaCountFor returns the desired replica count for a workload's
// Deployment/StatefulSet. The Main service's Resources.Replicas is
// authoritative; non-Main services don't carry a count (they always
// run alongside Main).
func replicaCountFor(req provider.ProvisionRequest) int32 {
	const maxInt32 = int(^uint32(0) >> 1)

	for i := range req.Services {
		if req.Services[i].Role == provider.RoleMain || req.Services[i].Role == "" {
			n := min(max(req.Services[i].Resources.Replicas, 1), maxInt32)

			return int32(n) //nolint:gosec // clamped to [1, maxInt32] above
		}
	}

	return 1
}

// buildPodSpec assembles a PodSpec from a ProvisionRequest's Services.
// Init services land in InitContainers (run-once before main start);
// Main and Sidecar services land in Containers and run for the pod's
// lifetime.
//
// imagePullSecrets is the list of Secret names in the same namespace
// that hold registry credentials. Pass nil/empty for public images.
func buildPodSpec(req provider.ProvisionRequest, imagePullSecrets []string) corev1.PodSpec {
	var (
		containers     []corev1.Container
		initContainers []corev1.Container
		podVolumes     []corev1.Volume
		seenVolume     = make(map[string]struct{})
	)

	for i := range req.Services {
		svc := req.Services[i]
		c := buildContainer(req.InstanceID, svc)

		if svc.Role == provider.RoleInit {
			initContainers = append(initContainers, c)
		} else {
			containers = append(containers, c)
		}

		// Aggregate volumes across services. Two services that share a
		// volume name map to a single Pod volume the runtime mounts
		// into both containers — the standard pattern for sharing data
		// between Main and a Sidecar.
		for _, v := range svc.Volumes {
			if _, dup := seenVolume[v.Name]; dup {
				continue
			}

			seenVolume[v.Name] = struct{}{}

			podVolumes = append(podVolumes, corev1.Volume{
				Name: v.Name,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{
						SizeLimit: resource.NewQuantity(int64(v.SizeMB)*1024*1024, resource.BinarySI),
					},
				},
			})
		}
	}

	var pullSecretRefs []corev1.LocalObjectReference
	for _, s := range imagePullSecrets {
		pullSecretRefs = append(pullSecretRefs, corev1.LocalObjectReference{Name: s})
	}

	return corev1.PodSpec{
		Containers:       containers,
		InitContainers:   initContainers,
		Volumes:          podVolumes,
		ImagePullSecrets: pullSecretRefs,
	}
}

// buildContainer translates one ServiceSpec into a Kubernetes Container.
func buildContainer(instanceID id.ID, svc provider.ServiceSpec) corev1.Container {
	c := corev1.Container{
		Name:    svc.Name,
		Image:   svc.Image,
		Command: svc.Command,
		Args:    svc.Args,
		Resources: corev1.ResourceRequirements{
			Requests: buildResourceList(svc.Resources),
			Limits:   buildResourceList(svc.Resources),
		},
		Ports:        buildContainerPorts(svc.Ports),
		VolumeMounts: buildVolumeMounts(svc.Volumes),
	}

	if len(svc.Env) > 0 {
		c.EnvFrom = []corev1.EnvFromSource{
			{
				ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: configMapNameFor(instanceID, svc.Name),
					},
				},
			},
		}
	}

	return c
}

// buildDeployment creates a Kubernetes Deployment for a stateless
// workload (req.Kind == KindDeployment, the default).
func buildDeployment(req provider.ProvisionRequest, namespace string, labels map[string]string, imagePullSecrets []string) *appsv1.Deployment {
	replicas := replicaCountFor(req)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName(req.InstanceID),
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					labelInstanceID: req.InstanceID.String(),
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: buildPodSpec(req, imagePullSecrets),
			},
		},
	}
}

// buildStatefulSet creates a StatefulSet + headless Service for a
// stateful workload (req.Kind == KindStatefulSet). Persistent volumes
// declared on services become volumeClaimTemplates so each replica
// gets its own PVC.
func buildStatefulSet(req provider.ProvisionRequest, namespace string, labels map[string]string, imagePullSecrets []string) *appsv1.StatefulSet {
	replicas := replicaCountFor(req)
	podSpec := buildPodSpec(req, imagePullSecrets)

	// Volume claims: take every unique named volume across services
	// and emit a volumeClaimTemplate for it. The PodSpec volumes that
	// match these names are dropped — StatefulSet auto-injects the
	// PVC-backed volumes from the templates.
	claimNames := make(map[string]int64)

	for i := range req.Services {
		for _, v := range req.Services[i].Volumes {
			if v.SizeMB > 0 {
				claimNames[v.Name] = int64(v.SizeMB) * 1024 * 1024
			}
		}
	}

	templates := make([]corev1.PersistentVolumeClaim, 0, len(claimNames))

	for name, size := range claimNames {
		templates = append(templates, corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: *resource.NewQuantity(size, resource.BinarySI),
					},
				},
			},
		})
	}

	// Strip pod-level Volumes for any name that has a claim template —
	// k8s will reject the spec if a Pod volume name collides with a
	// volumeClaimTemplate name.
	filtered := podSpec.Volumes[:0]

	for _, v := range podSpec.Volumes {
		if _, isClaim := claimNames[v.Name]; isClaim {
			continue
		}

		filtered = append(filtered, v)
	}

	podSpec.Volumes = filtered

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName(req.InstanceID),
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: serviceName(req.InstanceID),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					labelInstanceID: req.InstanceID.String(),
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: podSpec,
			},
			VolumeClaimTemplates: templates,
		},
	}
}

// buildService creates a Kubernetes Service exposing every service's
// ports. Each port's TargetPort is the named port on the Container so
// k8s routes correctly even when two services in the same Pod publish
// the same numeric port (it shouldn't, but we don't enforce
// non-collision at this layer).
//
// For StatefulSets the caller passes headless=true to set ClusterIP=None
// — required for stable per-replica DNS.
func buildService(req provider.ProvisionRequest, namespace string, labels map[string]string, headless bool) *corev1.Service {
	var ports []corev1.ServicePort

	for i := range req.Services {
		svc := req.Services[i]

		for j, p := range svc.Ports {
			ports = append(ports, corev1.ServicePort{
				Name:       fmt.Sprintf("%s-%d", svc.Name, j),
				Port:       int32(p.Container),                   //nolint:gosec // 0-65535 fits int32
				TargetPort: intstr.FromInt32(int32(p.Container)), //nolint:gosec
				Protocol:   toK8sProtocol(p.Protocol),
			})
		}
	}

	if len(ports) == 0 {
		return nil
	}

	spec := corev1.ServiceSpec{
		Selector: map[string]string{
			labelInstanceID: req.InstanceID.String(),
		},
		Ports: ports,
		Type:  corev1.ServiceTypeClusterIP,
	}

	if headless {
		spec.ClusterIP = corev1.ClusterIPNone
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName(req.InstanceID),
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: spec,
	}
}

// buildConfigMaps creates one ConfigMap per service that has env vars.
// Per-service maps keep service A's env out of service B's container
// (a single shared map with EnvFrom would expose everything to
// everyone).
func buildConfigMaps(req provider.ProvisionRequest, namespace string, labels map[string]string) []*corev1.ConfigMap {
	var maps []*corev1.ConfigMap

	for i := range req.Services {
		svc := req.Services[i]
		if len(svc.Env) == 0 {
			continue
		}

		maps = append(maps, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapNameFor(req.InstanceID, svc.Name),
				Namespace: namespace,
				Labels:    labels,
			},
			Data: svc.Env,
		})
	}

	return maps
}

// buildResourceList converts a ResourceSpec into Kubernetes resource quantities.
func buildResourceList(spec provider.ResourceSpec) corev1.ResourceList {
	resources := corev1.ResourceList{}

	if spec.CPUMillis > 0 {
		resources[corev1.ResourceCPU] = *resource.NewMilliQuantity(int64(spec.CPUMillis), resource.DecimalSI)
	}

	if spec.MemoryMB > 0 {
		resources[corev1.ResourceMemory] = *resource.NewQuantity(int64(spec.MemoryMB)*1024*1024, resource.BinarySI)
	}

	return resources
}

// buildContainerPorts converts port specs to Kubernetes container ports.
func buildContainerPorts(ports []provider.PortSpec) []corev1.ContainerPort {
	if len(ports) == 0 {
		return nil
	}

	result := make([]corev1.ContainerPort, 0, len(ports))
	for i, p := range ports {
		cp := corev1.ContainerPort{
			Name:          fmt.Sprintf("port-%d", i),
			ContainerPort: int32(p.Container), //nolint:gosec // 0-65535 fits int32
			Protocol:      toK8sProtocol(p.Protocol),
		}
		result = append(result, cp)
	}

	return result
}

// buildVolumeMounts converts volume specs to Kubernetes volume mounts.
func buildVolumeMounts(volumes []provider.VolumeSpec) []corev1.VolumeMount {
	if len(volumes) == 0 {
		return nil
	}

	result := make([]corev1.VolumeMount, 0, len(volumes))
	for _, v := range volumes {
		vm := corev1.VolumeMount{
			Name:      v.Name,
			MountPath: v.MountPath,
		}
		result = append(result, vm)
	}

	return result
}

// buildEndpoints derives the in-cluster DNS endpoints for a provisioned
// instance. One Endpoint is emitted per service that declares at least
// one port. The URL has the form:
//
//	http://<serviceName>.<namespace>.svc.cluster.local:<firstPort>
//
// This is the stable address consumers (e.g. twinos injectUpstreamEnv)
// should use to reach the workload from within the cluster. Non-HTTP
// protocols are not distinguished here — callers that need TLS or gRPC
// URLs should wrap the value themselves.
func buildEndpoints(req provider.ProvisionRequest, namespace string) []provider.Endpoint {
	svcName := serviceName(req.InstanceID)

	var endpoints []provider.Endpoint

	for i := range req.Services {
		svc := req.Services[i]
		if len(svc.Ports) == 0 {
			continue
		}

		firstPort := svc.Ports[0].Container
		url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", svcName, namespace, firstPort)

		endpoints = append(endpoints, provider.Endpoint{
			ServiceName: svc.Name,
			URL:         url,
			Port:        firstPort,
			Protocol:    "TCP",
		})
	}

	return endpoints
}

// toK8sProtocol maps a protocol string to a Kubernetes Protocol constant.
func toK8sProtocol(protocol string) corev1.Protocol {
	switch strings.ToUpper(protocol) {
	case "UDP":
		return corev1.ProtocolUDP
	case "SCTP":
		return corev1.ProtocolSCTP
	default:
		return corev1.ProtocolTCP
	}
}
