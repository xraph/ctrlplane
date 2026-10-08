package contract

import (
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/network"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/secrets"
	"github.com/xraph/ctrlplane/telemetry"
	"github.com/xraph/ctrlplane/template"
	"github.com/xraph/ctrlplane/vars"
	"github.com/xraph/ctrlplane/worker"
	"github.com/xraph/ctrlplane/workload"
)

// Response types snapshot the dashboard surface. New domain fields need an explicit mapping.
type instanceDTO struct {
	entityDTO

	TenantID       string                 `json:"tenant_id"`
	Name           string                 `json:"name"`
	Slug           string                 `json:"slug"`
	DatacenterID   id.ID                  `json:"datacenter_id,omitzero"`
	ProviderName   string                 `json:"provider_name"`
	ProviderRef    string                 `json:"provider_ref"`
	ServiceRefs    map[string]string      `json:"service_refs,omitempty"`
	Region         string                 `json:"region"`
	State          provider.InstanceState `json:"state"`
	Kind           provider.WorkloadKind  `json:"kind"`
	Services       []serviceSpecDTO       `json:"services"`
	Source         deploymentSourceDTO    `json:"source,omitzero"`
	Endpoints      []endpointDTO          `json:"endpoints,omitempty"`
	Labels         map[string]string      `json:"labels,omitempty"`
	CurrentRelease id.ID                  `json:"current_release,omitzero"`
	SuspendedAt    *time.Time             `json:"suspended_at,omitempty"`
}

func projectInstance(v instance.Instance) instanceDTO {
	return instanceDTO{
		entityDTO:      projectEntity(v.Entity),
		TenantID:       v.TenantID,
		Name:           v.Name,
		Slug:           v.Slug,
		DatacenterID:   v.DatacenterID,
		ProviderName:   v.ProviderName,
		ProviderRef:    v.ProviderRef,
		ServiceRefs:    v.ServiceRefs,
		Region:         v.Region,
		State:          v.State,
		Kind:           v.Kind,
		Services:       viewSlice(v.Services, projectServiceSpec),
		Source:         projectDeploymentSource(v.Source),
		Endpoints:      viewSlice(v.Endpoints, projectEndpoint),
		Labels:         v.Labels,
		CurrentRelease: v.CurrentRelease,
		SuspendedAt:    viewPointer(v.SuspendedAt, func(t time.Time) time.Time { return t.UTC() }),
	}
}

type entityDTO struct {
	ID        id.ID     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func projectEntity(v ctrlplane.Entity) entityDTO {
	return entityDTO{
		ID:        v.ID,
		CreatedAt: v.CreatedAt.UTC(),
		UpdatedAt: v.UpdatedAt.UTC(),
	}
}

type serviceSpecDTO struct {
	Name        string               `json:"name"`
	Image       string               `json:"image"`
	Role        provider.ServiceRole `json:"role,omitempty"`
	DependsOn   []string             `json:"depends_on,omitempty"`
	Resources   resourceSpecDTO      `json:"resources"`
	Env         map[string]string    `json:"env,omitempty"`
	Ports       []portSpecDTO        `json:"ports,omitempty"`
	Volumes     []volumeSpecDTO      `json:"volumes,omitempty"`
	HealthCheck *healthCheckSpecDTO  `json:"health_check,omitempty"`
	Secrets     []secretRefDTO       `json:"secrets,omitempty"`
	ConfigFiles []configFileDTO      `json:"config_files,omitempty"`
	Annotations map[string]string    `json:"annotations,omitempty"`
	Command     []string             `json:"command,omitempty"`
	Args        []string             `json:"args,omitempty"`
}

func projectServiceSpec(v provider.ServiceSpec) serviceSpecDTO {
	return serviceSpecDTO{
		Name:        v.Name,
		Image:       v.Image,
		Role:        v.Role,
		DependsOn:   viewSlice(v.DependsOn, func(v string) string { return v }),
		Resources:   projectResourceSpec(v.Resources),
		Env:         v.Env,
		Ports:       viewSlice(v.Ports, projectPortSpec),
		Volumes:     viewSlice(v.Volumes, projectVolumeSpec),
		HealthCheck: viewPointer(v.HealthCheck, projectHealthCheckSpec),
		Secrets:     viewSlice(v.Secrets, projectSecretRef),
		ConfigFiles: viewSlice(v.ConfigFiles, projectConfigFile),
		Annotations: v.Annotations,
		Command:     viewSlice(v.Command, func(v string) string { return v }),
		Args:        viewSlice(v.Args, func(v string) string { return v }),
	}
}

type resourceSpecDTO struct {
	CPUMillis int    `json:"cpu_millis"`
	MemoryMB  int    `json:"memory_mb"`
	DiskMB    int    `json:"disk_mb,omitempty"`
	Replicas  int    `json:"replicas"`
	GPU       string `json:"gpu,omitempty"`
}

func projectResourceSpec(v provider.ResourceSpec) resourceSpecDTO {
	return resourceSpecDTO{
		CPUMillis: v.CPUMillis,
		MemoryMB:  v.MemoryMB,
		DiskMB:    v.DiskMB,
		Replicas:  v.Replicas,
		GPU:       v.GPU,
	}
}

type portSpecDTO struct {
	Container int    `json:"container"`
	Host      int    `json:"host,omitempty"`
	Protocol  string `json:"protocol"`
}

func projectPortSpec(v provider.PortSpec) portSpecDTO {
	return portSpecDTO{
		Container: v.Container,
		Host:      v.Host,
		Protocol:  v.Protocol,
	}
}

type volumeSpecDTO struct {
	Name      string `json:"name"`
	MountPath string `json:"mount_path"`
	SizeMB    int    `json:"size_mb"`
	Type      string `json:"type"`
}

func projectVolumeSpec(v provider.VolumeSpec) volumeSpecDTO {
	return volumeSpecDTO{
		Name:      v.Name,
		MountPath: v.MountPath,
		SizeMB:    v.SizeMB,
		Type:      v.Type,
	}
}

type healthCheckSpecDTO struct {
	Path     string        `json:"path,omitempty"`
	Port     int           `json:"port"`
	Interval time.Duration `json:"interval"`
	Timeout  time.Duration `json:"timeout"`
	Retries  int           `json:"retries"`
}

func projectHealthCheckSpec(v provider.HealthCheckSpec) healthCheckSpecDTO {
	return healthCheckSpecDTO{
		Path:     v.Path,
		Port:     v.Port,
		Interval: v.Interval,
		Timeout:  v.Timeout,
		Retries:  v.Retries,
	}
}

type secretRefDTO struct {
	Key  string             `json:"key"`
	Type secrets.SecretType `json:"type"`
}

func projectSecretRef(v provider.SecretRef) secretRefDTO {
	return secretRefDTO{
		Key:  v.Key,
		Type: v.Type,
	}
}

type configFileDTO struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Format  string `json:"format"`
	Content string `json:"content"`
}

