package bun

import (
	"time"

	bun "github.com/uptrace/bun"

	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/network"
	"github.com/xraph/ctrlplane/secrets"
	"github.com/xraph/ctrlplane/telemetry"
)

// tenantModel is the database model for admin.Tenant.
type tenantModel struct {
	bun.BaseModel `bun:"table:tenants,alias:t"`

	ID         id.ID     `bun:"id,pk"`
	ExternalID string    `bun:"external_id"`
	Slug       string    `bun:"slug,unique,notnull"`
	Name       string    `bun:"name,notnull"`
	Status     string    `bun:"status,notnull"`
	Metadata   []byte    `bun:"metadata,type:jsonb"`
	CreatedAt  time.Time `bun:"created_at,notnull"`
	UpdatedAt  time.Time `bun:"updated_at,notnull"`
}

// instanceModel is the database model for instance.Instance.
type instanceModel struct {
	bun.BaseModel `bun:"table:instances,alias:i"`

	ID           string    `bun:"id,pk"`
	TenantID     string    `bun:"tenant_id,notnull"`
	Slug         string    `bun:"slug,notnull"`
	Name         string    `bun:"name,notnull"`
	State        string    `bun:"state,notnull"`
	ProviderName string    `bun:"provider_name,notnull"`
	ProviderRef  string    `bun:"provider_ref"`
	Region       string    `bun:"region"`
	Image        string    `bun:"image"`
	Config       []byte    `bun:"config,type:jsonb"`
	Metadata     []byte    `bun:"metadata,type:jsonb"`
	CreatedAt    time.Time `bun:"created_at,notnull"`
	UpdatedAt    time.Time `bun:"updated_at,notnull"`
}

// deploymentModel is the database model for deploy.Deployment.
type deploymentModel struct {
	bun.BaseModel `bun:"table:deployments,alias:d"`

	ID          string     `bun:"id,pk"`
	TenantID    string     `bun:"tenant_id,notnull"`
	InstanceID  string     `bun:"instance_id,notnull"`
	ReleaseID   string     `bun:"release_id,notnull"`
	State       string     `bun:"state,notnull"`
	Strategy    string     `bun:"strategy,notnull"`
	Image       string     `bun:"image"`
	ProviderRef string     `bun:"provider_ref"`
	Error       string     `bun:"error"`
	Initiator   string     `bun:"initiator"`
	StartedAt   *time.Time `bun:"started_at"`
	FinishedAt  *time.Time `bun:"finished_at"`
	CreatedAt   time.Time  `bun:"created_at,notnull"`
	UpdatedAt   time.Time  `bun:"updated_at,notnull"`
}

// releaseModel is the database model for deploy.Release.
type releaseModel struct {
	bun.BaseModel `bun:"table:releases,alias:r"`

	ID         string    `bun:"id,pk"`
	TenantID   string    `bun:"tenant_id,notnull"`
	InstanceID string    `bun:"instance_id,notnull"`
	Version    int       `bun:"version,notnull"`
	Image      string    `bun:"image,notnull"`
	Notes      string    `bun:"notes"`
	CommitSHA  string    `bun:"commit_sha"`
	Active     bool      `bun:"active"`
	Config     []byte    `bun:"config,type:jsonb"`
	Metadata   []byte    `bun:"metadata,type:jsonb"`
	CreatedAt  time.Time `bun:"created_at,notnull"`
}

// healthCheckModel is the database model for health.HealthCheck.
type healthCheckModel struct {
	bun.BaseModel `bun:"table:health_checks,alias:hc"`

	ID         string    `bun:"id,pk"`
	TenantID   string    `bun:"tenant_id,notnull"`
	InstanceID string    `bun:"instance_id,notnull"`
	Name       string    `bun:"name,notnull"`
	Type       string    `bun:"type,notnull"`
	Enabled    bool      `bun:"enabled,notnull"`
	Interval   int64     `bun:"interval,notnull"`
	Timeout    int64     `bun:"timeout,notnull"`
	Config     []byte    `bun:"config,type:jsonb"`
	CreatedAt  time.Time `bun:"created_at,notnull"`
	UpdatedAt  time.Time `bun:"updated_at,notnull"`
}

// healthResultModel is the database model for health.HealthResult.
type healthResultModel struct {
	bun.BaseModel `bun:"table:health_results,alias:hr"`

	ID         int64     `bun:"id,pk,autoincrement"`
	CheckID    string    `bun:"check_id,notnull"`
	InstanceID string    `bun:"instance_id,notnull"`
	TenantID   string    `bun:"tenant_id,notnull"`
	Status     string    `bun:"status,notnull"`
	Latency    int64     `bun:"latency,notnull"`
	Message    string    `bun:"message"`
	StatusCode int       `bun:"status_code"`
	CheckedAt  time.Time `bun:"checked_at,notnull"`
}

// metricModel is the database model for telemetry.Metric.
type metricModel struct {
	bun.BaseModel `bun:"table:metrics,alias:m"`

	ID         int64     `bun:"id,pk,autoincrement"`
	TenantID   string    `bun:"tenant_id,notnull"`
	InstanceID string    `bun:"instance_id,notnull"`
	Name       string    `bun:"name,notnull"`
	Type       string    `bun:"type"`
	Value      float64   `bun:"value,notnull"`
	Labels     []byte    `bun:"labels,type:jsonb"`
	Timestamp  time.Time `bun:"timestamp,notnull"`
}

