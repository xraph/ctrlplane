package auth

import "context"

type ctxKey struct{}

// WithClaims stores Claims in the context.
func WithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// ClaimsFrom retrieves Claims from context. Returns nil if absent.
func ClaimsFrom(ctx context.Context) *Claims {
	c, _ := ctx.Value(ctxKey{}).(*Claims)

	return c
}

// RequireClaims retrieves Claims or returns ErrUnauthorized.
func RequireClaims(ctx context.Context) (*Claims, error) {
	c := ClaimsFrom(ctx)
	if c == nil {
		return nil, ErrUnauthorized
	}

	return c, nil
}
