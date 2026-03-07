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
	"github.com/xraph/ctrlplane/worker"
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
}

// InstanceHealthRow combines an instance with its health data for the health overview page.
type InstanceHealthRow struct {
	Instance *instance.Instance
	Health   *health.InstanceHealth
}

// DeployFormData holds data for the create deployment form page.
type DeployFormData struct {
	Instances        []*instance.Instance
	Instance         *instance.Instance
	Templates        []*deploy.Template
	SelectedTemplate *deploy.Template // Pre-fill form from this template when set.
	Error            string
	Success          string
	RedirectURL      string // When set, replaces the browser URL to prevent form resubmission on refresh.
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
	Provider   admin.ProviderStatus
	Instances  []*instance.Instance
	HealthTest *admin.ProviderHealthResult
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

// TemplateListPageData holds data for the deployment templates list page.
type TemplateListPageData struct {
	Templates *deploy.TemplateListResult
}

// TemplateDetailPageData holds data for a single template detail page.
type TemplateDetailPageData struct {
	Template    *deploy.Template
	Error       string
	Success     string
	RedirectURL string // When set, replaces the browser URL to prevent form resubmission on refresh.
}

// TemplateFormData holds data for the create/edit template form page.
type TemplateFormData struct {
	Template *deploy.Template // nil for create, populated for edit.
	Error    string
	Success  string
}
