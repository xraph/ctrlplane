package provider

import "github.com/xraph/ctrlplane/secrets"

// SecretRef is a lightweight reference to a secret needed at deploy time.
// The secret value itself is fetched from the vault by Key when a Workload
// is deployed; specs only carry the reference, never the value.
//
// Lives in the provider package so [ServiceSpec] can embed it without
// creating a template ↔ provider import cycle.
type SecretRef struct {
	Key  string             `db:"key"  json:"key"`
	Type secrets.SecretType `db:"type" json:"type"`
}

// ConfigFile defines a configuration file (JSON/YAML/env/text) that is
// stored in the vault and mounted into the container at deploy time.
type ConfigFile struct {
	Name    string `db:"name"    json:"name"`    // e.g. "app-config"
	Path    string `db:"path"    json:"path"`    // mount path, e.g. "/etc/app/config.yaml"
	Format  string `db:"format"  json:"format"`  // "json", "yaml", "env", "text"
	Content string `db:"content" json:"content"` // file content (stored in vault at deploy time)
}
