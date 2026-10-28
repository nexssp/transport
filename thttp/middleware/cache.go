package middleware

import (
	"net/http"
	"strings"
)

// NoStoreAPIResponses disables caching for API paths under /api/.
func NoStoreAPIResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
