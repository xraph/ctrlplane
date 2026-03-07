package docker

import (
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
)

// Option configures a Docker provider.
type Option func(*Provider) error

// WithHost sets the Docker daemon address (e.g., "unix:///var/run/docker.sock").
func WithHost(host string) Option {
	return func(p *Provider) error {
		p.cfg.Host = host

		return nil
	}
}

// WithNetwork sets the Docker network to attach containers to.
func WithNetwork(network string) Option {
	return func(p *Provider) error {
		if network == "" {
			return fmt.Errorf("docker: %w: network must not be empty", ctrlplane.ErrInvalidConfig)
		}

		p.cfg.Network = network

		return nil
	}
}

// WithRegistry sets the default image registry prefix.
func WithRegistry(registry string) Option {
	return func(p *Provider) error {
		p.cfg.Registry = registry

		return nil
	}
}

// WithConfig applies all non-zero fields from a Config struct.
// This is useful when loading configuration from files or environment variables.
func WithConfig(cfg Config) Option {
	return func(p *Provider) error {
		if cfg.Host != "" {
			p.cfg.Host = cfg.Host
		}

		if cfg.Network != "" {
			p.cfg.Network = cfg.Network
		}

		if cfg.Registry != "" {
			p.cfg.Registry = cfg.Registry
		}

		return nil
	}
}
