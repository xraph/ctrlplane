package audithook

// Severity constants.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Outcome constants.
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
)

// Instance action constants.
const (
	ActionInstanceCreated     = "ctrlplane.instance.created"
	ActionInstanceStarted     = "ctrlplane.instance.started"
	ActionInstanceStopped     = "ctrlplane.instance.stopped"
	ActionInstanceFailed      = "ctrlplane.instance.failed"
	ActionInstanceDeleted     = "ctrlplane.instance.deleted"
	ActionInstanceScaled      = "ctrlplane.instance.scaled"
	ActionInstanceSuspended   = "ctrlplane.instance.suspended"
	ActionInstanceUnsuspended = "ctrlplane.instance.unsuspended"
)

// Deploy action constants.
const (
	ActionDeployStarted    = "ctrlplane.deploy.started"
	ActionDeploySucceeded  = "ctrlplane.deploy.succeeded"
	ActionDeployFailed     = "ctrlplane.deploy.failed"
	ActionDeployRolledBack = "ctrlplane.deploy.rolled_back"
)

// Health action constants.
const (
	ActionHealthCheckPassed = "ctrlplane.health.passed"
	ActionHealthCheckFailed = "ctrlplane.health.failed"
	ActionHealthDegraded    = "ctrlplane.health.degraded"
	ActionHealthRecovered   = "ctrlplane.health.recovered"
)

// Network action constants.
const (
	ActionDomainAdded     = "ctrlplane.domain.added"
	ActionDomainVerified  = "ctrlplane.domain.verified"
	ActionDomainRemoved   = "ctrlplane.domain.removed"
	ActionCertProvisioned = "ctrlplane.cert.provisioned"
	ActionCertExpiring    = "ctrlplane.cert.expiring"
)

// Admin action constants.
const (
	ActionTenantCreated   = "ctrlplane.tenant.created"
	ActionTenantSuspended = "ctrlplane.tenant.suspended"
	ActionTenantDeleted   = "ctrlplane.tenant.deleted"
	ActionQuotaExceeded   = "ctrlplane.quota.exceeded"
)

// Resource constants.
const (
	ResourceInstance    = "instance"
	ResourceDeployment  = "deployment"
	ResourceHealthCheck = "health_check"
	ResourceDomain      = "domain"
	ResourceCertificate = "certificate"
	ResourceTenant      = "tenant"
	ResourceQuota       = "quota"
)

// Category constants.
const (
	CategoryInstance = "instance"
	CategoryDeploy   = "deploy"
	CategoryHealth   = "health"
	CategoryNetwork  = "network"
	CategoryAdmin    = "admin"
)
