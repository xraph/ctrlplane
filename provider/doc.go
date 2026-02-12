// Package provider defines the unified interface for infrastructure operations.
// Each cloud provider and orchestrator (Kubernetes, Nomad, AWS ECS, Docker,
// Fly.io, etc.) implements the Provider interface, giving library consumers a
// single API for all infrastructure operations.
package provider
