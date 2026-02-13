package api

import (
	"encoding/json"
	"net/http"

	"github.com/xraph/forge"

	"github.com/xraph/ctrlplane/auth"
)

// authMiddleware wraps an http.Handler with bearer token authentication.
// Used for standalone mode where the API manages its own routing.
func (a *API) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")

		claims, err := a.cp.Auth().Authenticate(r.Context(), token)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)

			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": err.Error(),
			})

			return
		}

		ctx := auth.WithClaims(r.Context(), claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AuthForgeMiddleware returns a Forge middleware that performs bearer token
// authentication. Use this when registering routes into an external Forge
// router (extension mode).
func (a *API) AuthForgeMiddleware() forge.Middleware {
	return func(next forge.Handler) forge.Handler {
		return func(ctx forge.Context) error {
			token := ctx.Header("Authorization")

			claims, err := a.cp.Auth().Authenticate(ctx.Context(), token)
			if err != nil {
				return forge.Unauthorized(err.Error())
			}

			ctx.WithContext(auth.WithClaims(ctx.Context(), claims))

			return next(ctx)
		}
	}
}