func projectConfigFile(v provider.ConfigFile) configFileDTO {
	return configFileDTO{
		Name:    v.Name,
		Path:    v.Path,
		Format:  v.Format,
		Content: v.Content,
	}
}

type deploymentSourceDTO struct {
	Type      provider.SourceType `json:"type"`
	Services  []serviceSpecDTO    `json:"services,omitempty"`
	Helm      *helmSourceDTO      `json:"helm,omitempty"`
	Manifests *manifestSourceDTO  `json:"manifests,omitempty"`
	ArgoCD    *argoCDSourceDTO    `json:"argocd,omitempty"`
}

func projectDeploymentSource(v provider.DeploymentSource) deploymentSourceDTO {
	return deploymentSourceDTO{
		Type:      v.Type,
		Services:  viewSlice(v.Services, projectServiceSpec),
		Helm:      viewPointer(v.Helm, projectHelmSource),
		Manifests: viewPointer(v.Manifests, projectManifestSource),
		ArgoCD:    viewPointer(v.ArgoCD, projectArgoCDSource),
	}
}

type helmSourceDTO struct {
	Repo        string         `json:"repo,omitempty"`
	Chart       string         `json:"chart"`
	Version     string         `json:"version,omitempty"`
	ReleaseName string         `json:"release_name,omitempty"`
	Namespace   string         `json:"namespace,omitempty"`
	Values      map[string]any `json:"values,omitempty"`
	ValuesFiles []string       `json:"values_files,omitempty"`
}

func projectHelmSource(v provider.HelmSource) helmSourceDTO {
	return helmSourceDTO{
		Repo:        v.Repo,
		Chart:       v.Chart,
		Version:     v.Version,
		ReleaseName: v.ReleaseName,
		Namespace:   v.Namespace,
		Values:      v.Values,
		ValuesFiles: viewSlice(v.ValuesFiles, func(v string) string { return v }),
	}
}

type manifestSourceDTO struct {
	Inline    string              `json:"inline,omitempty"`
	Kustomize *kustomizeSourceDTO `json:"kustomize,omitempty"`
}

func projectManifestSource(v provider.ManifestSource) manifestSourceDTO {
	return manifestSourceDTO{
		Inline:    v.Inline,
		Kustomize: viewPointer(v.Kustomize, projectKustomizeSource),
	}
}

type kustomizeSourceDTO struct {
	Files map[string]string `json:"files"`
	Root  string            `json:"root,omitempty"`
}

func projectKustomizeSource(v provider.KustomizeSource) kustomizeSourceDTO {
	return kustomizeSourceDTO{
		Files: v.Files,
		Root:  v.Root,
	}
}

type argoCDSourceDTO struct {
	Project        string            `json:"project,omitempty"`
	RepoURL        string            `json:"repo_url"`
	Path           string            `json:"path,omitempty"`
	TargetRevision string            `json:"target_revision,omitempty"`
	DestServer     string            `json:"dest_server,omitempty"`
	DestNamespace  string            `json:"dest_namespace,omitempty"`
	Helm           *argoHelmDTO      `json:"helm,omitempty"`
	SyncPolicy     argoSyncPolicyDTO `json:"sync_policy,omitzero"`
}

func projectArgoCDSource(v provider.ArgoCDSource) argoCDSourceDTO {
	return argoCDSourceDTO{
		Project:        v.Project,
		RepoURL:        v.RepoURL,
		Path:           v.Path,
		TargetRevision: v.TargetRevision,
		DestServer:     v.DestServer,
		DestNamespace:  v.DestNamespace,
		Helm:           viewPointer(v.Helm, projectArgoHelm),
		SyncPolicy:     projectArgoSyncPolicy(v.SyncPolicy),
	}
}

type argoHelmDTO struct {
	ValueFiles []string          `json:"value_files,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
}

func projectArgoHelm(v provider.ArgoHelm) argoHelmDTO {
	return argoHelmDTO{
		ValueFiles: viewSlice(v.ValueFiles, func(v string) string { return v }),
		Parameters: v.Parameters,
	}
}

type argoSyncPolicyDTO struct {
	Automated bool `json:"automated,omitempty"`
	SelfHeal  bool `json:"self_heal,omitempty"`
	Prune     bool `json:"prune,omitempty"`
}

func projectArgoSyncPolicy(v provider.ArgoSyncPolicy) argoSyncPolicyDTO {
	return argoSyncPolicyDTO{
		Automated: v.Automated,
		SelfHeal:  v.SelfHeal,
		Prune:     v.Prune,
	}
}

type endpointDTO struct {
	ServiceName string `json:"service_name,omitempty"`
	URL         string `json:"url"`
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"`
	Public      bool   `json:"public"`
}

func projectEndpoint(v provider.Endpoint) endpointDTO {
	return endpointDTO{
		ServiceName: v.ServiceName,
		URL:         v.URL,
		Port:        v.Port,
		Protocol:    v.Protocol,
		Public:      v.Public,
	}
}

type workloadDTO struct {
	entityDTO

	TenantID         string                `json:"tenant_id"`
	Name             string                `json:"name"`
	Slug             string                `json:"slug"`
	DatacenterID     id.ID                 `json:"datacenter_id,omitzero"`
	ProviderName     string                `json:"provider_name"`
	Region           string                `json:"region"`
	Kind             provider.WorkloadKind `json:"kind"`
	Services         []serviceSpecDTO      `json:"services"`
	Labels           map[string]string     `json:"labels,omitempty"`
	CurrentReleaseID id.ID                 `json:"current_release_id,omitzero"`
	ReplicaCount     int                   `json:"replica_count"`
	State            workload.State        `json:"state"`
	PausedAt         *time.Time            `json:"paused_at,omitempty"`
	PreviousReplicas int                   `json:"previous_replicas,omitempty"`
	TemplateID       id.ID                 `json:"template_id,omitzero"`
}

