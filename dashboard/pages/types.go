package pages

import (
	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/network"
	"github.com/xraph/ctrlplane/secrets"
	"github.com/xraph/ctrlplane/telemetry"
	"github.com/xraph/ctrlplane/template"
	"github.com/xraph/ctrlplane/worker"
	"github.com/xraph/ctrlplane/workload"
)

// InstanceDetailData holds all data needed to render the instance detail page.
type InstanceDetailData struct {
	Instance           *instance.Instance
	Tab                string
	Deployments        *deploy.DeployListResult
	Releases           *deploy.ReleaseListResult
	Health             *health.InstanceHealth
	HealthChecks       []health.HealthCheck
	Domains            []network.Domain
	Routes             []network.Route
	Secrets            []secrets.Secret
	TelemetryDashboard *telemetry.DashboardData

	// ParentWorkload is set when this instance is a replica of a
	// workload (i.e., it carries the ctrlplane.workload label).
	// Nil for standalone instances. Surfaced as a "Part of …"
	// link in the instance detail header.
	ParentWorkload *workload.Workload
}

// InstanceHealthRow combines an instance with its health data for the health overview page.
type InstanceHealthRow struct {
	Instance *instance.Instance
	Health   *health.InstanceHealth
}

// WorkloadDetailData holds everything the workload detail page
// renders across its tabs. Populated by Contributor.renderWorkloadDetail.
type WorkloadDetailData struct {
	Workload    *workload.Workload
	Tab         string
	Replicas    []*instance.Instance
	Deployments *deploy.DeployListResult
	Releases    *deploy.ReleaseListResult
	Health      *workload.WorkloadHealth
	Domains     []network.Domain
	Routes      []network.Route
}

// WorkloadHealthRow combines a workload with its aggregate health
// for the health page's Workloads tab.
type WorkloadHealthRow struct {
	Workload *workload.Workload
	Health   *workload.WorkloadHealth
}

// DeployFormData holds data for the create deployment form page.
type DeployFormData struct {
	Instances   []*instance.Instance
	Instance    *instance.Instance
	Error       string
	Success     string
	RedirectURL string // When set, replaces the browser URL to prevent form resubmission on refresh.
}

// RollbackFormData holds data for the rollback form page.
type RollbackFormData struct {
	Instance *instance.Instance
	Releases []*deploy.Release
	Error    string
	Success  string
}

// ProviderListData holds data for the providers overview page.
type ProviderListData struct {
	Providers []admin.ProviderStatus
}

// ProviderDetailData holds data for a single provider detail page.
type ProviderDetailData struct {
	Provider     admin.ProviderStatus
	Instances    []*instance.Instance
	HealthTest   *admin.ProviderHealthResult
	PurgeSummary *PurgeSummary
	PurgeError   string
}

// PurgeSummary records the outcome of the provider Purge All action.
// Populated by Contributor.purgeProvider; nil when no purge ran.
// Listed failures are best-effort surfaces — operators retry by
// hitting the button again.
type PurgeSummary struct {
	Provider         string
	WorkloadsDeleted int
	InstancesDeleted int
	WorkloadFailures []string
	InstanceFailures []string
}

// WorkersPageData holds data for the workers overview page.
type WorkersPageData struct {
	Workers []worker.WorkerInfo
}

// WorkerDetailData holds data for a single worker detail page.
type WorkerDetailData struct {
	Worker worker.WorkerInfo
}

// EventsPageData holds data for the events stream page.
type EventsPageData struct {
	Events     []*event.Event
	FilterType string
}

// TemplateListPageData holds data for the workload templates list page.
type TemplateListPageData struct {
	Templates *template.ListResult
}

// TemplateDetailPageData holds data for a single template detail page.
type TemplateDetailPageData struct {
	Template    *template.Template
	Error       string
	Success     string
	RedirectURL string // When set, replaces the browser URL to prevent form resubmission on refresh.
}

// TemplateFormData holds data for the create/edit template form page.
type TemplateFormData struct {
	Template *template.Template // nil for create, populated for edit.
	Error    string
	Success  string
}
