package mongo

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

// ── Tenant ──────────────────────────────────────────────────────────────────

type tenantModel struct {
	grove.BaseModel `grove:"table:cp_tenants"`

	ID         string            `bson:"_id"                   grove:"id,pk"`
	ExternalID string            `bson:"external_id,omitempty" grove:"external_id"`
	Slug       string            `bson:"slug"                  grove:"slug"`
	Name       string            `bson:"name"                  grove:"name"`
	Status     string            `bson:"status"                grove:"status"`
	Metadata   map[string]string `bson:"metadata,omitempty"    grove:"metadata"`
	CreatedAt  time.Time         `bson:"created_at"            grove:"created_at"`
	UpdatedAt  time.Time         `bson:"updated_at"            grove:"updated_at"`
}

func toTenantModel(t *admin.Tenant) *tenantModel {
	return &tenantModel{
		ID:         idStr(t.ID),
		ExternalID: t.ExternalID,
		Slug:       t.Slug,
		Name:       t.Name,
		Status:     string(t.Status),
		CreatedAt:  t.CreatedAt,
		UpdatedAt:  t.UpdatedAt,
	}
}

func fromTenantModel(m *tenantModel) *admin.Tenant {
	return &admin.Tenant{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		ExternalID: m.ExternalID,
		Slug:       m.Slug,
		Name:       m.Name,
		Status:     admin.TenantStatus(m.Status),
	}
}

// ── Instance ────────────────────────────────────────────────────────────────

type instanceModel struct {
	grove.BaseModel `grove:"table:cp_instances"`

	ID           string    `bson:"_id"                    grove:"id,pk"`
	TenantID     string    `bson:"tenant_id"              grove:"tenant_id"`
	Slug         string    `bson:"slug"                   grove:"slug"`
	Name         string    `bson:"name"                   grove:"name"`
	State        string    `bson:"state"                  grove:"state"`
	ProviderName string    `bson:"provider_name"          grove:"provider_name"`
	ProviderRef  string    `bson:"provider_ref,omitempty" grove:"provider_ref"`
	Region       string    `bson:"region,omitempty"       grove:"region"`
	Image        string    `bson:"image,omitempty"        grove:"image"`
	CreatedAt    time.Time `bson:"created_at"             grove:"created_at"`
	UpdatedAt    time.Time `bson:"updated_at"             grove:"updated_at"`
}

