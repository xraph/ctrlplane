package mongo

import (
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/network"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/secrets"
	"github.com/xraph/ctrlplane/telemetry"
)

// ── Instance ────────────────────────────────────────────────────────────────

type instanceModel struct {
	ID             string            `bson:"_id"`
	TenantID       string            `bson:"tenant_id"`
	Name           string            `bson:"name"`
	Slug           string            `bson:"slug"`
	ProviderName   string            `bson:"provider_name"`
	ProviderRef    string            `bson:"provider_ref"`
	Region         string            `bson:"region"`
	State          string            `bson:"state"`
	Image          string            `bson:"image"`
	Resources      resourceSpecModel `bson:"resources"`
	Env            map[string]string `bson:"env,omitempty"`
	Ports          []portSpecModel   `bson:"ports,omitempty"`
	Endpoints      []endpointModel   `bson:"endpoints,omitempty"`
	Labels         map[string]string `bson:"labels,omitempty"`
	CurrentRelease string            `bson:"current_release,omitempty"`
	SuspendedAt    *time.Time        `bson:"suspended_at,omitempty"`
	CreatedAt      time.Time         `bson:"created_at"`
	UpdatedAt      time.Time         `bson:"updated_at"`
}

type resourceSpecModel struct {
	CPUMillis int    `bson:"cpu_millis"`
	MemoryMB  int    `bson:"memory_mb"`
	DiskMB    int    `bson:"disk_mb"`
	GPUType   string `bson:"gpu_type,omitempty"`
	GPUCount  int    `bson:"gpu_count,omitempty"`
}

type portSpecModel struct {
	Container int    `bson:"container"`
	Host      int    `bson:"host"`
	Protocol  string `bson:"protocol"`
}

type endpointModel struct {
	URL      string `bson:"url"`
	Port     int    `bson:"port"`
	Protocol string `bson:"protocol"`
	Public   bool   `bson:"public"`
}

func toInstanceModel(inst *instance.Instance) *instanceModel {
	m := &instanceModel{
		ID:           idStr(inst.ID),
		TenantID:     inst.TenantID,
		Name:         inst.Name,
		Slug:         inst.Slug,
		ProviderName: inst.ProviderName,
		ProviderRef:  inst.ProviderRef,
		Region:       inst.Region,
		State:        string(inst.State),
		Image:        inst.Image,
		Resources: resourceSpecModel{
			CPUMillis: inst.Resources.CPUMillis,
			MemoryMB:  inst.Resources.MemoryMB,
			DiskMB:    inst.Resources.DiskMB,
		},
		Env:            inst.Env,
		Labels:         inst.Labels,
		CurrentRelease: idStr(inst.CurrentRelease),
		SuspendedAt:    inst.SuspendedAt,
		CreatedAt:      inst.CreatedAt,
		UpdatedAt:      inst.UpdatedAt,
	}

	for _, p := range inst.Ports {
		m.Ports = append(m.Ports, portSpecModel{
			Container: p.Container,
			Host:      p.Host,
			Protocol:  p.Protocol,
		})
	}

	for _, e := range inst.Endpoints {
		m.Endpoints = append(m.Endpoints, endpointModel{
			URL:      e.URL,
			Port:     e.Port,
			Protocol: e.Protocol,
			Public:   e.Public,
		})
	}

	return m
}

func fromInstanceModel(m *instanceModel) *instance.Instance {
	inst := &instance.Instance{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		TenantID:     m.TenantID,
		Name:         m.Name,
		Slug:         m.Slug,
		ProviderName: m.ProviderName,
		ProviderRef:  m.ProviderRef,
		Region:       m.Region,
		State:        provider.InstanceState(m.State),
		Image:        m.Image,
		Resources: provider.ResourceSpec{
			CPUMillis: m.Resources.CPUMillis,
			MemoryMB:  m.Resources.MemoryMB,
			DiskMB:    m.Resources.DiskMB,
		},
		Env:         m.Env,
		Labels:      m.Labels,
		SuspendedAt: m.SuspendedAt,
	}

	if m.CurrentRelease != "" {
		inst.CurrentRelease = id.MustParse(m.CurrentRelease)
	}

	for _, p := range m.Ports {
		inst.Ports = append(inst.Ports, provider.PortSpec{
			Container: p.Container,
			Host:      p.Host,
			Protocol:  p.Protocol,
		})
	}

	for _, e := range m.Endpoints {
		inst.Endpoints = append(inst.Endpoints, provider.Endpoint{
			URL:      e.URL,
			Port:     e.Port,
			Protocol: e.Protocol,
			Public:   e.Public,
		})
	}

	return inst
}

