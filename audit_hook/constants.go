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

// Workload action constants.
const (
	ActionWorkloadCreated   = "ctrlplane.workload.created"
	ActionWorkloadUpdated   = "ctrlplane.workload.updated"
	ActionWorkloadScaled    = "ctrlplane.workload.scaled"
	ActionWorkloadDeployed  = "ctrlplane.workload.deployed"
	ActionWorkloadPaused    = "ctrlplane.workload.paused"
	ActionWorkloadResumed   = "ctrlplane.workload.resumed"
	ActionWorkloadRestarted = "ctrlplane.workload.restarted"
	ActionWorkloadDeleted   = "ctrlplane.workload.deleted"
	ActionWorkloadFailed    = "ctrlplane.workload.failed"
)

// Template action constants.
const (
	ActionTemplateCreated = "ctrlplane.template.created"
	ActionTemplateUpdated = "ctrlplane.template.updated"
	ActionTemplateDeleted = "ctrlplane.template.deleted"
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
	ResourceWorkload    = "workload"
	ResourceTemplate    = "template"
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
	CategoryWorkload = "workload"
	CategoryTemplate = "template"
	CategoryDeploy   = "deploy"
	CategoryHealth   = "health"
	CategoryNetwork  = "network"
	CategoryAdmin    = "admin"
)