func projectWorkload(v workload.Workload) workloadDTO {
	return workloadDTO{
		entityDTO:        projectEntity(v.Entity),
		TenantID:         v.TenantID,
		Name:             v.Name,
		Slug:             v.Slug,
		DatacenterID:     v.DatacenterID,
		ProviderName:     v.ProviderName,
		Region:           v.Region,
		Kind:             v.Kind,
		Services:         viewSlice(v.Services, projectServiceSpec),
		Labels:           v.Labels,
		CurrentReleaseID: v.CurrentReleaseID,
		ReplicaCount:     v.ReplicaCount,
		State:            v.State,
		PausedAt:         viewPointer(v.PausedAt, func(t time.Time) time.Time { return t.UTC() }),
		PreviousReplicas: v.PreviousReplicas,
		TemplateID:       v.TemplateID,
	}
}

type workloadHealthDTO struct {
	WorkloadID    id.ID  `json:"workload_id"`
	Status        string `json:"status"`
	ReplicaCount  int    `json:"replica_count"`
	HealthyCount  int    `json:"healthy_count"`
	DegradedCount int    `json:"degraded_count"`
	UnhealthyCnt  int    `json:"unhealthy_count"`
	UnknownCount  int    `json:"unknown_count"`
}

func projectWorkloadHealth(v workload.WorkloadHealth) workloadHealthDTO {
	return workloadHealthDTO{
		WorkloadID:    v.WorkloadID,
		Status:        v.Status,
		ReplicaCount:  v.ReplicaCount,
		HealthyCount:  v.HealthyCount,
		DegradedCount: v.DegradedCount,
		UnhealthyCnt:  v.UnhealthyCnt,
		UnknownCount:  v.UnknownCount,
	}
}

type templateDTO struct {
	entityDTO

	TenantID        string                `json:"tenant_id"`
	Name            string                `json:"name"`
	Description     string                `json:"description,omitempty"`
	DefaultKind     provider.WorkloadKind `json:"default_kind,omitempty"`
	DefaultStrategy string                `json:"default_strategy,omitempty"`
	Services        []serviceSpecDTO      `json:"services"`
	Labels          map[string]string     `json:"labels,omitempty"`
	Notes           string                `json:"notes,omitempty"`
	Variables       []definitionDTO       `json:"variables,omitempty"`
	Source          deploymentSourceDTO   `json:"source,omitzero"`
}

func projectTemplate(v template.Template) templateDTO {
	return templateDTO{
		entityDTO:       projectEntity(v.Entity),
		TenantID:        v.TenantID,
		Name:            v.Name,
		Description:     v.Description,
		DefaultKind:     v.DefaultKind,
		DefaultStrategy: v.DefaultStrategy,
		Services:        viewSlice(v.Services, projectServiceSpec),
		Labels:          v.Labels,
		Notes:           v.Notes,
		Variables:       viewSlice(v.Variables, projectDefinition),
		Source:          projectDeploymentSource(v.Source),
	}
}

type definitionDTO struct {
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Type        vars.Type     `json:"type"`
	Required    bool          `json:"required,omitempty"`
	Default     any           `json:"default,omitempty"`
	Enum        []string      `json:"enum,omitempty"`
	Secret      *secretRefDTO `json:"secret,omitempty"`
	Expression  string        `json:"expression,omitempty"`
	Pattern     string        `json:"pattern,omitempty"`
}

func projectDefinition(v vars.Definition) definitionDTO {
	return definitionDTO{
		Name:        v.Name,
		Description: v.Description,
		Type:        v.Type,
		Required:    v.Required,
		Default:     v.Default,
		Enum:        viewSlice(v.Enum, func(v string) string { return v }),
		Secret:      viewPointer(v.Secret, projectSecretRef),
		Expression:  v.Expression,
		Pattern:     v.Pattern,
	}
}

type datacenterDTO struct {
	entityDTO

	TenantID          string                    `json:"tenant_id"`
	Name              string                    `json:"name"`
	Slug              string                    `json:"slug"`
	ProviderName      string                    `json:"provider_name"`
	Region            string                    `json:"region"`
	Zone              string                    `json:"zone"`
	Status            datacenter.Status         `json:"status"`
	Location          locationDTO               `json:"location"`
	Capacity          capacityDTO               `json:"capacity"`
	Labels            map[string]string         `json:"labels,omitempty"`
	Metadata          map[string]string         `json:"metadata,omitempty"`
	LastCheckedAt     *time.Time                `json:"last_checked_at,omitempty"`
	BootstrapServices []bootstrapServiceSpecDTO `json:"bootstrap_services,omitempty"`
}

func projectDatacenter(v datacenter.Datacenter) datacenterDTO {
	return datacenterDTO{
		entityDTO:         projectEntity(v.Entity),
		TenantID:          v.TenantID,
		Name:              v.Name,
		Slug:              v.Slug,
		ProviderName:      v.ProviderName,
		Region:            v.Region,
		Zone:              v.Zone,
		Status:            v.Status,
		Location:          projectLocation(v.Location),
		Capacity:          projectCapacity(v.Capacity),
		Labels:            v.Labels,
		Metadata:          v.Metadata,
		LastCheckedAt:     viewPointer(v.LastCheckedAt, func(t time.Time) time.Time { return t.UTC() }),
		BootstrapServices: viewSlice(v.BootstrapServices, projectBootstrapServiceSpec),
	}
}

type locationDTO struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Country   string  `json:"country"`
	City      string  `json:"city"`
}

func projectLocation(v datacenter.Location) locationDTO {
	return locationDTO{
		Latitude:  v.Latitude,
		Longitude: v.Longitude,
		Country:   v.Country,
		City:      v.City,
	}
}