func toInstanceModel(inst *instance.Instance) *instanceModel {
	return &instanceModel{
		ID:           idStr(inst.ID),
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

// ── Deployment ──────────────────────────────────────────────────────────────

type deploymentModel struct {
	grove.BaseModel `grove:"table:cp_deployments"`

	ID          string     `bson:"_id"                    grove:"id,pk"`
	TenantID    string     `bson:"tenant_id"              grove:"tenant_id"`
	InstanceID  string     `bson:"instance_id"            grove:"instance_id"`
	ReleaseID   string     `bson:"release_id"             grove:"release_id"`
	State       string     `bson:"state"                  grove:"state"`
	Strategy    string     `bson:"strategy"               grove:"strategy"`
	Image       string     `bson:"image,omitempty"        grove:"image"`
	ProviderRef string     `bson:"provider_ref,omitempty" grove:"provider_ref"`
	Error       string     `bson:"error,omitempty"        grove:"error"`
	Initiator   string     `bson:"initiator,omitempty"    grove:"initiator"`
	StartedAt   *time.Time `bson:"started_at,omitempty"   grove:"started_at"`
	FinishedAt  *time.Time `bson:"finished_at,omitempty"  grove:"finished_at"`
	CreatedAt   time.Time  `bson:"created_at"             grove:"created_at"`
	UpdatedAt   time.Time  `bson:"updated_at"             grove:"updated_at"`
}

func toDeploymentModel(d *deploy.Deployment) *deploymentModel {
	return &deploymentModel{
		ID:          idStr(d.ID),
		TenantID:    d.TenantID,
		InstanceID:  idStr(d.InstanceID),
		ReleaseID:   idStr(d.ReleaseID),
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

// ── Release ─────────────────────────────────────────────────────────────────

type releaseModel struct {
	grove.BaseModel `grove:"table:cp_releases"`

	ID         string    `bson:"_id"                  grove:"id,pk"`
	TenantID   string    `bson:"tenant_id"            grove:"tenant_id"`
	InstanceID string    `bson:"instance_id"          grove:"instance_id"`
	Version    int       `bson:"version"              grove:"version"`
	Image      string    `bson:"image"                grove:"image"`
	Notes      string    `bson:"notes,omitempty"      grove:"notes"`
	CommitSHA  string    `bson:"commit_sha,omitempty" grove:"commit_sha"`
	Active     bool      `bson:"active"               grove:"active"`
	CreatedAt  time.Time `bson:"created_at"           grove:"created_at"`
}

func toReleaseModel(r *deploy.Release) *releaseModel {
	return &releaseModel{
		ID:         idStr(r.ID),
		TenantID:   r.TenantID,
		InstanceID: idStr(r.InstanceID),
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

// ── Health Check ────────────────────────────────────────────────────────────

type healthCheckModel struct {
	grove.BaseModel `grove:"table:cp_health_checks"`

	ID         string    `bson:"_id"         grove:"id,pk"`
	TenantID   string    `bson:"tenant_id"   grove:"tenant_id"`
	InstanceID string    `bson:"instance_id" grove:"instance_id"`
	Name       string    `bson:"name"        grove:"name"`
	Type       string    `bson:"type"        grove:"type"`
	Enabled    bool      `bson:"enabled"     grove:"enabled"`
	Interval   int64     `bson:"interval"    grove:"interval"`
	Timeout    int64     `bson:"timeout"     grove:"timeout"`
	CreatedAt  time.Time `bson:"created_at"  grove:"created_at"`
	UpdatedAt  time.Time `bson:"updated_at"  grove:"updated_at"`
}

func toHealthCheckModel(c *health.HealthCheck) *healthCheckModel {
	return &healthCheckModel{
		ID:         idStr(c.ID),
		TenantID:   c.TenantID,
		InstanceID: idStr(c.InstanceID),
		Name:       c.Name,
		Type:       string(c.Type),
		Enabled:    c.Enabled,
		Interval:   int64(c.Interval),
		Timeout:    int64(c.Timeout),
		CreatedAt:  c.CreatedAt,
		UpdatedAt:  c.UpdatedAt,
	}
}

func fromHealthCheckModel(m *healthCheckModel) *health.HealthCheck {
	return &health.HealthCheck{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		TenantID:   m.TenantID,
		InstanceID: id.MustParse(m.InstanceID),
		Name:       m.Name,
		Type:       health.CheckType(m.Type),
		Enabled:    m.Enabled,
		Interval:   time.Duration(m.Interval),
		Timeout:    time.Duration(m.Timeout),
	}
}

// ── Health Result ───────────────────────────────────────────────────────────

type healthResultModel struct {
	grove.BaseModel `grove:"table:cp_health_results"`

	ID         string    `bson:"_id,omitempty"         grove:"id,pk"`
	CheckID    string    `bson:"check_id"              grove:"check_id"`
	InstanceID string    `bson:"instance_id"           grove:"instance_id"`
	TenantID   string    `bson:"tenant_id"             grove:"tenant_id"`
	Status     string    `bson:"status"                grove:"status"`
	Latency    int64     `bson:"latency"               grove:"latency"`
	Message    string    `bson:"message,omitempty"     grove:"message"`
	StatusCode int       `bson:"status_code,omitempty" grove:"status_code"`
	CheckedAt  time.Time `bson:"checked_at"            grove:"checked_at"`
}

func toHealthResultModel(r *health.HealthResult) *healthResultModel {
	return &healthResultModel{
		CheckID:    idStr(r.CheckID),
		InstanceID: idStr(r.InstanceID),
		TenantID:   r.TenantID,
		Status:     string(r.Status),
		Latency:    int64(r.Latency),
		Message:    r.Message,
		StatusCode: r.StatusCode,
		CheckedAt:  r.CheckedAt,
	}
}

func fromHealthResultModel(m *healthResultModel) health.HealthResult {
	return health.HealthResult{
		CheckID:    id.MustParse(m.CheckID),
		InstanceID: id.MustParse(m.InstanceID),
		TenantID:   m.TenantID,
		Status:     health.Status(m.Status),
		Latency:    time.Duration(m.Latency),
		Message:    m.Message,
		StatusCode: m.StatusCode,
		CheckedAt:  m.CheckedAt,
	}
}

// ── Telemetry ───────────────────────────────────────────────────────────────

type metricModel struct {
	grove.BaseModel `grove:"table:cp_metrics"`

	TenantID   string            `bson:"tenant_id"        grove:"tenant_id"`
	InstanceID string            `bson:"instance_id"      grove:"instance_id"`
	Name       string            `bson:"name"             grove:"name"`
	Type       string            `bson:"type,omitempty"   grove:"type"`
	Value      float64           `bson:"value"            grove:"value"`
	Labels     map[string]string `bson:"labels,omitempty" grove:"labels"`
	Timestamp  time.Time         `bson:"timestamp"        grove:"timestamp"`
}

func toMetricModel(m *telemetry.Metric) *metricModel {
	return &metricModel{
		TenantID:   m.TenantID,
		InstanceID: idStr(m.InstanceID),
		Name:       m.Name,
		Type:       string(m.Type),
		Value:      m.Value,
		Labels:     m.Labels,
		Timestamp:  m.Timestamp,
	}
}

func fromMetricModel(m *metricModel) telemetry.Metric {
	return telemetry.Metric{
		TenantID:   m.TenantID,
		InstanceID: id.MustParse(m.InstanceID),
		Name:       m.Name,
		Type:       telemetry.MetricType(m.Type),
		Value:      m.Value,
		Labels:     m.Labels,
		Timestamp:  m.Timestamp,
	}
}

type logEntryModel struct {
	grove.BaseModel `grove:"table:cp_logs"`

	TenantID   string         `bson:"tenant_id"        grove:"tenant_id"`
	InstanceID string         `bson:"instance_id"      grove:"instance_id"`
	Level      string         `bson:"level"            grove:"level"`
	Message    string         `bson:"message"          grove:"message"`
	Source     string         `bson:"source,omitempty" grove:"source"`
	Fields     map[string]any `bson:"fields,omitempty" grove:"fields"`
	Timestamp  time.Time      `bson:"timestamp"        grove:"timestamp"`
}

func toLogEntryModel(l *telemetry.LogEntry) *logEntryModel {
	return &logEntryModel{
		TenantID:   l.TenantID,
		InstanceID: idStr(l.InstanceID),
		Level:      l.Level,
		Message:    l.Message,
		Source:     l.Source,
		Fields:     l.Fields,
		Timestamp:  l.Timestamp,
	}
}

func fromLogEntryModel(m *logEntryModel) telemetry.LogEntry {
	return telemetry.LogEntry{
		TenantID:   m.TenantID,
		InstanceID: id.MustParse(m.InstanceID),
		Level:      m.Level,
		Message:    m.Message,
		Source:     m.Source,
		Fields:     m.Fields,
		Timestamp:  m.Timestamp,
	}
}

type traceModel struct {
	grove.BaseModel `grove:"table:cp_traces"`

	TenantID   string            `bson:"tenant_id"            grove:"tenant_id"`
	InstanceID string            `bson:"instance_id"          grove:"instance_id"`
	TraceID    string            `bson:"trace_id"             grove:"trace_id"`
	SpanID     string            `bson:"span_id"              grove:"span_id"`
	ParentID   string            `bson:"parent_id,omitempty"  grove:"parent_id"`
	Operation  string            `bson:"operation"            grove:"operation"`
	Duration   int64             `bson:"duration"             grove:"duration"`
	Status     string            `bson:"status,omitempty"     grove:"status"`
	Attributes map[string]string `bson:"attributes,omitempty" grove:"attributes"`
	Timestamp  time.Time         `bson:"timestamp"            grove:"timestamp"`
}

func toTraceModel(t *telemetry.Trace) *traceModel {
	return &traceModel{
		TenantID:   t.TenantID,
		InstanceID: idStr(t.InstanceID),
		TraceID:    t.TraceID,
		SpanID:     t.SpanID,
		ParentID:   t.ParentID,
		Operation:  t.Operation,
		Duration:   int64(t.Duration),
		Status:     t.Status,
		Attributes: t.Attributes,
		Timestamp:  t.Timestamp,
	}
}

func fromTraceModel(m *traceModel) telemetry.Trace {
	return telemetry.Trace{
		TenantID:   m.TenantID,
		InstanceID: id.MustParse(m.InstanceID),
		TraceID:    m.TraceID,
		SpanID:     m.SpanID,
		ParentID:   m.ParentID,
		Operation:  m.Operation,
		Duration:   time.Duration(m.Duration),
		Status:     m.Status,
		Attributes: m.Attributes,
		Timestamp:  m.Timestamp,
	}
}

type resourceSnapshotModel struct {
	grove.BaseModel `grove:"table:cp_resource_snapshots"`

	TenantID      string    `bson:"tenant_id"       grove:"tenant_id"`
	InstanceID    string    `bson:"instance_id"     grove:"instance_id"`
	CPUPercent    float64   `bson:"cpu_percent"     grove:"cpu_percent"`
	MemoryUsedMB  int       `bson:"memory_used_mb"  grove:"memory_used_mb"`
	MemoryLimitMB int       `bson:"memory_limit_mb" grove:"memory_limit_mb"`
	DiskUsedMB    int       `bson:"disk_used_mb"    grove:"disk_used_mb"`
	NetworkInMB   float64   `bson:"network_in_mb"   grove:"network_in_mb"`
	NetworkOutMB  float64   `bson:"network_out_mb"  grove:"network_out_mb"`
	Timestamp     time.Time `bson:"timestamp"       grove:"timestamp"`
}

func toResourceSnapshotModel(s *telemetry.ResourceSnapshot) *resourceSnapshotModel {
	return &resourceSnapshotModel{
		TenantID:      s.TenantID,
		InstanceID:    idStr(s.InstanceID),
		CPUPercent:    s.CPUPercent,
		MemoryUsedMB:  s.MemoryUsedMB,
		MemoryLimitMB: s.MemoryLimitMB,
		DiskUsedMB:    s.DiskUsedMB,
		NetworkInMB:   s.NetworkInMB,
		NetworkOutMB:  s.NetworkOutMB,
		Timestamp:     s.Timestamp,
	}
}

func fromResourceSnapshotModel(m *resourceSnapshotModel) telemetry.ResourceSnapshot {
	return telemetry.ResourceSnapshot{
		TenantID:      m.TenantID,
		InstanceID:    id.MustParse(m.InstanceID),
		CPUPercent:    m.CPUPercent,
		MemoryUsedMB:  m.MemoryUsedMB,
		MemoryLimitMB: m.MemoryLimitMB,
		DiskUsedMB:    m.DiskUsedMB,
		NetworkInMB:   m.NetworkInMB,
		NetworkOutMB:  m.NetworkOutMB,
		Timestamp:     m.Timestamp,
	}
}

// ── Network ─────────────────────────────────────────────────────────────────

type domainModel struct {
	grove.BaseModel `grove:"table:cp_domains"`

	ID          string     `bson:"_id"                    grove:"id,pk"`
	TenantID    string     `bson:"tenant_id"              grove:"tenant_id"`
	InstanceID  string     `bson:"instance_id"            grove:"instance_id"`
	Hostname    string     `bson:"hostname"               grove:"hostname"`
	Verified    bool       `bson:"verified"               grove:"verified"`
	TLSEnabled  bool       `bson:"tls_enabled"            grove:"tls_enabled"`
	CertExpiry  *time.Time `bson:"cert_expiry,omitempty"  grove:"cert_expiry"`
	DNSTarget   string     `bson:"dns_target,omitempty"   grove:"dns_target"`
	VerifyToken string     `bson:"verify_token,omitempty" grove:"verify_token"`
	CreatedAt   time.Time  `bson:"created_at"             grove:"created_at"`
	UpdatedAt   time.Time  `bson:"updated_at"             grove:"updated_at"`
}

func toDomainModel(d *network.Domain) *domainModel {
	return &domainModel{
		ID:          idStr(d.ID),
		TenantID:    d.TenantID,
		InstanceID:  idStr(d.InstanceID),
		Hostname:    d.Hostname,
		Verified:    d.Verified,
		TLSEnabled:  d.TLSEnabled,
		CertExpiry:  d.CertExpiry,
		DNSTarget:   d.DNSTarget,
		VerifyToken: d.VerifyToken,
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
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

type routeModel struct {
	grove.BaseModel `grove:"table:cp_routes"`

	ID          string    `bson:"_id"                grove:"id,pk"`
	TenantID    string    `bson:"tenant_id"          grove:"tenant_id"`
	InstanceID  string    `bson:"instance_id"        grove:"instance_id"`
	Path        string    `bson:"path"               grove:"path"`
	Port        int       `bson:"port"               grove:"port"`
	Protocol    string    `bson:"protocol,omitempty" grove:"protocol"`
	Weight      int       `bson:"weight"             grove:"weight"`
	StripPrefix bool      `bson:"strip_prefix"       grove:"strip_prefix"`
	CreatedAt   time.Time `bson:"created_at"         grove:"created_at"`
	UpdatedAt   time.Time `bson:"updated_at"         grove:"updated_at"`
}

func toRouteModel(r *network.Route) *routeModel {
	return &routeModel{
		ID:          idStr(r.ID),
		TenantID:    r.TenantID,
		InstanceID:  idStr(r.InstanceID),
		Path:        r.Path,
		Port:        r.Port,
		Protocol:    r.Protocol,
		Weight:      r.Weight,
		StripPrefix: r.StripPrefix,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
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

type certificateModel struct {
	grove.BaseModel `grove:"table:cp_certificates"`

	ID        string    `bson:"_id"        grove:"id,pk"`
	DomainID  string    `bson:"domain_id"  grove:"domain_id"`
	TenantID  string    `bson:"tenant_id"  grove:"tenant_id"`
	Issuer    string    `bson:"issuer"     grove:"issuer"`
	ExpiresAt time.Time `bson:"expires_at" grove:"expires_at"`
	AutoRenew bool      `bson:"auto_renew" grove:"auto_renew"`
	CreatedAt time.Time `bson:"created_at" grove:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" grove:"updated_at"`
}

func toCertificateModel(c *network.Certificate) *certificateModel {
	return &certificateModel{
		ID:        idStr(c.ID),
		DomainID:  idStr(c.DomainID),
		TenantID:  c.TenantID,
		Issuer:    c.Issuer,
		ExpiresAt: c.ExpiresAt,
		AutoRenew: c.AutoRenew,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
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

// ── Secrets ─────────────────────────────────────────────────────────────────

type secretModel struct {
	grove.BaseModel `grove:"table:cp_secrets"`

	ID         string    `bson:"_id,omitempty" grove:"id,pk"`
	TenantID   string    `bson:"tenant_id"     grove:"tenant_id"`
	InstanceID string    `bson:"instance_id"   grove:"instance_id"`
	Key        string    `bson:"key"           grove:"key"`
	Value      []byte    `bson:"value"         grove:"value"`
	CreatedAt  time.Time `bson:"created_at"    grove:"created_at"`
	UpdatedAt  time.Time `bson:"updated_at"    grove:"updated_at"`
}

func toSecretModel(s *secrets.Secret) *secretModel {
	return &secretModel{
		TenantID:   s.TenantID,
		InstanceID: idStr(s.InstanceID),
		Key:        s.Key,
		Value:      s.Value,
		CreatedAt:  s.CreatedAt,
		UpdatedAt:  s.UpdatedAt,
	}
}

func fromSecretModel(m *secretModel) *secrets.Secret {
	return &secrets.Secret{
		Entity: ctrlplane.Entity{
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		TenantID:   m.TenantID,
		InstanceID: id.MustParse(m.InstanceID),
		Key:        m.Key,
		Value:      m.Value,
	}
}

// ── Audit ───────────────────────────────────────────────────────────────────

type auditEntryModel struct {
	grove.BaseModel `grove:"table:cp_audit_entries"`

	ID         string         `bson:"_id,omitempty"         grove:"id,pk"`
	TenantID   string         `bson:"tenant_id"             grove:"tenant_id"`
	ActorID    string         `bson:"actor_id"              grove:"actor_id"`
	Action     string         `bson:"action"                grove:"action"`
	Resource   string         `bson:"resource,omitempty"    grove:"resource"`
	ResourceID string         `bson:"resource_id,omitempty" grove:"resource_id"`
	Details    map[string]any `bson:"details,omitempty"     grove:"details"`
	CreatedAt  time.Time      `bson:"created_at"            grove:"created_at"`
}

func toAuditEntryModel(e *admin.AuditEntry) *auditEntryModel {
	return &auditEntryModel{
		TenantID:   e.TenantID,
		ActorID:    e.ActorID,
		Action:     e.Action,
		Resource:   e.Resource,
		ResourceID: e.ResourceID,
		CreatedAt:  e.CreatedAt,
	}
}

func fromAuditEntryModel(m *auditEntryModel) admin.AuditEntry {
	return admin.AuditEntry{
		Entity: ctrlplane.Entity{
			CreatedAt: m.CreatedAt,
		},
		TenantID:   m.TenantID,
		ActorID:    m.ActorID,
		Action:     m.Action,
		Resource:   m.Resource,
		ResourceID: m.ResourceID,
	}
}
