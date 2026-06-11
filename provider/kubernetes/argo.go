package kubernetes

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/xraph/ctrlplane/provider"
)

// argoGVR addresses Argo CD Application custom resources.
var argoGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

const (
	argoAPIVersion      = "argoproj.io/v1alpha1"
	argoKind            = "Application"
	argoDefaultProject  = "default"
	argoInClusterServer = "https://kubernetes.default.svc"
)

// argoApplication is a minimal typed view of the Argo CD Application CR —
// only the fields ctrlplane sets. Marshaled to unstructured for the dynamic
// client; avoids a dependency on the argo-cd module.
type argoApplication struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Metadata   argoMetadata `json:"metadata"`
	Spec       argoSpec     `json:"spec"`
}

type argoMetadata struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type argoSpec struct {
	Project     string          `json:"project,omitempty"`
	Source      argoSource      `json:"source"`
	Destination argoDestination `json:"destination"`
	SyncPolicy  *argoSyncPolicy `json:"syncPolicy,omitempty"`
}

type argoSource struct {
	RepoURL        string    `json:"repoURL"`
	Path           string    `json:"path,omitempty"`
	TargetRevision string    `json:"targetRevision,omitempty"`
	Helm           *argoHelm `json:"helm,omitempty"`
}

type argoHelm struct {
	ValueFiles []string        `json:"valueFiles,omitempty"`
	Parameters []argoHelmParam `json:"parameters,omitempty"`
}

type argoHelmParam struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type argoDestination struct {
	Server    string `json:"server,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type argoSyncPolicy struct {
	Automated *argoAutomated `json:"automated,omitempty"`
}

type argoAutomated struct {
	SelfHeal bool `json:"selfHeal,omitempty"`
	Prune    bool `json:"prune,omitempty"`
}

// buildArgoApplication assembles the typed Application for a request and
// converts it to an unstructured object, applying ctrlplane defaults
// (project, in-cluster destination server).
func buildArgoApplication(req provider.ArgoApplyRequest, name, namespace string) (*unstructured.Unstructured, error) {
	src := req.App

	app := argoApplication{
		APIVersion: argoAPIVersion,
		Kind:       argoKind,
		Metadata:   argoMetadata{Name: name, Namespace: namespace, Labels: req.Labels},
		Spec: argoSpec{
			Project: src.Project,
			Source: argoSource{
				RepoURL:        src.RepoURL,
				Path:           src.Path,
				TargetRevision: src.TargetRevision,
			},
			Destination: argoDestination{Server: src.DestServer, Namespace: src.DestNamespace},
		},
	}

	if app.Spec.Project == "" {
		app.Spec.Project = argoDefaultProject
	}

	if app.Spec.Destination.Server == "" {
		app.Spec.Destination.Server = argoInClusterServer
	}

	if src.SyncPolicy.Automated {
		app.Spec.SyncPolicy = &argoSyncPolicy{
			Automated: &argoAutomated{SelfHeal: src.SyncPolicy.SelfHeal, Prune: src.SyncPolicy.Prune},
		}
	}

	if src.Helm != nil {
		h := &argoHelm{ValueFiles: src.Helm.ValueFiles}
		for k, v := range src.Helm.Parameters {
			h.Parameters = append(h.Parameters, argoHelmParam{Name: k, Value: v})
		}

		app.Spec.Source.Helm = h
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&app)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: build argo application: %w", err)
	}

	return &unstructured.Unstructured{Object: obj}, nil
}

// argoNamespace returns the namespace where Argo Application CRs live.
func (p *Provider) argoNamespace() string {
	if p.cfg.ArgoNamespace != "" {
		return p.cfg.ArgoNamespace
	}

	return "argocd"
}

// ArgoApply creates or updates the instance's Argo CD Application CR.
func (p *Provider) ArgoApply(ctx context.Context, req provider.ArgoApplyRequest) (*provider.ProvisionResult, error) {
	ns := p.argoNamespace()
	name := deploymentName(req.InstanceID)

	app, err := buildArgoApplication(req, name, ns)
	if err != nil {
		return nil, err
	}

	if err := applyVia(ctx, p.dynamic.Resource(argoGVR).Namespace(ns), app); err != nil {
		return nil, err
	}

	return &provider.ProvisionResult{
		ProviderRef: "argocd:" + ns + "/" + name,
	}, nil
}

// argoStateFor maps an Argo CD Application's sync and health status to a
// ctrlplane InstanceState. Health is primary; sync refines the healthy case
// (healthy-but-out-of-sync is still converging).
func argoStateFor(sync, health string) provider.InstanceState {
	switch health {
	case "Healthy":
		if sync == "Synced" {
			return provider.StateRunning
		}

		return provider.StateStarting
	case "Progressing":
		return provider.StateStarting
	case "Degraded":
		return provider.StateFailed
	case "Suspended":
		return provider.StateStopped
	default:
		return provider.StateProvisioning
	}
}