type capacityDTO struct {
	MaxInstances int `json:"max_instances"`
	MaxCPUMillis int `json:"max_cpu_millis"`
	MaxMemoryMB  int `json:"max_memory_mb"`
}

func projectCapacity(v datacenter.Capacity) capacityDTO {
	return capacityDTO{
		MaxInstances: v.MaxInstances,
		MaxCPUMillis: v.MaxCPUMillis,
		MaxMemoryMB:  v.MaxMemoryMB,
	}
}

type bootstrapServiceSpecDTO struct {
	Name     string                `json:"name"`
	Kind     provider.WorkloadKind `json:"kind,omitempty"`
	Replicas int                   `json:"replicas,omitempty"`
	Services []serviceSpecDTO      `json:"services"`
	Labels   map[string]string     `json:"labels,omitempty"`
}

func projectBootstrapServiceSpec(v bootstrap.BootstrapServiceSpec) bootstrapServiceSpecDTO {
	return bootstrapServiceSpecDTO{
		Name:     v.Name,
		Kind:     v.Kind,
		Replicas: v.Replicas,
		Services: viewSlice(v.Services, projectServiceSpec),
		Labels:   v.Labels,
	}
}

type deploymentDTO struct {
	entityDTO

	TenantID        string                 `json:"tenant_id"`
	InstanceID      id.ID                  `json:"instance_id"`
	ReleaseID       id.ID                  `json:"release_id"`
	State           deploy.DeployState     `json:"state"`
	Strategy        string                 `json:"strategy"`
	Services        []serviceDeploySpecDTO `json:"services"`
	ServiceProgress map[string]string      `json:"service_progress,omitempty"`
	ProviderRef     string                 `json:"provider_ref,omitempty"`
	StartedAt       *time.Time             `json:"started_at,omitempty"`
	FinishedAt      *time.Time             `json:"finished_at,omitempty"`
	Error           string                 `json:"error,omitempty"`
	Initiator       string                 `json:"initiator"`
}

func projectDeployment(v deploy.Deployment) deploymentDTO {
	return deploymentDTO{
		entityDTO:       projectEntity(v.Entity),
		TenantID:        v.TenantID,
		InstanceID:      v.InstanceID,
		ReleaseID:       v.ReleaseID,
		State:           v.State,
		Strategy:        v.Strategy,
		Services:        viewSlice(v.Services, projectServiceDeploySpec),
		ServiceProgress: v.ServiceProgress,
		ProviderRef:     v.ProviderRef,
		StartedAt:       viewPointer(v.StartedAt, func(t time.Time) time.Time { return t.UTC() }),
		FinishedAt:      viewPointer(v.FinishedAt, func(t time.Time) time.Time { return t.UTC() }),
		Error:           safeDiagnostic(v.Error),
		Initiator:       v.Initiator,
	}
}

type serviceDeploySpecDTO struct {
	Name        string              `json:"name"`
	Image       string              `json:"image"`
	Env         map[string]string   `json:"env,omitempty"`
	HealthCheck *healthCheckSpecDTO `json:"health_check,omitempty"`
}

func projectServiceDeploySpec(v provider.ServiceDeploySpec) serviceDeploySpecDTO {
	return serviceDeploySpecDTO{
		Name:        v.Name,
		Image:       v.Image,
		Env:         v.Env,
		HealthCheck: viewPointer(v.HealthCheck, projectHealthCheckSpec),
	}
}

type releaseDTO struct {
	entityDTO

	TenantID   string               `json:"tenant_id"`
	InstanceID id.ID                `json:"instance_id"`
	Version    int                  `json:"version"`
	Services   []serviceSnapshotDTO `json:"services"`
	Notes      string               `json:"notes,omitempty"`
	CommitSHA  string               `json:"commit_sha,omitempty"`
	Active     bool                 `json:"active"`
}

func projectRelease(v deploy.Release) releaseDTO {
	return releaseDTO{
		entityDTO:  projectEntity(v.Entity),
		TenantID:   v.TenantID,
		InstanceID: v.InstanceID,
		Version:    v.Version,
		Services:   viewSlice(v.Services, projectServiceSnapshot),
		Notes:      v.Notes,
		CommitSHA:  v.CommitSHA,
		Active:     v.Active,
	}
}

type serviceSnapshotDTO struct {
	Name  string            `json:"name"`
	Image string            `json:"image"`
	Env   map[string]string `json:"env,omitempty"`
}

func projectServiceSnapshot(v provider.ServiceSnapshot) serviceSnapshotDTO {
	return serviceSnapshotDTO{
		Name:  v.Name,
		Image: v.Image,
		Env:   v.Env,
	}
}

type domainDTO struct {
	entityDTO

	TenantID    string     `json:"tenant_id"`
	InstanceID  id.ID      `json:"instance_id"`
	Hostname    string     `json:"hostname"`
	Verified    bool       `json:"verified"`
	TLSEnabled  bool       `json:"tls_enabled"`
	CertExpiry  *time.Time `json:"cert_expiry,omitempty"`
	DNSTarget   string     `json:"dns_target"`
	VerifyToken string     `json:"verify_token"`
}

func projectDomain(v network.Domain) domainDTO {
	return domainDTO{
		entityDTO:   projectEntity(v.Entity),
		TenantID:    v.TenantID,
		InstanceID:  v.InstanceID,
		Hostname:    v.Hostname,
		Verified:    v.Verified,
		TLSEnabled:  v.TLSEnabled,
		CertExpiry:  viewPointer(v.CertExpiry, func(t time.Time) time.Time { return t.UTC() }),
		DNSTarget:   v.DNSTarget,
		VerifyToken: v.VerifyToken,
	}
}

type routeDTO struct {
	entityDTO

	TenantID          string `json:"tenant_id"`
	InstanceID        id.ID  `json:"instance_id"`
	ServiceName       string `json:"service_name,omitempty"`
	Path              string `json:"path"`
	Port              int    `json:"port"`
	Protocol          string `json:"protocol"`
	Weight            int    `json:"weight"`
	StripPrefix       bool   `json:"strip_prefix"`
	RewriteRedirects  bool   `json:"rewrite_redirects,omitempty"`
	RewriteCookiePath bool   `json:"rewrite_cookie_path,omitempty"`
	UpstreamOrigin    string `json:"upstream_origin,omitempty"`
	TLSVerify         bool   `json:"tls_verify"`
	Hostname          string `json:"hostname,omitempty"`
}

