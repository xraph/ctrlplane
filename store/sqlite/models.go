package sqlite

import (
	"time"

	"github.com/xraph/grove"

	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/network"
	"github.com/xraph/ctrlplane/secrets"
	"github.com/xraph/ctrlplane/telemetry"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/provider"
)

// tenantModel is the database model for admin.Tenant.
type tenantModel struct {
	grove.BaseModel `grove:"table:cp_tenants"`

	ID         id.ID     `grove:"id,pk"`
	ExternalID string    `grove:"external_id"`
	Slug       string    `grove:"slug,unique,notnull"`
	Name       string    `grove:"name,notnull"`
	Status     string    `grove:"status,notnull"`
	Metadata   []byte    `grove:"metadata"`
	CreatedAt  time.Time `grove:"created_at,notnull"`
	UpdatedAt  time.Time `grove:"updated_at,notnull"`
}

// instanceModel is the database model for instance.Instance.
type instanceModel struct {
	grove.BaseModel `grove:"table:cp_instances"`

	ID           string    `grove:"id,pk"`
	TenantID     string    `grove:"tenant_id,notnull"`
	Slug         string    `grove:"slug,notnull"`
	Name         string    `grove:"name,notnull"`
	State        string    `grove:"state,notnull"`
	ProviderName string    `grove:"provider_name,notnull"`
	ProviderRef  string    `grove:"provider_ref"`
	Region       string    `grove:"region"`
	Image        string    `grove:"image"`
	Config       []byte    `grove:"config"`
	Metadata     []byte    `grove:"metadata"`
	CreatedAt    time.Time `grove:"created_at,notnull"`
	UpdatedAt    time.Time `grove:"updated_at,notnull"`
}

// deploymentModel is the database model for deploy.Deployment.
type deploymentModel struct {
	grove.BaseModel `grove:"table:cp_deployments"`

	ID          string     `grove:"id,pk"`
	TenantID    string     `grove:"tenant_id,notnull"`
	InstanceID  string     `grove:"instance_id,notnull"`
	ReleaseID   string     `grove:"release_id,notnull"`
	State       string     `grove:"state,notnull"`
	Strategy    string     `grove:"strategy,notnull"`
	Image       string     `grove:"image"`
	ProviderRef string     `grove:"provider_ref"`
	Error       string     `grove:"error"`
	Initiator   string     `grove:"initiator"`
	StartedAt   *time.Time `grove:"started_at"`
	FinishedAt  *time.Time `grove:"finished_at"`
	CreatedAt   time.Time  `grove:"created_at,notnull"`
	UpdatedAt   time.Time  `grove:"updated_at,notnull"`
}

// releaseModel is the database model for deploy.Release.
type releaseModel struct {
	grove.BaseModel `grove:"table:cp_releases"`

	ID         string    `grove:"id,pk"`
	TenantID   string    `grove:"tenant_id,notnull"`
	InstanceID string    `grove:"instance_id,notnull"`
	Version    int       `grove:"version,notnull"`
	Image      string    `grove:"image,notnull"`
	Notes      string    `grove:"notes"`
	CommitSHA  string    `grove:"commit_sha"`
	Active     bool      `grove:"active"`
	Config     []byte    `grove:"config"`
	Metadata   []byte    `grove:"metadata"`
	CreatedAt  time.Time `grove:"created_at,notnull"`
}

// healthCheckModel is the database model for health.HealthCheck.
type healthCheckModel struct {
	grove.BaseModel `grove:"table:cp_health_checks"`

	ID         string    `grove:"id,pk"`
	TenantID   string    `grove:"tenant_id,notnull"`
	InstanceID string    `grove:"instance_id,notnull"`
	Name       string    `grove:"name,notnull"`
	Type       string    `grove:"type,notnull"`
	Enabled    bool      `grove:"enabled,notnull"`
	Interval   int64     `grove:"interval,notnull"`
	Timeout    int64     `grove:"timeout,notnull"`
	Config     []byte    `grove:"config"`
	CreatedAt  time.Time `grove:"created_at,notnull"`
	UpdatedAt  time.Time `grove:"updated_at,notnull"`
}

