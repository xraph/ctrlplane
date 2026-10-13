package provider

import (
	"context"
	"time"
)

// Credentials is what a provider needs to authenticate one call against
// its backend. Values suits flat secrets (an access key and its secret, an
// API token). Raw carries a provider-specific object, such as an OAuth2
// token, when a flat map does not fit. Neither is ever serialized.
type Credentials struct {
	Values    map[string]string `json:"-"`
	Raw       any               `json:"-"`
	ExpiresAt time.Time         `json:"-"`
}

// CredentialsResolver returns the credentials for the provider registered
// under providerKey. Ctrl Plane calls it before each provider call when you
// set one on the Registry, so a resolver is the place to read a vault, mint
// a short-lived token, or refresh one that is about to expire. Cache inside
// the resolver if fetching is expensive.
type CredentialsResolver interface {
	ResolveCredentials(ctx context.Context, providerKey string) (*Credentials, error)
}

// CredentialsResolverFunc adapts a function to CredentialsResolver.
type CredentialsResolverFunc func(ctx context.Context, providerKey string) (*Credentials, error)

// ResolveCredentials implements CredentialsResolver.
func (f CredentialsResolverFunc) ResolveCredentials(ctx context.Context, providerKey string) (*Credentials, error) {
	return f(ctx, providerKey)
}

type credentialsKey struct{}

// ContextWithCredentials returns a copy of ctx carrying creds. The Registry
// does this for you in Acquire; call it yourself only when you invoke a
// provider directly.
func ContextWithCredentials(ctx context.Context, creds *Credentials) context.Context {
	return context.WithValue(ctx, credentialsKey{}, creds)
}

// CredentialsFromContext returns the credentials attached to ctx for this
// call. A provider that supports per-call credentials reads them here and
// falls back to whatever it was constructed with when ok is false, which is
// the case whenever no CredentialsResolver is configured.
func CredentialsFromContext(ctx context.Context) (*Credentials, bool) {
	creds, ok := ctx.Value(credentialsKey{}).(*Credentials)

	return creds, ok && creds != nil
}
