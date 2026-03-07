package kubernetes

import (
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
)

// Option configures a Kubernetes provider.
type Option func(*Provider) error

// WithKubeconfig sets the path to a kubeconfig file.
// When empty, in-cluster configuration is used.
func WithKubeconfig(path string) Option {
	return func(p *Provider) error {
		p.cfg.Kubeconfig = path

		return nil
	}
}

// WithContext sets the kubeconfig context to use.
func WithContext(ctx string) Option {
	return func(p *Provider) error {
		p.cfg.Context = ctx

		return nil
	}
}

// WithNamespace sets the Kubernetes namespace for all managed resources.
func WithNamespace(ns string) Option {
	return func(p *Provider) error {
		if ns == "" {
			return fmt.Errorf("kubernetes: %w: namespace must not be empty", ctrlplane.ErrInvalidConfig)
		}

		p.cfg.Namespace = ns

		return nil
	}
}

// WithRegion sets the region label reported in provider info.
func WithRegion(region string) Option {
	return func(p *Provider) error {
		p.cfg.Region = region

		return nil
	}
}

// WithLabels sets additional labels applied to all managed resources.
func WithLabels(labels map[string]string) Option {
	return func(p *Provider) error {
		p.cfg.Labels = labels

		return nil
	}
}

// WithInCluster forces in-cluster configuration only, skipping the local
// kubeconfig fallback. Use this in production to prevent accidentally
// picking up a developer's local kubeconfig.
func WithInCluster() Option {
	return func(p *Provider) error {
		p.cfg.InCluster = true

		return nil
	}
}

// WithConfig applies all non-zero fields from a Config struct.
// This is useful when loading configuration from files or environment variables.
func WithConfig(cfg Config) Option {
	return func(p *Provider) error {
		if cfg.Kubeconfig != "" {
			p.cfg.Kubeconfig = cfg.Kubeconfig
		}

		if cfg.Context != "" {
			p.cfg.Context = cfg.Context
		}

		if cfg.Namespace != "" {
			p.cfg.Namespace = cfg.Namespace
		}

		if cfg.Region != "" {
			p.cfg.Region = cfg.Region
		}

		if cfg.InCluster {
			p.cfg.InCluster = true
		}

		if cfg.Labels != nil {
			p.cfg.Labels = cfg.Labels
		}

		return nil
	}
}