// ── Deployment ──────────────────────────────────────────────────────────────

type deploymentModel struct {
	ID          string            `bson:"_id"`
	TenantID    string            `bson:"tenant_id"`
	InstanceID  string            `bson:"instance_id"`
	ReleaseID   string            `bson:"release_id"`
	State       string            `bson:"state"`
	Strategy    string            `bson:"strategy"`
	Image       string            `bson:"image"`
	Env         map[string]string `bson:"env,omitempty"`
	ProviderRef string            `bson:"provider_ref,omitempty"`
	StartedAt   *time.Time        `bson:"started_at,omitempty"`
	FinishedAt  *time.Time        `bson:"finished_at,omitempty"`
	Error       string            `bson:"error,omitempty"`
	Initiator   string            `bson:"initiator"`
	CreatedAt   time.Time         `bson:"created_at"`
	UpdatedAt   time.Time         `bson:"updated_at"`
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
		Env:         d.Env,
		ProviderRef: d.ProviderRef,
		StartedAt:   d.StartedAt,
		FinishedAt:  d.FinishedAt,
		Error:       d.Error,
		Initiator:   d.Initiator,
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	}
}

func fromDeploymentModel(m *deploymentModel) *deploy.Deployment {
	d := &deploy.Deployment{
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
		Env:         m.Env,
		ProviderRef: m.ProviderRef,
		StartedAt:   m.StartedAt,
		FinishedAt:  m.FinishedAt,
		Error:       m.Error,
		Initiator:   m.Initiator,
	}

	return d
}

// ── Release ─────────────────────────────────────────────────────────────────

type releaseModel struct {
	ID         string            `bson:"_id"`
	TenantID   string            `bson:"tenant_id"`
	InstanceID string            `bson:"instance_id"`
	Version    int               `bson:"version"`
	Image      string            `bson:"image"`
	Env        map[string]string `bson:"env,omitempty"`
	Notes      string            `bson:"notes,omitempty"`
	CommitSHA  string            `bson:"commit_sha,omitempty"`
	Active     bool              `bson:"active"`
	CreatedAt  time.Time         `bson:"created_at"`
	UpdatedAt  time.Time         `bson:"updated_at"`
}

func toReleaseModel(r *deploy.Release) *releaseModel {
	return &releaseModel{
		ID:         idStr(r.ID),
		TenantID:   r.TenantID,
		InstanceID: idStr(r.InstanceID),
		Version:    r.Version,
		Image:      r.Image,
		Env:        r.Env,
		Notes:      r.Notes,
		CommitSHA:  r.CommitSHA,
		Active:     r.Active,
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
	}
}

func fromReleaseModel(m *releaseModel) *deploy.Release {
	return &deploy.Release{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		TenantID:   m.TenantID,
		InstanceID: id.MustParse(m.InstanceID),
		Version:    m.Version,
		Image:      m.Image,
		Env:        m.Env,
		Notes:      m.Notes,
		CommitSHA:  m.CommitSHA,
		Active:     m.Active,
	}
}

// ── Health Check ────────────────────────────────────────────────────────────

type healthCheckModel struct {
	ID         string    `bson:"_id"`
	TenantID   string    `bson:"tenant_id"`
	InstanceID string    `bson:"instance_id"`
	Name       string    `bson:"name"`
	Type       string    `bson:"type"`
	Target     string    `bson:"target"`
	Interval   int64     `bson:"interval"`
	Timeout    int64     `bson:"timeout"`
	Retries    int       `bson:"retries"`
	Enabled    bool      `bson:"enabled"`
	CreatedAt  time.Time `bson:"created_at"`
	UpdatedAt  time.Time `bson:"updated_at"`
}