// healthResultModel is the database model for health.HealthResult.
type healthResultModel struct {
	grove.BaseModel `grove:"table:cp_health_results"`

	ID         int64     `grove:"id,pk,autoincrement"`
	CheckID    string    `grove:"check_id,notnull"`
	InstanceID string    `grove:"instance_id,notnull"`
	TenantID   string    `grove:"tenant_id,notnull"`
	Status     string    `grove:"status,notnull"`
	Latency    int64     `grove:"latency,notnull"`
	Message    string    `grove:"message"`
	StatusCode int       `grove:"status_code"`
	CheckedAt  time.Time `grove:"checked_at,notnull"`
}

// metricModel is the database model for telemetry.Metric.
type metricModel struct {
	grove.BaseModel `grove:"table:cp_metrics"`

	ID         int64     `grove:"id,pk,autoincrement"`
	TenantID   string    `grove:"tenant_id,notnull"`
	InstanceID string    `grove:"instance_id,notnull"`
	Name       string    `grove:"name,notnull"`
	Type       string    `grove:"type"`
	Value      float64   `grove:"value,notnull"`
	Labels     []byte    `grove:"labels"`
	Timestamp  time.Time `grove:"timestamp,notnull"`
}

// logEntryModel is the database model for telemetry.LogEntry.
type logEntryModel struct {
	grove.BaseModel `grove:"table:cp_logs"`

	ID         int64     `grove:"id,pk,autoincrement"`
	TenantID   string    `grove:"tenant_id,notnull"`
	InstanceID string    `grove:"instance_id,notnull"`
	Level      string    `grove:"level,notnull"`
	Message    string    `grove:"message,notnull"`
	Source     string    `grove:"source"`
	Attributes []byte    `grove:"attributes"`
	Timestamp  time.Time `grove:"timestamp,notnull"`
}

// traceModel is the database model for telemetry.Trace.
type traceModel struct {
	grove.BaseModel `grove:"table:cp_traces"`

	ID         int64     `grove:"id,pk,autoincrement"`
	TenantID   string    `grove:"tenant_id,notnull"`
	InstanceID string    `grove:"instance_id,notnull"`
	TraceID    string    `grove:"trace_id,notnull"`
	SpanID     string    `grove:"span_id,notnull"`
	ParentID   string    `grove:"parent_id"`
	Operation  string    `grove:"operation,notnull"`
	Duration   int64     `grove:"duration,notnull"`
	Status     string    `grove:"status"`
	Attributes []byte    `grove:"attributes"`
	Timestamp  time.Time `grove:"timestamp,notnull"`
}

// resourceSnapshotModel is the database model for telemetry.ResourceSnapshot.
type resourceSnapshotModel struct {
	grove.BaseModel `grove:"table:cp_resource_snapshots"`

	ID            int64     `grove:"id,pk,autoincrement"`
	TenantID      string    `grove:"tenant_id,notnull"`
	InstanceID    string    `grove:"instance_id,notnull"`
	CPUPercent    float64   `grove:"cpu_percent,notnull"`
	MemoryUsedMB  int       `grove:"memory_used_mb,notnull"`
	MemoryLimitMB int       `grove:"memory_limit_mb,notnull"`
	DiskUsedMB    int       `grove:"disk_used_mb,notnull"`
	NetworkInMB   float64   `grove:"network_in_mb,notnull"`
	NetworkOutMB  float64   `grove:"network_out_mb,notnull"`
	Timestamp     time.Time `grove:"timestamp,notnull"`
}

// domainModel is the database model for network.Domain.
type domainModel struct {
	grove.BaseModel `grove:"table:cp_domains"`

	ID          string     `grove:"id,pk"`
	TenantID    string     `grove:"tenant_id,notnull"`
	InstanceID  string     `grove:"instance_id,notnull"`
	Hostname    string     `grove:"hostname,unique,notnull"`
	Verified    bool       `grove:"verified,notnull"`
	TLSEnabled  bool       `grove:"tls_enabled,notnull"`
	CertExpiry  *time.Time `grove:"cert_expiry"`
	DNSTarget   string     `grove:"dns_target"`
	VerifyToken string     `grove:"verify_token"`
	CreatedAt   time.Time  `grove:"created_at,notnull"`
	UpdatedAt   time.Time  `grove:"updated_at,notnull"`
}

