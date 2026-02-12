package api

import (
	"net/http"

	"github.com/xraph/ctrlplane/auth"
)

// AuthMiddleware extracts the bearer token, authenticates it, and stores
// claims in the request context.
func (a *API) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")

		claims, err := a.cp.Auth().Authenticate(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err)

			return
		}

		ctx := auth.WithClaims(r.Context(), claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
