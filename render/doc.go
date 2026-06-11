// Package render resolves a provider.DeploymentSource against a resolved
// variable scope into a concrete provider.RenderedSource the provider can
// apply. It templates services, helm values, manifest YAML, and argocd
// fields with Go text/template, and builds kustomize sources in memory.
//
// Secret-typed variables are excluded from the scope by the vars resolver,
// so any inline reference to one ({{ .var.<secret> }}) fails with a missing
// key — the render-time enforcement of "secrets are never inlined".
package render
