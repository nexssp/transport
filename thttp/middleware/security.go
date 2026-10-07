package middleware

import "net/http"

// SecurityHeaders applies a conservative set of HTTP hardening headers
// to every response. The set is deliberately narrow: it improves
// defaults without being so opinionated that it breaks legitimate
// same-host UI such as Scalar docs, GraphiQL, or embedded consoles.
//
// Content-Security-Policy is intentionally NOT set. CSP is genuinely
// application-specific — a policy that works for a JSON API breaks a
// dashboard with inline styles — and silently shipping a default has
// caused more outages than it has prevented. Applications that serve
// HTML should add their own CSP middleware after this one.
//
// Zero-allocation contract: net/http pre-sizes the response header map,
// so each Set is a map write without growth. No allocation on the fast
// path.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")

		// HSTS is meaningful only over TLS. Setting it on plain HTTP is
		// inert for the browser and pollutes reverse-proxy logs.
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}