func projectRoute(v network.Route) routeDTO {
	return routeDTO{
		entityDTO:         projectEntity(v.Entity),
		TenantID:          v.TenantID,
		InstanceID:        v.InstanceID,
		ServiceName:       v.ServiceName,
		Path:              v.Path,
		Port:              v.Port,
		Protocol:          v.Protocol,
		Weight:            v.Weight,
		StripPrefix:       v.StripPrefix,
		RewriteRedirects:  v.RewriteRedirects,
		RewriteCookiePath: v.RewriteCookiePath,
		UpstreamOrigin:    v.UpstreamOrigin,
		TLSVerify:         v.TLSVerify,
		Hostname:          v.Hostname,
	}
}

type certificateDTO struct {
	entityDTO

	DomainID  id.ID     `json:"domain_id"`
	TenantID  string    `json:"tenant_id"`
	Issuer    string    `json:"issuer"`
	ExpiresAt time.Time `json:"expires_at"`
	AutoRenew bool      `json:"auto_renew"`
}

func projectCertificate(v network.Certificate) certificateDTO {
	return certificateDTO{
		entityDTO: projectEntity(v.Entity),
		DomainID:  v.DomainID,
		TenantID:  v.TenantID,
		Issuer:    v.Issuer,
		ExpiresAt: v.ExpiresAt.UTC(),
		AutoRenew: v.AutoRenew,
	}
}

type healthCheckDTO struct {
	entityDTO

	TenantID    string           `json:"tenant_id"`
	InstanceID  id.ID            `json:"instance_id"`
	ServiceName string           `json:"service_name,omitempty"`
	Name        string           `json:"name"`
	Type        health.CheckType `json:"type"`
	Target      string           `json:"target"`
	Interval    time.Duration    `json:"interval"`
	Timeout     time.Duration    `json:"timeout"`
	Retries     int              `json:"retries"`
	Enabled     bool             `json:"enabled"`
}

func projectHealthCheck(v health.HealthCheck) healthCheckDTO {
	return healthCheckDTO{
		entityDTO:   projectEntity(v.Entity),
		TenantID:    v.TenantID,
		InstanceID:  v.InstanceID,
		ServiceName: v.ServiceName,
		Name:        v.Name,
		Type:        v.Type,
		Target:      v.Target,
		Interval:    v.Interval,
		Timeout:     v.Timeout,
		Retries:     v.Retries,
		Enabled:     v.Enabled,
	}
}

type healthResultDTO struct {
	entityDTO

	CheckID    id.ID         `json:"check_id"`
	InstanceID id.ID         `json:"instance_id"`
	TenantID   string        `json:"tenant_id"`
	Status     health.Status `json:"status"`
	Latency    time.Duration `json:"latency"`
	Message    string        `json:"message,omitempty"`
	StatusCode int           `json:"status_code,omitempty"`
	CheckedAt  time.Time     `json:"checked_at"`
}

func projectHealthResult(v health.HealthResult) healthResultDTO {
	return healthResultDTO{
		entityDTO:  projectEntity(v.Entity),
		CheckID:    v.CheckID,
		InstanceID: v.InstanceID,
		TenantID:   v.TenantID,
		Status:     v.Status,
		Latency:    v.Latency,
		Message:    safeObservation(v.Message),
		StatusCode: v.StatusCode,
		CheckedAt:  v.CheckedAt.UTC(),
	}
}

type instanceHealthDTO struct {
	InstanceID  id.ID             `json:"instance_id"`
	Status      health.Status     `json:"status"`
	Checks      []checkSummaryDTO `json:"checks"`
	LastChecked *time.Time        `json:"last_checked"`
	Uptime      *float64          `json:"uptime_percent"`
	ConsecFails *int              `json:"consecutive_failures"`
}

func projectInstanceHealth(v health.InstanceHealth) instanceHealthDTO {
	return instanceHealthDTO{
		InstanceID:  v.InstanceID,
		Status:      v.Status,
		Checks:      viewSlice(v.Checks, projectCheckSummary),
		LastChecked: observedTime(v.LastChecked),
		Uptime:      nil,
		ConsecFails: nil,
	}
}

type checkSummaryDTO struct {
	CheckID    id.ID            `json:"check_id"`
	Name       string           `json:"name"`
	Status     health.Status    `json:"status"`
	Latency    time.Duration    `json:"latency"`
	LastResult *healthResultDTO `json:"last_result,omitempty"`
}

func projectCheckSummary(v health.CheckSummary) checkSummaryDTO {
	return checkSummaryDTO{
		CheckID:    v.CheckID,
		Name:       v.Name,
		Status:     v.Status,
		Latency:    v.Latency,
		LastResult: viewPointer(v.LastResult, projectHealthResult),
	}
}

type secretDTO struct {
	entityDTO

	TenantID   string             `json:"tenant_id"`
	InstanceID id.ID              `json:"instance_id"`
	Key        string             `json:"key"`
	Type       secrets.SecretType `json:"type"`
	Version    int                `json:"version"`
}

func projectSecret(v secrets.Secret) secretDTO {
	return secretDTO{
		entityDTO:  projectEntity(v.Entity),
		TenantID:   v.TenantID,
		InstanceID: v.InstanceID,
		Key:        v.Key,
		Type:       v.Type,
		Version:    v.Version,
	}
}

type bootstrapWorkloadDTO struct {
	entityDTO

	DatacenterID id.ID                 `json:"datacenter_id"`
	Name         string                `json:"name"`
	Kind         provider.WorkloadKind `json:"kind"`
	Services     []serviceSpecDTO      `json:"services"`
	State        bootstrap.State       `json:"state"`
	ProviderRef  string                `json:"provider_ref,omitempty"`
	ServiceRefs  map[string]string     `json:"service_refs,omitempty"`
	LastError    string                `json:"last_error,omitempty"`
	Attempts     int                   `json:"attempts"`
	Labels       map[string]string     `json:"labels,omitempty"`
}