func toHealthCheckModel(c *health.HealthCheck) *healthCheckModel {
	return &healthCheckModel{
		ID:         idStr(c.ID),
		TenantID:   c.TenantID,
		InstanceID: idStr(c.InstanceID),
		Name:       c.Name,
		Type:       string(c.Type),
		Target:     c.Target,
		Interval:   int64(c.Interval),
		Timeout:    int64(c.Timeout),
		Retries:    c.Retries,
		Enabled:    c.Enabled,
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
		Target:     m.Target,
		Interval:   time.Duration(m.Interval),
		Timeout:    time.Duration(m.Timeout),
		Retries:    m.Retries,
		Enabled:    m.Enabled,
	}
}

// ── Health Result ───────────────────────────────────────────────────────────

type healthResultModel struct {
	ID         string    `bson:"_id"`
	CheckID    string    `bson:"check_id"`
	InstanceID string    `bson:"instance_id"`
	TenantID   string    `bson:"tenant_id"`
	Status     string    `bson:"status"`
	Latency    int64     `bson:"latency"`
	Message    string    `bson:"message,omitempty"`
	StatusCode int       `bson:"status_code,omitempty"`
	CheckedAt  time.Time `bson:"checked_at"`
	CreatedAt  time.Time `bson:"created_at"`
	UpdatedAt  time.Time `bson:"updated_at"`
}

func toHealthResultModel(r *health.HealthResult) *healthResultModel {
	return &healthResultModel{
		ID:         idStr(r.ID),
		CheckID:    idStr(r.CheckID),
		InstanceID: idStr(r.InstanceID),
		TenantID:   r.TenantID,
		Status:     string(r.Status),
		Latency:    int64(r.Latency),
		Message:    r.Message,
		StatusCode: r.StatusCode,
		CheckedAt:  r.CheckedAt,
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
	}
}