// routeModel is the database model for network.Route.
type routeModel struct {
	grove.BaseModel `grove:"table:cp_routes"`

	ID          string    `grove:"id,pk"`
	TenantID    string    `grove:"tenant_id,notnull"`
	InstanceID  string    `grove:"instance_id,notnull"`
	Path        string    `grove:"path,notnull"`
	Port        int       `grove:"port,notnull"`
	Protocol    string    `grove:"protocol"`
	Weight      int       `grove:"weight"`
	StripPrefix bool      `grove:"strip_prefix"`
	CreatedAt   time.Time `grove:"created_at,notnull"`
	UpdatedAt   time.Time `grove:"updated_at,notnull"`
}

// certificateModel is the database model for network.Certificate.
type certificateModel struct {
	grove.BaseModel `grove:"table:cp_certificates"`

	ID        string    `grove:"id,pk"`
	DomainID  string    `grove:"domain_id,notnull"`
	TenantID  string    `grove:"tenant_id,notnull"`
	Issuer    string    `grove:"issuer,notnull"`
	ExpiresAt time.Time `grove:"expires_at,notnull"`
	AutoRenew bool      `grove:"auto_renew,notnull"`
	CreatedAt time.Time `grove:"created_at,notnull"`
	UpdatedAt time.Time `grove:"updated_at,notnull"`
}

// secretModel is the database model for secrets.Secret.
type secretModel struct {
	grove.BaseModel `grove:"table:cp_secrets"`

	ID         int64     `grove:"id,pk,autoincrement"`
	TenantID   string    `grove:"tenant_id,notnull"`
	InstanceID string    `grove:"instance_id,notnull"`
	Key        string    `grove:"key,notnull"`
	Value      []byte    `grove:"value,notnull"`
	CreatedAt  time.Time `grove:"created_at,notnull"`
	UpdatedAt  time.Time `grove:"updated_at,notnull"`
}

// auditEntryModel is the database model for admin.AuditEntry.
type auditEntryModel struct {
	grove.BaseModel `grove:"table:cp_audit_entries"`

	ID         int64     `grove:"id,pk,autoincrement"`
	TenantID   string    `grove:"tenant_id,notnull"`
	ActorID    string    `grove:"actor_id,notnull"`
	Action     string    `grove:"action,notnull"`
	Resource   string    `grove:"resource"`
	ResourceID string    `grove:"resource_id"`
	Details    []byte    `grove:"details"`
	CreatedAt  time.Time `grove:"created_at,notnull"`
}

// --- Conversion helpers ---

func toInstanceModel(inst *instance.Instance) *instanceModel {
	return &instanceModel{
		ID:           inst.ID.String(),
		TenantID:     inst.TenantID,
		Slug:         inst.Slug,
		Name:         inst.Name,
		State:        string(inst.State),
		ProviderName: inst.ProviderName,
		ProviderRef:  inst.ProviderRef,
		Region:       inst.Region,
		Image:        inst.Image,
		CreatedAt:    inst.CreatedAt,
		UpdatedAt:    inst.UpdatedAt,
	}
}

func fromInstanceModel(m *instanceModel) *instance.Instance {
	return &instance.Instance{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		TenantID:     m.TenantID,
		Slug:         m.Slug,
		Name:         m.Name,
		State:        provider.InstanceState(m.State),
		ProviderName: m.ProviderName,
		ProviderRef:  m.ProviderRef,
		Region:       m.Region,
		Image:        m.Image,
	}
}

func toDeploymentModel(d *deploy.Deployment) *deploymentModel {
	return &deploymentModel{
		ID:          d.ID.String(),
		TenantID:    d.TenantID,
		InstanceID:  d.InstanceID.String(),
		ReleaseID:   d.ReleaseID.String(),
		State:       string(d.State),
		Strategy:    d.Strategy,
		Image:       d.Image,
		ProviderRef: d.ProviderRef,
		Error:       d.Error,
		Initiator:   d.Initiator,
		StartedAt:   d.StartedAt,
		FinishedAt:  d.FinishedAt,
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	}
}