func projectBootstrapWorkload(v bootstrap.BootstrapWorkload) bootstrapWorkloadDTO {
	return bootstrapWorkloadDTO{
		entityDTO:    projectEntity(v.Entity),
		DatacenterID: v.DatacenterID,
		Name:         v.Name,
		Kind:         v.Kind,
		Services:     viewSlice(v.Services, projectServiceSpec),
		State:        v.State,
		ProviderRef:  v.ProviderRef,
		ServiceRefs:  v.ServiceRefs,
		LastError:    safeDiagnostic(v.LastError),
		Attempts:     v.Attempts,
		Labels:       v.Labels,
	}
}

type tenantDTO struct {
	entityDTO

	ExternalID  string             `json:"external_id,omitempty"`
	Name        string             `json:"name"`
	Slug        string             `json:"slug"`
	Status      admin.TenantStatus `json:"status"`
	Plan        string             `json:"plan"`
	Quota       quotaDTO           `json:"quota"`
	SuspendedAt *time.Time         `json:"suspended_at,omitempty"`
	Metadata    map[string]string  `json:"metadata,omitempty"`
}

func projectTenant(v admin.Tenant) tenantDTO {
	return tenantDTO{
		entityDTO:   projectEntity(v.Entity),
		ExternalID:  v.ExternalID,
		Name:        v.Name,
		Slug:        v.Slug,
		Status:      v.Status,
		Plan:        v.Plan,
		Quota:       projectQuota(v.Quota),
		SuspendedAt: viewPointer(v.SuspendedAt, func(t time.Time) time.Time { return t.UTC() }),
		Metadata:    v.Metadata,
	}
}

type quotaDTO struct {
	MaxInstances int `json:"max_instances"`
	MaxCPUMillis int `json:"max_cpu_millis"`
	MaxMemoryMB  int `json:"max_memory_mb"`
	MaxDiskMB    int `json:"max_disk_mb"`
	MaxDomains   int `json:"max_domains"`
	MaxSecrets   int `json:"max_secrets"`
}

func projectQuota(v admin.Quota) quotaDTO {
	return quotaDTO{
		MaxInstances: v.MaxInstances,
		MaxCPUMillis: v.MaxCPUMillis,
		MaxMemoryMB:  v.MaxMemoryMB,
		MaxDiskMB:    v.MaxDiskMB,
		MaxDomains:   v.MaxDomains,
		MaxSecrets:   v.MaxSecrets,
	}
}

type quotaUsageDTO struct {
	Tenant *tenantDTO       `json:"tenant"`
	Quota  quotaDTO         `json:"quota"`
	Used   quotaSnapshotDTO `json:"used"`
}

func projectQuotaUsage(v admin.QuotaUsage) quotaUsageDTO {
	return quotaUsageDTO{
		Tenant: viewPointer(v.Tenant, projectTenant),
		Quota:  projectQuota(v.Quota),
		Used:   projectQuotaSnapshot(v.Used),
	}
}

type quotaSnapshotDTO struct {
	Instances int `json:"instances"`
	CPUMillis int `json:"cpu_millis"`
	MemoryMB  int `json:"memory_mb"`
	DiskMB    int `json:"disk_mb"`
	Domains   int `json:"domains"`
	Secrets   int `json:"secrets"`
}

func projectQuotaSnapshot(v admin.QuotaSnapshot) quotaSnapshotDTO {
	return quotaSnapshotDTO{
		Instances: v.Instances,
		CPUMillis: v.CPUMillis,
		MemoryMB:  v.MemoryMB,
		DiskMB:    v.DiskMB,
		Domains:   v.Domains,
		Secrets:   v.Secrets,
	}
}

type auditEntryDTO struct {
	entityDTO

	TenantID   string         `json:"tenant_id"`
	ActorID    string         `json:"actor_id"`
	ActorType  string         `json:"actor_type"`
	Resource   string         `json:"resource"`
	ResourceID string         `json:"resource_id"`
	Action     string         `json:"action"`
	Details    map[string]any `json:"details,omitempty"`
	IPAddress  string         `json:"ip_address,omitempty"`
}

func projectAuditEntry(v admin.AuditEntry) auditEntryDTO {
	return auditEntryDTO{
		entityDTO:  projectEntity(v.Entity),
		TenantID:   v.TenantID,
		ActorID:    v.ActorID,
		ActorType:  v.ActorType,
		Resource:   v.Resource,
		ResourceID: v.ResourceID,
		Action:     v.Action,
		Details:    safeMetadata(v.Details),
		IPAddress:  v.IPAddress,
	}
}

type providerHealthResultDTO struct {
	Name      string        `json:"name"`
	Healthy   bool          `json:"healthy"`
	Message   string        `json:"message"`
	Latency   time.Duration `json:"latency"`
	CheckedAt time.Time     `json:"checked_at"`
}

func projectProviderHealthResult(v admin.ProviderHealthResult) providerHealthResultDTO {
	return providerHealthResultDTO{
		Name:      v.Name,
		Healthy:   v.Healthy,
		Message:   safeObservation(v.Message),
		Latency:   v.Latency,
		CheckedAt: v.CheckedAt.UTC(),
	}
}

