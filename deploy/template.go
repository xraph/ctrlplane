package deploy

import (
	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/secrets"
)

// SecretRef is a lightweight reference to a secret needed at deploy time.
type SecretRef struct {
	Key  string             `db:"key"  json:"key"`
	Type secrets.SecretType `db:"type" json:"type"`
}

// ConfigFile defines a configuration file (JSON/YAML/env/text) that is stored
// in the vault and mounted into the container at deploy time.
type ConfigFile struct {
	Name    string `db:"name"    json:"name"`    // e.g. "app-config"
	Path    string `db:"path"    json:"path"`    // mount path, e.g. "/etc/app/config.yaml"
	Format  string `db:"format"  json:"format"`  // "json", "yaml", "env", "text"
	Content string `db:"content" json:"content"` // file content (stored in vault at deploy time)
}

// Template is a reusable deployment configuration that can be saved
// and applied when creating new deployments.
type Template struct {
	ctrlplane.Entity

	TenantID    string                    `db:"tenant_id"    json:"tenant_id"`
	Name        string                    `db:"name"         json:"name"`
	Description string                    `db:"description"  json:"description,omitempty"`
	Image       string                    `db:"image"        json:"image"`
	Strategy    string                    `db:"strategy"     json:"strategy"`
	Resources   provider.ResourceSpec     `db:"resources"    json:"resources"`
	Ports       []provider.PortSpec       `db:"ports"        json:"ports,omitempty"`
	Volumes     []provider.VolumeSpec     `db:"volumes"      json:"volumes,omitempty"`
	HealthCheck *provider.HealthCheckSpec `db:"health_check" json:"health_check,omitempty"`
	Env         map[string]string         `db:"env"          json:"env,omitempty"`
	Secrets     []SecretRef               `db:"secrets"      json:"secrets,omitempty"`
	ConfigFiles []ConfigFile              `db:"config_files" json:"config_files,omitempty"`
	Labels      map[string]string         `db:"labels"       json:"labels,omitempty"`
	Annotations map[string]string         `db:"annotations"  json:"annotations,omitempty"`
	CommitSHA   string                    `db:"commit_sha"   json:"commit_sha,omitempty"`
	Notes       string                    `db:"notes"        json:"notes,omitempty"`
}

// CreateTemplateRequest holds the parameters for creating a deployment template.
type CreateTemplateRequest struct {
	Name        string                    `json:"name"                   validate:"required"`
	Description string                    `json:"description,omitempty"`
	Image       string                    `json:"image"                  validate:"required"`
	Strategy    string                    `json:"strategy,omitempty"`
	Resources   provider.ResourceSpec     `json:"resources"`
	Ports       []provider.PortSpec       `json:"ports,omitempty"`
	Volumes     []provider.VolumeSpec     `json:"volumes,omitempty"`
	HealthCheck *provider.HealthCheckSpec `json:"health_check,omitempty"`
	Env         map[string]string         `json:"env,omitempty"`
	Secrets     []SecretRef               `json:"secrets,omitempty"`
	ConfigFiles []ConfigFile              `json:"config_files,omitempty"`
	Labels      map[string]string         `json:"labels,omitempty"`
	Annotations map[string]string         `json:"annotations,omitempty"`
	CommitSHA   string                    `json:"commit_sha,omitempty"`
	Notes       string                    `json:"notes,omitempty"`
}

// UpdateTemplateRequest holds the parameters for updating a deployment template.
// Pointer fields enable partial updates — only non-nil fields are applied.
type UpdateTemplateRequest struct {
	Name        *string                   `json:"name,omitempty"`
	Description *string                   `json:"description,omitempty"`
	Image       *string                   `json:"image,omitempty"`
	Strategy    *string                   `json:"strategy,omitempty"`
	Resources   *provider.ResourceSpec    `json:"resources,omitempty"`
	Ports       []provider.PortSpec       `json:"ports,omitempty"`
	Volumes     []provider.VolumeSpec     `json:"volumes,omitempty"`
	HealthCheck *provider.HealthCheckSpec `json:"health_check,omitempty"`
	Env         map[string]string         `json:"env,omitempty"`
	Secrets     []SecretRef               `json:"secrets,omitempty"`
	ConfigFiles []ConfigFile              `json:"config_files,omitempty"`
	Labels      map[string]string         `json:"labels,omitempty"`
	Annotations map[string]string         `json:"annotations,omitempty"`
	CommitSHA   *string                   `json:"commit_sha,omitempty"`
	Notes       *string                   `json:"notes,omitempty"`
}

// TemplateListResult holds a page of templates with a total count.
type TemplateListResult struct {
	Items []*Template `json:"items"`
	Total int         `json:"total"`
}
