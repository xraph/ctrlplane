package nomad

import (
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
)

// Option configures a Nomad provider.
type Option func(*Provider) error

// WithAddress sets the Nomad API endpoint.
func WithAddress(addr string) Option {
	return func(p *Provider) error {
		if addr == "" {
			return fmt.Errorf("nomad: %w: address must not be empty", ctrlplane.ErrInvalidConfig)
		}

		p.cfg.Address = addr

		return nil
	}
}

// WithToken sets the Nomad ACL token for authentication.
func WithToken(token string) Option {
	return func(p *Provider) error {
		p.cfg.Token = token

		return nil
	}
}

// WithRegion sets the Nomad region to target.
func WithRegion(region string) Option {
	return func(p *Provider) error {
		p.cfg.Region = region

		return nil
	}
}

// WithNamespace sets the Nomad namespace for job submissions.
func WithNamespace(ns string) Option {
	return func(p *Provider) error {
		p.cfg.Namespace = ns

		return nil
	}
}

// WithConfig applies all non-zero fields from a Config struct.
// This is useful when loading configuration from files or environment variables.
func WithConfig(cfg Config) Option {
	return func(p *Provider) error {
		if cfg.Address != "" {
			p.cfg.Address = cfg.Address
		}

		if cfg.Token != "" {
			p.cfg.Token = cfg.Token
		}

		if cfg.Region != "" {
			p.cfg.Region = cfg.Region
		}

		if cfg.Namespace != "" {
			p.cfg.Namespace = cfg.Namespace
		}

		return nil
	}
}