type providerLocationDTO struct {
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`
	Country   string  `json:"country,omitempty"`
	City      string  `json:"city,omitempty"`
}

func projectProviderLocation(v admin.ProviderLocation) providerLocationDTO {
	return providerLocationDTO{
		Latitude:  v.Latitude,
		Longitude: v.Longitude,
		Country:   v.Country,
		City:      v.City,
	}
}

type workerInfoDTO struct {
	Name     string        `json:"name"`
	Interval time.Duration `json:"interval"`
	Running  bool          `json:"running"`
	LastRun  *time.Time    `json:"last_run,omitempty"`
	LastErr  string        `json:"last_error,omitempty"`
	RunCount int64         `json:"run_count"`
}

func projectWorkerInfo(v worker.WorkerInfo) workerInfoDTO {
	return workerInfoDTO{
		Name:     v.Name,
		Interval: v.Interval,
		Running:  v.Running,
		LastRun:  viewPointer(v.LastRun, func(t time.Time) time.Time { return t.UTC() }),
		LastErr:  safeDiagnostic(v.LastErr),
		RunCount: v.RunCount,
	}
}

type dashboardDataDTO struct {
	InstanceID id.ID                `json:"instance_id"`
	Resources  *resourceSnapshotDTO `json:"resources"`
}

func projectDashboardData(v telemetry.DashboardData) dashboardDataDTO {
	return dashboardDataDTO{
		InstanceID: v.InstanceID,
		Resources:  viewPointer(v.Resources, projectResourceSnapshot),
	}
}

type resourceSnapshotDTO struct {
	InstanceID    id.ID     `json:"instance_id"`
	TenantID      string    `json:"tenant_id"`
	CPUPercent    float64   `json:"cpu_percent"`
	MemoryUsedMB  int       `json:"memory_used_mb"`
	MemoryLimitMB int       `json:"memory_limit_mb"`
	DiskUsedMB    int       `json:"disk_used_mb"`
	NetworkInMB   float64   `json:"network_in_mb"`
	NetworkOutMB  float64   `json:"network_out_mb"`
	Timestamp     time.Time `json:"timestamp"`
}

func projectResourceSnapshot(v telemetry.ResourceSnapshot) resourceSnapshotDTO {
	return resourceSnapshotDTO{
		InstanceID:    v.InstanceID,
		TenantID:      v.TenantID,
		CPUPercent:    v.CPUPercent,
		MemoryUsedMB:  v.MemoryUsedMB,
		MemoryLimitMB: v.MemoryLimitMB,
		DiskUsedMB:    v.DiskUsedMB,
		NetworkInMB:   v.NetworkInMB,
		NetworkOutMB:  v.NetworkOutMB,
		Timestamp:     v.Timestamp.UTC(),
	}
}

type eventDTO struct {
	ID         id.ID          `json:"id"`
	Type       event.Type     `json:"type"`
	TenantID   string         `json:"tenant_id"`
	InstanceID id.ID          `json:"instance_id,omitzero"`
	ActorID    string         `json:"actor_id,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
	Timestamp  time.Time      `json:"timestamp"`
}

