package network

import (
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
)

// Domain represents a custom domain bound to an instance.
type Domain struct {
	ctrlplane.Entity

	TenantID    string     `db:"tenant_id"    json:"tenant_id"`
	InstanceID  id.ID      `db:"instance_id"  json:"instance_id"`
	Hostname    string     `db:"hostname"     json:"hostname"`
	Verified    bool       `db:"verified"     json:"verified"`
	TLSEnabled  bool       `db:"tls_enabled"  json:"tls_enabled"`
	CertExpiry  *time.Time `db:"cert_expiry"  json:"cert_expiry,omitempty"`
	DNSTarget   string     `db:"dns_target"   json:"dns_target"`
	VerifyToken string     `db:"verify_token" json:"verify_token"`
}

// Route maps traffic from an endpoint to an instance.
type Route struct {
	ctrlplane.Entity

	TenantID    string `db:"tenant_id"    json:"tenant_id"`
	InstanceID  id.ID  `db:"instance_id"  json:"instance_id"`
	Path        string `db:"path"         json:"path"`
	Port        int    `db:"port"         json:"port"`
	Protocol    string `db:"protocol"     json:"protocol"`
	Weight      int    `db:"weight"       json:"weight"`
	StripPrefix bool   `db:"strip_prefix" json:"strip_prefix"`
}

// Certificate holds TLS certificate state.
type Certificate struct {
	ctrlplane.Entity

	DomainID  id.ID     `db:"domain_id"  json:"domain_id"`
	TenantID  string    `db:"tenant_id"  json:"tenant_id"`
	Issuer    string    `db:"issuer"     json:"issuer"`
	ExpiresAt time.Time `db:"expires_at" json:"expires_at"`
	AutoRenew bool      `db:"auto_renew" json:"auto_renew"`
}