func fromDeploymentModel(m *deploymentModel) *deploy.Deployment {
	return &deploy.Deployment{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		TenantID:    m.TenantID,
		InstanceID:  id.MustParse(m.InstanceID),
		ReleaseID:   id.MustParse(m.ReleaseID),
		State:       deploy.DeployState(m.State),
		Strategy:    m.Strategy,
		Image:       m.Image,
		ProviderRef: m.ProviderRef,
		Error:       m.Error,
		Initiator:   m.Initiator,
		StartedAt:   m.StartedAt,
		FinishedAt:  m.FinishedAt,
	}
}

func toReleaseModel(r *deploy.Release) *releaseModel {
	return &releaseModel{
		ID:         r.ID.String(),
		TenantID:   r.TenantID,
		InstanceID: r.InstanceID.String(),
		Version:    r.Version,
		Image:      r.Image,
		Notes:      r.Notes,
		CommitSHA:  r.CommitSHA,
		Active:     r.Active,
		CreatedAt:  r.CreatedAt,
	}
}

func fromReleaseModel(m *releaseModel) *deploy.Release {
	return &deploy.Release{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
		},
		TenantID:   m.TenantID,
		InstanceID: id.MustParse(m.InstanceID),
		Version:    m.Version,
		Image:      m.Image,
		Notes:      m.Notes,
		CommitSHA:  m.CommitSHA,
		Active:     m.Active,
	}
}

func toHealthCheckModel(check *health.HealthCheck) *healthCheckModel {
	return &healthCheckModel{
		ID:         check.ID.String(),
		TenantID:   check.TenantID,
		InstanceID: check.InstanceID.String(),
		Name:       check.Name,
		Type:       string(check.Type),
		Enabled:    check.Enabled,
		Interval:   int64(check.Interval),
		Timeout:    int64(check.Timeout),
		CreatedAt:  check.CreatedAt,
		UpdatedAt:  check.UpdatedAt,
	}
}

func toHealthResultModel(result *health.HealthResult) *healthResultModel {
	return &healthResultModel{
		CheckID:    result.CheckID.String(),
		InstanceID: result.InstanceID.String(),
		TenantID:   result.TenantID,
		Status:     string(result.Status),
		Latency:    int64(result.Latency),
		Message:    result.Message,
		StatusCode: result.StatusCode,
		CheckedAt:  result.CheckedAt,
	}
}

func toMetricModel(m *telemetry.Metric) *metricModel {
	return &metricModel{
		TenantID:   m.TenantID,
		InstanceID: m.InstanceID.String(),
		Name:       m.Name,
		Type:       string(m.Type),
		Value:      m.Value,
		Timestamp:  m.Timestamp,
	}
}

func toLogEntryModel(log *telemetry.LogEntry) *logEntryModel {
	return &logEntryModel{
		TenantID:   log.TenantID,
		InstanceID: log.InstanceID.String(),
		Level:      log.Level,
		Message:    log.Message,
		Source:     log.Source,
		Timestamp:  log.Timestamp,
	}
}

func toTraceModel(trace *telemetry.Trace) *traceModel {
	return &traceModel{
		TenantID:   trace.TenantID,
		InstanceID: trace.InstanceID.String(),
		TraceID:    trace.TraceID,
		SpanID:     trace.SpanID,
		ParentID:   trace.ParentID,
		Operation:  trace.Operation,
		Duration:   int64(trace.Duration),
		Status:     trace.Status,
		Timestamp:  trace.Timestamp,
	}
}

func toResourceSnapshotModel(snap *telemetry.ResourceSnapshot) *resourceSnapshotModel {
	return &resourceSnapshotModel{
		TenantID:      snap.TenantID,
		InstanceID:    snap.InstanceID.String(),
		CPUPercent:    snap.CPUPercent,
		MemoryUsedMB:  snap.MemoryUsedMB,
		MemoryLimitMB: snap.MemoryLimitMB,
		DiskUsedMB:    snap.DiskUsedMB,
		NetworkInMB:   snap.NetworkInMB,
		NetworkOutMB:  snap.NetworkOutMB,
		Timestamp:     snap.Timestamp,
	}
}

