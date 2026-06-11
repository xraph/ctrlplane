// Package dispatch routes a rendered deployment source to the appropriate
// provider operation: container services go through the core Provider
// lifecycle, while helm, manifests, and argocd sources are delegated to the
// provider's optional engine interfaces (gated by capability). It is the
// seam that lets one provisioning path serve every source type.
package dispatch

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// Request carries everything needed to provision an instance from a
// rendered source, independent of source type.
type Request struct {
	InstanceID id.ID
	TenantID   string
	Name       string
	Namespace  string
	Kind       provider.WorkloadKind
	Source     provider.RenderedSource
	Labels     map[string]string
}

// Provision routes a rendered source to the right provider engine. Services
// use the core Provision; other types require the provider to implement the
// matching engine interface and advertise its capability, else
// ctrlplane.ErrUnsupportedSource.
func Provision(ctx context.Context, p provider.Provider, req Request) (*provider.ProvisionResult, error) {
	switch req.Source.Type {
	case provider.SourceServices:
		return p.Provision(ctx, provider.ProvisionRequest{
			InstanceID: req.InstanceID,
			TenantID:   req.TenantID,
			Name:       req.Name,
			Kind:       req.Kind,
			Services:   req.Source.Services,
			Labels:     req.Labels,
		})
	case provider.SourceManifests:
		eng, ok := manifestEngine(p)
		if !ok {
			return nil, unsupported(req.Source.Type)
		}

		var docs provider.RenderedManifests
		if req.Source.Manifests != nil {
			docs = *req.Source.Manifests
		}

		return eng.ApplyManifests(ctx, provider.ManifestApplyRequest{
			InstanceID: req.InstanceID,
			TenantID:   req.TenantID,
			Namespace:  req.Namespace,
			Manifests:  docs,
			Labels:     req.Labels,
		})
	case provider.SourceHelm:
		eng, ok := helmEngine(p)
		if !ok {
			return nil, unsupported(req.Source.Type)
		}

		var chart provider.RenderedHelm
		if req.Source.Helm != nil {
			chart = *req.Source.Helm
		}

		return eng.HelmInstall(ctx, provider.HelmInstallRequest{
			InstanceID: req.InstanceID,
			TenantID:   req.TenantID,
			Namespace:  req.Namespace,
			Chart:      chart,
		})
	case provider.SourceArgoCD:
		eng, ok := argoEngine(p)
		if !ok {
			return nil, unsupported(req.Source.Type)
		}

		var app provider.ArgoCDSource
		if req.Source.ArgoCD != nil {
			app = *req.Source.ArgoCD
		}

		return eng.ArgoApply(ctx, provider.ArgoApplyRequest{
			InstanceID: req.InstanceID,
			TenantID:   req.TenantID,
			App:        app,
			Labels:     req.Labels,
		})
	default:
		return nil, fmt.Errorf("%w: %q", ctrlplane.ErrInvalidSource, req.Source.Type)
	}
}

// unsupported builds the capability-gated error.
func unsupported(t provider.SourceType) error {
	return fmt.Errorf("%w: %q", ctrlplane.ErrUnsupportedSource, t)
}

// manifestEngine returns the provider as a ManifestEngine when it both
// implements the interface and advertises the capability.
func manifestEngine(p provider.Provider) (provider.ManifestEngine, bool) {
	eng, ok := p.(provider.ManifestEngine)

	return eng, ok && provider.HasCapability(p, provider.CapManifests)
}

// helmEngine returns the provider as a HelmEngine when supported.
func helmEngine(p provider.Provider) (provider.HelmEngine, bool) {
	eng, ok := p.(provider.HelmEngine)

	return eng, ok && provider.HasCapability(p, provider.CapHelm)
}

// argoEngine returns the provider as an ArgoEngine when supported.
func argoEngine(p provider.Provider) (provider.ArgoEngine, bool) {
	eng, ok := p.(provider.ArgoEngine)

	return eng, ok && provider.HasCapability(p, provider.CapArgoCD)
}
