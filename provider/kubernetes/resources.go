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

	// configMapSuffix is appended to deployment names for ConfigMaps.
	configMapSuffix = "-env"
)

// deploymentName derives a Kubernetes-safe resource name from an instance ID.
func deploymentName(instanceID id.ID) string {
	// TypeID format: prefix_suffix. Use the full string with underscores replaced by dashes.
	return strings.ReplaceAll(instanceID.String(), "_", "-")
}

// configMapName returns the ConfigMap name for a given instance.
func configMapName(instanceID id.ID) string {
	return deploymentName(instanceID) + configMapSuffix
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

// buildDeployment creates a Kubernetes Deployment spec from a ProvisionRequest.
func buildDeployment(req provider.ProvisionRequest, namespace string, labels map[string]string) *appsv1.Deployment {
	replicas := int32(max(min(req.Resources.Replicas, int(^int32(0))), 1)) //nolint:gosec // clamped to int32 range via min

	containers := []corev1.Container{
		{
			Name:  "app",
			Image: req.Image,
			Resources: corev1.ResourceRequirements{
				Requests: buildResourceList(req.Resources),
				Limits:   buildResourceList(req.Resources),
			},
			Ports:        buildContainerPorts(req.Ports),
			VolumeMounts: buildVolumeMounts(req.Volumes),
		},
	}

	// If env vars are provided, reference them from the ConfigMap.
	if len(req.Env) > 0 {
		containers[0].EnvFrom = []corev1.EnvFromSource{
			{
				ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: configMapName(req.InstanceID),
					},
				},
			},
		}
	}

	annotations := maps.Clone(req.Annotations)
	if annotations == nil {
		annotations = make(map[string]string)
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        deploymentName(req.InstanceID),
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
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
				Spec: corev1.PodSpec{
					Containers: containers,
					Volumes:    buildVolumes(req.Volumes),
				},
			},
		},
	}
}

// buildService creates a Kubernetes Service from a ProvisionRequest.
// Returns nil if no ports are defined.
func buildService(req provider.ProvisionRequest, namespace string, labels map[string]string) *corev1.Service {
	if len(req.Ports) == 0 {
		return nil
	}

	ports := make([]corev1.ServicePort, 0, len(req.Ports))
	for i, p := range req.Ports {
		sp := corev1.ServicePort{
			Name:       fmt.Sprintf("port-%d", i),
			Port:       int32(p.Container),                   //nolint:gosec // ports are validated to fit int32 range (0-65535)
			TargetPort: intstr.FromInt32(int32(p.Container)), //nolint:gosec // ports are validated to fit int32 range (0-65535)
			Protocol:   toK8sProtocol(p.Protocol),
		}
		ports = append(ports, sp)
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName(req.InstanceID),
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				labelInstanceID: req.InstanceID.String(),
			},
			Ports: ports,
			Type:  corev1.ServiceTypeClusterIP,
		},
	}
}

// buildConfigMap creates a Kubernetes ConfigMap from environment variables.
// Returns nil if no environment variables are provided.
func buildConfigMap(req provider.ProvisionRequest, namespace string, labels map[string]string) *corev1.ConfigMap {
	if len(req.Env) == 0 {
		return nil
	}

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName(req.InstanceID),
			Namespace: namespace,
			Labels:    labels,
		},
		Data: req.Env,
	}
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
			ContainerPort: int32(p.Container), //nolint:gosec // ports are validated to fit int32 range (0-65535)
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

// buildVolumes converts volume specs to Kubernetes volume definitions.
func buildVolumes(volumes []provider.VolumeSpec) []corev1.Volume {
	if len(volumes) == 0 {
		return nil
	}

	result := make([]corev1.Volume, 0, len(volumes))
	for _, v := range volumes {
		vol := corev1.Volume{
			Name: v.Name,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: resource.NewQuantity(int64(v.SizeMB)*1024*1024, resource.BinarySI),
				},
			},
		}
		result = append(result, vol)
	}

	return result
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