func projectEvent(v event.Event) eventDTO {
	return eventDTO{
		ID:         v.ID,
		Type:       v.Type,
		TenantID:   v.TenantID,
		InstanceID: v.InstanceID,
		ActorID:    v.ActorID,
		Payload:    safeMetadata(v.Payload),
		Timestamp:  v.Timestamp.UTC(),
	}
}
func viewPointer[A, B any](v *A, fn func(A) B) *B {
	if v == nil {
		return nil
	}

	out := fn(*v)

	return &out
}
func viewSlice[A, B any](v []A, fn func(A) B) []B {
	out := make([]B, 0, len(v))
	for _, item := range v {
		out = append(out, fn(item))
	}

	return out
}
func viewMap[K comparable, A, B any](v map[K]A, fn func(A) B) map[K]B {
	if v == nil {
		return nil
	}

	out := make(map[K]B, len(v))
	for k, item := range v {
		out[k] = fn(item)
	}

	return out
}
func projectResponse(v any) (any, error) {
	switch v := v.(type) {
	case instance.Instance:
		return projectInstance(v), nil
	case *instance.Instance:
		return viewPointer(v, projectInstance), nil
	case []instance.Instance:
		return viewSlice(v, projectInstance), nil
	case []*instance.Instance:
		return viewSlice(v, func(v *instance.Instance) *instanceDTO { return viewPointer(v, projectInstance) }), nil
	case workload.Workload:
		return projectWorkload(v), nil
	case *workload.Workload:
		return viewPointer(v, projectWorkload), nil
	case []workload.Workload:
		return viewSlice(v, projectWorkload), nil
	case []*workload.Workload:
		return viewSlice(v, func(v *workload.Workload) *workloadDTO { return viewPointer(v, projectWorkload) }), nil
	case workload.WorkloadHealth:
		return projectWorkloadHealth(v), nil
	case *workload.WorkloadHealth:
		return viewPointer(v, projectWorkloadHealth), nil
	case []workload.WorkloadHealth:
		return viewSlice(v, projectWorkloadHealth), nil
	case []*workload.WorkloadHealth:
		return viewSlice(v, func(v *workload.WorkloadHealth) *workloadHealthDTO { return viewPointer(v, projectWorkloadHealth) }), nil
	case template.Template:
		return projectTemplate(v), nil
	case *template.Template:
		return viewPointer(v, projectTemplate), nil
	case []template.Template:
		return viewSlice(v, projectTemplate), nil
	case []*template.Template:
		return viewSlice(v, func(v *template.Template) *templateDTO { return viewPointer(v, projectTemplate) }), nil
	case datacenter.Datacenter:
		return projectDatacenter(v), nil
	case *datacenter.Datacenter:
		return viewPointer(v, projectDatacenter), nil
	case []datacenter.Datacenter:
		return viewSlice(v, projectDatacenter), nil
	case []*datacenter.Datacenter:
		return viewSlice(v, func(v *datacenter.Datacenter) *datacenterDTO { return viewPointer(v, projectDatacenter) }), nil
	case deploy.Deployment:
		return projectDeployment(v), nil
	case *deploy.Deployment:
		return viewPointer(v, projectDeployment), nil
	case []deploy.Deployment:
		return viewSlice(v, projectDeployment), nil
	case []*deploy.Deployment:
		return viewSlice(v, func(v *deploy.Deployment) *deploymentDTO { return viewPointer(v, projectDeployment) }), nil
	case deploy.Release:
		return projectRelease(v), nil
	case *deploy.Release:
		return viewPointer(v, projectRelease), nil
	case []deploy.Release:
		return viewSlice(v, projectRelease), nil
	case []*deploy.Release:
		return viewSlice(v, func(v *deploy.Release) *releaseDTO { return viewPointer(v, projectRelease) }), nil
	case network.Domain:
		return projectDomain(v), nil
	case *network.Domain:
		return viewPointer(v, projectDomain), nil
	case []network.Domain:
		return viewSlice(v, projectDomain), nil
	case []*network.Domain:
		return viewSlice(v, func(v *network.Domain) *domainDTO { return viewPointer(v, projectDomain) }), nil
	case network.Route:
		return projectRoute(v), nil
	case *network.Route:
		return viewPointer(v, projectRoute), nil
	case []network.Route:
		return viewSlice(v, projectRoute), nil
	case []*network.Route:
		return viewSlice(v, func(v *network.Route) *routeDTO { return viewPointer(v, projectRoute) }), nil
	case network.Certificate:
		return projectCertificate(v), nil
	case *network.Certificate:
		return viewPointer(v, projectCertificate), nil
	case []network.Certificate:
		return viewSlice(v, projectCertificate), nil
	case []*network.Certificate:
		return viewSlice(v, func(v *network.Certificate) *certificateDTO { return viewPointer(v, projectCertificate) }), nil
	case health.HealthCheck:
		return projectHealthCheck(v), nil
	case *health.HealthCheck:
		return viewPointer(v, projectHealthCheck), nil
	case []health.HealthCheck:
		return viewSlice(v, projectHealthCheck), nil
	case []*health.HealthCheck:
		return viewSlice(v, func(v *health.HealthCheck) *healthCheckDTO { return viewPointer(v, projectHealthCheck) }), nil
	case health.HealthResult:
		return projectHealthResult(v), nil
	case *health.HealthResult:
		return viewPointer(v, projectHealthResult), nil
	case []health.HealthResult:
		return viewSlice(v, projectHealthResult), nil
	case []*health.HealthResult:
		return viewSlice(v, func(v *health.HealthResult) *healthResultDTO { return viewPointer(v, projectHealthResult) }), nil
	case health.InstanceHealth:
		return projectInstanceHealth(v), nil
	case *health.InstanceHealth:
		return viewPointer(v, projectInstanceHealth), nil
	case []health.InstanceHealth:
		return viewSlice(v, projectInstanceHealth), nil
	case []*health.InstanceHealth:
		return viewSlice(v, func(v *health.InstanceHealth) *instanceHealthDTO { return viewPointer(v, projectInstanceHealth) }), nil
	case secrets.Secret:
		return projectSecret(v), nil
	case *secrets.Secret:
		return viewPointer(v, projectSecret), nil
	case []secrets.Secret:
		return viewSlice(v, projectSecret), nil
	case []*secrets.Secret:
		return viewSlice(v, func(v *secrets.Secret) *secretDTO { return viewPointer(v, projectSecret) }), nil
	case bootstrap.BootstrapWorkload:
		return projectBootstrapWorkload(v), nil
	case *bootstrap.BootstrapWorkload:
		return viewPointer(v, projectBootstrapWorkload), nil
	case []bootstrap.BootstrapWorkload:
		return viewSlice(v, projectBootstrapWorkload), nil
	case []*bootstrap.BootstrapWorkload:
		return viewSlice(v, func(v *bootstrap.BootstrapWorkload) *bootstrapWorkloadDTO {
			return viewPointer(v, projectBootstrapWorkload)
		}), nil
	case admin.Tenant:
		return projectTenant(v), nil
	case *admin.Tenant:
		return viewPointer(v, projectTenant), nil
	case []admin.Tenant:
		return viewSlice(v, projectTenant), nil
	case []*admin.Tenant:
		return viewSlice(v, func(v *admin.Tenant) *tenantDTO { return viewPointer(v, projectTenant) }), nil
	case admin.QuotaUsage:
		return projectQuotaUsage(v), nil
	case *admin.QuotaUsage:
		return viewPointer(v, projectQuotaUsage), nil
	case []admin.QuotaUsage:
		return viewSlice(v, projectQuotaUsage), nil
	case []*admin.QuotaUsage:
		return viewSlice(v, func(v *admin.QuotaUsage) *quotaUsageDTO { return viewPointer(v, projectQuotaUsage) }), nil
	case admin.AuditEntry:
		return projectAuditEntry(v), nil
	case *admin.AuditEntry:
		return viewPointer(v, projectAuditEntry), nil
	case []admin.AuditEntry:
		return viewSlice(v, projectAuditEntry), nil
	case []*admin.AuditEntry:
		return viewSlice(v, func(v *admin.AuditEntry) *auditEntryDTO { return viewPointer(v, projectAuditEntry) }), nil
	case admin.ProviderHealthResult:
		return projectProviderHealthResult(v), nil
	case *admin.ProviderHealthResult:
		return viewPointer(v, projectProviderHealthResult), nil
	case []admin.ProviderHealthResult:
		return viewSlice(v, projectProviderHealthResult), nil
	case []*admin.ProviderHealthResult:
		return viewSlice(v, func(v *admin.ProviderHealthResult) *providerHealthResultDTO {
			return viewPointer(v, projectProviderHealthResult)
		}), nil
	case admin.ProviderLocation:
		return projectProviderLocation(v), nil
	case *admin.ProviderLocation:
		return viewPointer(v, projectProviderLocation), nil
	case []admin.ProviderLocation:
		return viewSlice(v, projectProviderLocation), nil
	case []*admin.ProviderLocation:
		return viewSlice(v, func(v *admin.ProviderLocation) *providerLocationDTO { return viewPointer(v, projectProviderLocation) }), nil
	case worker.WorkerInfo:
		return projectWorkerInfo(v), nil
	case *worker.WorkerInfo:
		return viewPointer(v, projectWorkerInfo), nil
	case []worker.WorkerInfo:
		return viewSlice(v, projectWorkerInfo), nil
	case []*worker.WorkerInfo:
		return viewSlice(v, func(v *worker.WorkerInfo) *workerInfoDTO { return viewPointer(v, projectWorkerInfo) }), nil
	case telemetry.DashboardData:
		return projectDashboardData(v), nil
	case *telemetry.DashboardData:
		return viewPointer(v, projectDashboardData), nil
	case []telemetry.DashboardData:
		return viewSlice(v, projectDashboardData), nil
	case []*telemetry.DashboardData:
		return viewSlice(v, func(v *telemetry.DashboardData) *dashboardDataDTO { return viewPointer(v, projectDashboardData) }), nil
	case event.Event:
		return projectEvent(v), nil
	case *event.Event:
		return viewPointer(v, projectEvent), nil
	case []event.Event:
		return viewSlice(v, projectEvent), nil
	case []*event.Event:
		return viewSlice(v, func(v *event.Event) *eventDTO { return viewPointer(v, projectEvent) }), nil
	default:
		return projectCollection(v)
	}
}