func toDomainModel(domain *network.Domain) *domainModel {
	return &domainModel{
		ID:          domain.ID.String(),
		TenantID:    domain.TenantID,
		InstanceID:  domain.InstanceID.String(),
		Hostname:    domain.Hostname,
		Verified:    domain.Verified,
		TLSEnabled:  domain.TLSEnabled,
		CertExpiry:  domain.CertExpiry,
		DNSTarget:   domain.DNSTarget,
		VerifyToken: domain.VerifyToken,
		CreatedAt:   domain.CreatedAt,
		UpdatedAt:   domain.UpdatedAt,
	}
}

func fromDomainModel(m *domainModel) *network.Domain {
	return &network.Domain{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		TenantID:    m.TenantID,
		InstanceID:  id.MustParse(m.InstanceID),
		Hostname:    m.Hostname,
		Verified:    m.Verified,
		TLSEnabled:  m.TLSEnabled,
		CertExpiry:  m.CertExpiry,
		DNSTarget:   m.DNSTarget,
		VerifyToken: m.VerifyToken,
	}
}

func toRouteModel(route *network.Route) *routeModel {
	return &routeModel{
		ID:          route.ID.String(),
		TenantID:    route.TenantID,
		InstanceID:  route.InstanceID.String(),
		Path:        route.Path,
		Port:        route.Port,
		Protocol:    route.Protocol,
		Weight:      route.Weight,
		StripPrefix: route.StripPrefix,
		CreatedAt:   route.CreatedAt,
		UpdatedAt:   route.UpdatedAt,
	}
}

func fromRouteModel(m *routeModel) *network.Route {
	return &network.Route{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		TenantID:    m.TenantID,
		InstanceID:  id.MustParse(m.InstanceID),
		Path:        m.Path,
		Port:        m.Port,
		Protocol:    m.Protocol,
		Weight:      m.Weight,
		StripPrefix: m.StripPrefix,
	}
}

func toCertificateModel(cert *network.Certificate) *certificateModel {
	return &certificateModel{
		ID:        cert.ID.String(),
		DomainID:  cert.DomainID.String(),
		TenantID:  cert.TenantID,
		Issuer:    cert.Issuer,
		ExpiresAt: cert.ExpiresAt,
		AutoRenew: cert.AutoRenew,
		CreatedAt: cert.CreatedAt,
		UpdatedAt: cert.UpdatedAt,
	}
}

func fromCertificateModel(m *certificateModel) *network.Certificate {
	return &network.Certificate{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		DomainID:  id.MustParse(m.DomainID),
		TenantID:  m.TenantID,
		Issuer:    m.Issuer,
		ExpiresAt: m.ExpiresAt,
		AutoRenew: m.AutoRenew,
	}
}

func toSecretModel(secret *secrets.Secret) *secretModel {
	return &secretModel{
		TenantID:   secret.TenantID,
		InstanceID: secret.InstanceID.String(),
		Key:        secret.Key,
		Value:      secret.Value,
		CreatedAt:  secret.CreatedAt,
		UpdatedAt:  secret.UpdatedAt,
	}
}

func toTenantModel(tenant *admin.Tenant) *tenantModel {
	return &tenantModel{
		ID:         tenant.ID,
		ExternalID: tenant.ExternalID,
		Slug:       tenant.Slug,
		Name:       tenant.Name,
		Status:     string(tenant.Status),
		CreatedAt:  tenant.CreatedAt,
		UpdatedAt:  tenant.UpdatedAt,
	}
}

func toAuditEntryModel(entry *admin.AuditEntry) *auditEntryModel {
	return &auditEntryModel{
		TenantID:   entry.TenantID,
		ActorID:    entry.ActorID,
		Action:     entry.Action,
		Resource:   entry.Resource,
		ResourceID: entry.ResourceID,
		CreatedAt:  entry.CreatedAt,
	}
}