// logEntryModel is the database model for telemetry.LogEntry.
type logEntryModel struct {
	bun.BaseModel `bun:"table:logs,alias:l"`

	ID         int64     `bun:"id,pk,autoincrement"`
	TenantID   string    `bun:"tenant_id,notnull"`
	InstanceID string    `bun:"instance_id,notnull"`
	Level      string    `bun:"level,notnull"`
	Message    string    `bun:"message,notnull"`
	Source     string    `bun:"source"`
	Attributes []byte    `bun:"attributes,type:jsonb"`
	Timestamp  time.Time `bun:"timestamp,notnull"`
}

// traceModel is the database model for telemetry.Trace.
type traceModel struct {
	bun.BaseModel `bun:"table:traces,alias:tr"`

	ID         int64     `bun:"id,pk,autoincrement"`
	TenantID   string    `bun:"tenant_id,notnull"`
	InstanceID string    `bun:"instance_id,notnull"`
	TraceID    string    `bun:"trace_id,notnull"`
	SpanID     string    `bun:"span_id,notnull"`
	ParentID   string    `bun:"parent_id"`
	Operation  string    `bun:"operation,notnull"`
	Duration   int64     `bun:"duration,notnull"`
	Status     string    `bun:"status"`
	Attributes []byte    `bun:"attributes,type:jsonb"`
	Timestamp  time.Time `bun:"timestamp,notnull"`
}

// resourceSnapshotModel is the database model for telemetry.ResourceSnapshot.
type resourceSnapshotModel struct {
	bun.BaseModel `bun:"table:resource_snapshots,alias:rs"`

	ID            int64     `bun:"id,pk,autoincrement"`
	TenantID      string    `bun:"tenant_id,notnull"`
	InstanceID    string    `bun:"instance_id,notnull"`
	CPUPercent    float64   `bun:"cpu_percent,notnull"`
	MemoryUsedMB  int       `bun:"memory_used_mb,notnull"`
	MemoryLimitMB int       `bun:"memory_limit_mb,notnull"`
	DiskUsedMB    int       `bun:"disk_used_mb,notnull"`
	NetworkInMB   float64   `bun:"network_in_mb,notnull"`
	NetworkOutMB  float64   `bun:"network_out_mb,notnull"`
	Timestamp     time.Time `bun:"timestamp,notnull"`
}

// domainModel is the database model for network.Domain.
type domainModel struct {
	bun.BaseModel `bun:"table:domains,alias:dom"`

	ID          string     `bun:"id,pk"`
	TenantID    string     `bun:"tenant_id,notnull"`
	InstanceID  string     `bun:"instance_id,notnull"`
	Hostname    string     `bun:"hostname,unique,notnull"`
	Verified    bool       `bun:"verified,notnull"`
	TLSEnabled  bool       `bun:"tls_enabled,notnull"`
	CertExpiry  *time.Time `bun:"cert_expiry"`
	DNSTarget   string     `bun:"dns_target"`
	VerifyToken string     `bun:"verify_token"`
	CreatedAt   time.Time  `bun:"created_at,notnull"`
	UpdatedAt   time.Time  `bun:"updated_at,notnull"`
}

// routeModel is the database model for network.Route.
type routeModel struct {
	bun.BaseModel `bun:"table:routes,alias:rt"`

	ID          string    `bun:"id,pk"`
	TenantID    string    `bun:"tenant_id,notnull"`
	InstanceID  string    `bun:"instance_id,notnull"`
	Path        string    `bun:"path,notnull"`
	Port        int       `bun:"port,notnull"`
	Protocol    string    `bun:"protocol"`
	Weight      int       `bun:"weight"`
	StripPrefix bool      `bun:"strip_prefix"`
	CreatedAt   time.Time `bun:"created_at,notnull"`
	UpdatedAt   time.Time `bun:"updated_at,notnull"`
}

// certificateModel is the database model for network.Certificate.
type certificateModel struct {
	bun.BaseModel `bun:"table:certificates,alias:cert"`

	ID        string    `bun:"id,pk"`
	DomainID  string    `bun:"domain_id,notnull"`
	TenantID  string    `bun:"tenant_id,notnull"`
	Issuer    string    `bun:"issuer,notnull"`
	ExpiresAt time.Time `bun:"expires_at,notnull"`
	AutoRenew bool      `bun:"auto_renew,notnull"`
	CreatedAt time.Time `bun:"created_at,notnull"`
	UpdatedAt time.Time `bun:"updated_at,notnull"`
}

// secretModel is the database model for secrets.Secret.
type secretModel struct {
	bun.BaseModel `bun:"table:secrets,alias:s"`

	ID         int64     `bun:"id,pk,autoincrement"`
	TenantID   string    `bun:"tenant_id,notnull"`
	InstanceID string    `bun:"instance_id,notnull"`
	Key        string    `bun:"key,notnull"`
	Value      []byte    `bun:"value,notnull"`
	CreatedAt  time.Time `bun:"created_at,notnull"`
	UpdatedAt  time.Time `bun:"updated_at,notnull"`
}

// auditEntryModel is the database model for admin.AuditEntry.
type auditEntryModel struct {
	bun.BaseModel `bun:"table:audit_entries,alias:ae"`

	ID         int64     `bun:"id,pk,autoincrement"`
	TenantID   string    `bun:"tenant_id,notnull"`
	ActorID    string    `bun:"actor_id,notnull"`
	Action     string    `bun:"action,notnull"`
	Resource   string    `bun:"resource"`
	ResourceID string    `bun:"resource_id"`
	Details    []byte    `bun:"details,type:jsonb"`
	CreatedAt  time.Time `bun:"created_at,notnull"`
}

// Helper conversion functions.

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