func fromHealthResultModel(m *healthResultModel) *health.HealthResult {
	return &health.HealthResult{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
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
	InstanceID string            `bson:"instance_id"`
	TenantID   string            `bson:"tenant_id"`
	Name       string            `bson:"name"`
	Type       string            `bson:"type"`
	Value      float64           `bson:"value"`
	Labels     map[string]string `bson:"labels,omitempty"`
	Timestamp  time.Time         `bson:"timestamp"`
}

func toMetricModel(m *telemetry.Metric) *metricModel {
	return &metricModel{
		InstanceID: idStr(m.InstanceID),
		TenantID:   m.TenantID,
		Name:       m.Name,
		Type:       string(m.Type),
		Value:      m.Value,
		Labels:     m.Labels,
		Timestamp:  m.Timestamp,
	}
}

func fromMetricModel(m *metricModel) telemetry.Metric {
	return telemetry.Metric{
		InstanceID: id.MustParse(m.InstanceID),
		TenantID:   m.TenantID,
		Name:       m.Name,
		Type:       telemetry.MetricType(m.Type),
		Value:      m.Value,
		Labels:     m.Labels,
		Timestamp:  m.Timestamp,
	}
}

type logEntryModel struct {
	InstanceID string         `bson:"instance_id"`
	TenantID   string         `bson:"tenant_id"`
	Level      string         `bson:"level"`
	Message    string         `bson:"message"`
	Fields     map[string]any `bson:"fields,omitempty"`
	Source     string         `bson:"source"`
	Timestamp  time.Time      `bson:"timestamp"`
}

func toLogEntryModel(l *telemetry.LogEntry) *logEntryModel {
	return &logEntryModel{
		InstanceID: idStr(l.InstanceID),
		TenantID:   l.TenantID,
		Level:      l.Level,
		Message:    l.Message,
		Fields:     l.Fields,
		Source:     l.Source,
		Timestamp:  l.Timestamp,
	}
}

func fromLogEntryModel(m *logEntryModel) telemetry.LogEntry {
	return telemetry.LogEntry{
		InstanceID: id.MustParse(m.InstanceID),
		TenantID:   m.TenantID,
		Level:      m.Level,
		Message:    m.Message,
		Fields:     m.Fields,
		Source:     m.Source,
		Timestamp:  m.Timestamp,
	}
}

type traceModel struct {
	InstanceID string            `bson:"instance_id"`
	TenantID   string            `bson:"tenant_id"`
	TraceID    string            `bson:"trace_id"`
	SpanID     string            `bson:"span_id"`
	ParentID   string            `bson:"parent_id,omitempty"`
	Operation  string            `bson:"operation"`
	Duration   int64             `bson:"duration"`
	Status     string            `bson:"status"`
	Attributes map[string]string `bson:"attributes,omitempty"`
	Timestamp  time.Time         `bson:"timestamp"`
}

func toTraceModel(t *telemetry.Trace) *traceModel {
	return &traceModel{
		InstanceID: idStr(t.InstanceID),
		TenantID:   t.TenantID,
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
		InstanceID: id.MustParse(m.InstanceID),
		TenantID:   m.TenantID,
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
	InstanceID    string    `bson:"instance_id"`
	TenantID      string    `bson:"tenant_id"`
	CPUPercent    float64   `bson:"cpu_percent"`
	MemoryUsedMB  int       `bson:"memory_used_mb"`
	MemoryLimitMB int       `bson:"memory_limit_mb"`
	DiskUsedMB    int       `bson:"disk_used_mb"`
	NetworkInMB   float64   `bson:"network_in_mb"`
	NetworkOutMB  float64   `bson:"network_out_mb"`
	Timestamp     time.Time `bson:"timestamp"`
}

func toResourceSnapshotModel(s *telemetry.ResourceSnapshot) *resourceSnapshotModel {
	return &resourceSnapshotModel{
		InstanceID:    idStr(s.InstanceID),
		TenantID:      s.TenantID,
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
		InstanceID:    id.MustParse(m.InstanceID),
		TenantID:      m.TenantID,
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
	ID          string     `bson:"_id"`
	TenantID    string     `bson:"tenant_id"`
	InstanceID  string     `bson:"instance_id"`
	Hostname    string     `bson:"hostname"`
	Verified    bool       `bson:"verified"`
	TLSEnabled  bool       `bson:"tls_enabled"`
	CertExpiry  *time.Time `bson:"cert_expiry,omitempty"`
	DNSTarget   string     `bson:"dns_target"`
	VerifyToken string     `bson:"verify_token"`
	CreatedAt   time.Time  `bson:"created_at"`
	UpdatedAt   time.Time  `bson:"updated_at"`
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
	ID          string    `bson:"_id"`
	TenantID    string    `bson:"tenant_id"`
	InstanceID  string    `bson:"instance_id"`
	Path        string    `bson:"path"`
	Port        int       `bson:"port"`
	Protocol    string    `bson:"protocol"`
	Weight      int       `bson:"weight"`
	StripPrefix bool      `bson:"strip_prefix"`
	CreatedAt   time.Time `bson:"created_at"`
	UpdatedAt   time.Time `bson:"updated_at"`
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
	ID        string    `bson:"_id"`
	DomainID  string    `bson:"domain_id"`
	TenantID  string    `bson:"tenant_id"`
	Issuer    string    `bson:"issuer"`
	ExpiresAt time.Time `bson:"expires_at"`
	AutoRenew bool      `bson:"auto_renew"`
	CreatedAt time.Time `bson:"created_at"`
	UpdatedAt time.Time `bson:"updated_at"`
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
	ID         string    `bson:"_id"`
	TenantID   string    `bson:"tenant_id"`
	InstanceID string    `bson:"instance_id"`
	Key        string    `bson:"key"`
	Type       string    `bson:"type"`
	Version    int       `bson:"version"`
	Value      []byte    `bson:"value"`
	CreatedAt  time.Time `bson:"created_at"`
	UpdatedAt  time.Time `bson:"updated_at"`
}

func toSecretModel(s *secrets.Secret) *secretModel {
	return &secretModel{
		ID:         idStr(s.ID),
		TenantID:   s.TenantID,
		InstanceID: idStr(s.InstanceID),
		Key:        s.Key,
		Type:       string(s.Type),
		Version:    s.Version,
		Value:      s.Value,
		CreatedAt:  s.CreatedAt,
		UpdatedAt:  s.UpdatedAt,
	}
}

func fromSecretModel(m *secretModel) *secrets.Secret {
	return &secrets.Secret{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		TenantID:   m.TenantID,
		InstanceID: id.MustParse(m.InstanceID),
		Key:        m.Key,
		Type:       secrets.SecretType(m.Type),
		Version:    m.Version,
		Value:      m.Value,
	}
}

// ── Admin ───────────────────────────────────────────────────────────────────

type tenantModel struct {
	ID          string            `bson:"_id"`
	ExternalID  string            `bson:"external_id,omitempty"`
	Name        string            `bson:"name"`
	Slug        string            `bson:"slug"`
	Status      string            `bson:"status"`
	Plan        string            `bson:"plan"`
	Quota       quotaModel        `bson:"quota"`
	SuspendedAt *time.Time        `bson:"suspended_at,omitempty"`
	Metadata    map[string]string `bson:"metadata,omitempty"`
	CreatedAt   time.Time         `bson:"created_at"`
	UpdatedAt   time.Time         `bson:"updated_at"`
}

type quotaModel struct {
	MaxInstances int `bson:"max_instances"`
	MaxCPUMillis int `bson:"max_cpu_millis"`
	MaxMemoryMB  int `bson:"max_memory_mb"`
	MaxDiskMB    int `bson:"max_disk_mb"`
	MaxDomains   int `bson:"max_domains"`
	MaxSecrets   int `bson:"max_secrets"`
}

func toTenantModel(t *admin.Tenant) *tenantModel {
	return &tenantModel{
		ID:         idStr(t.ID),
		ExternalID: t.ExternalID,
		Name:       t.Name,
		Slug:       t.Slug,
		Status:     string(t.Status),
		Plan:       t.Plan,
		Quota: quotaModel{
			MaxInstances: t.Quota.MaxInstances,
			MaxCPUMillis: t.Quota.MaxCPUMillis,
			MaxMemoryMB:  t.Quota.MaxMemoryMB,
			MaxDiskMB:    t.Quota.MaxDiskMB,
			MaxDomains:   t.Quota.MaxDomains,
			MaxSecrets:   t.Quota.MaxSecrets,
		},
		SuspendedAt: t.SuspendedAt,
		Metadata:    t.Metadata,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
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
		Name:       m.Name,
		Slug:       m.Slug,
		Status:     admin.TenantStatus(m.Status),
		Plan:       m.Plan,
		Quota: admin.Quota{
			MaxInstances: m.Quota.MaxInstances,
			MaxCPUMillis: m.Quota.MaxCPUMillis,
			MaxMemoryMB:  m.Quota.MaxMemoryMB,
			MaxDiskMB:    m.Quota.MaxDiskMB,
			MaxDomains:   m.Quota.MaxDomains,
			MaxSecrets:   m.Quota.MaxSecrets,
		},
		SuspendedAt: m.SuspendedAt,
		Metadata:    m.Metadata,
	}
}

type auditEntryModel struct {
	ID         string         `bson:"_id"`
	TenantID   string         `bson:"tenant_id"`
	ActorID    string         `bson:"actor_id"`
	ActorType  string         `bson:"actor_type"`
	Resource   string         `bson:"resource"`
	ResourceID string         `bson:"resource_id"`
	Action     string         `bson:"action"`
	Details    map[string]any `bson:"details,omitempty"`
	IPAddress  string         `bson:"ip_address,omitempty"`
	CreatedAt  time.Time      `bson:"created_at"`
	UpdatedAt  time.Time      `bson:"updated_at"`
}

func toAuditEntryModel(e *admin.AuditEntry) *auditEntryModel {
	return &auditEntryModel{
		ID:         idStr(e.ID),
		TenantID:   e.TenantID,
		ActorID:    e.ActorID,
		ActorType:  e.ActorType,
		Resource:   e.Resource,
		ResourceID: e.ResourceID,
		Action:     e.Action,
		Details:    e.Details,
		IPAddress:  e.IPAddress,
		CreatedAt:  e.CreatedAt,
		UpdatedAt:  e.UpdatedAt,
	}
}

func fromAuditEntryModel(m *auditEntryModel) admin.AuditEntry {
	return admin.AuditEntry{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		TenantID:   m.TenantID,
		ActorID:    m.ActorID,
		ActorType:  m.ActorType,
		Resource:   m.Resource,
		ResourceID: m.ResourceID,
		Action:     m.Action,
		Details:    m.Details,
		IPAddress:  m.IPAddress,
	}
}
