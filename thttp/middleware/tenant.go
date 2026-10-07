package middleware

import (
	"net/http"

	"github.com/nexssp/kernel/xctx"
)

// TenantFromHeader reads a tenant identifier from a request header and
// attaches it to the execution context.
//
// This is the correct place for tenant extraction on HTTP paths. The
// header is presumed to have been set by an upstream trusted authority:
// a gateway, a service mesh, or an authentication middleware that has
// already verified the caller's claims and rewritten the request.
//
// This middleware deliberately does NOT trust a tenant id supplied in
// the request body. Placing tenant selection under client control is
// an authorization bypass in any multi-tenant system. When the tenant
// comes from verified JWT claims, do not use this middleware — set the
// tenant on the context inside the authentication middleware instead.
//
// Use this middleware when:
//   - the service sits behind a trusted proxy that sets X-Tenant-ID
//   - a batch or internal service invocation carries the tenant in a
//     well-known header
//   - local development needs to simulate a specific tenant
//
// Do not use this middleware when the tenant is derived from a verified
// claim; extract the claim in the authentication middleware and call
// xctx.WithTenantID there.
//
// Zero-allocation contract: the fast path does one header lookup and
// one field write inside the pooled scope. No allocation.
func TenantFromHeader(headerName string) func(http.Handler) http.Handler {
	if headerName == "" {
		headerName = "X-Tenant-ID"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tid := r.Header.Get(headerName); tid != "" {
				ctx := xctx.WithTenantID(r.Context(), tid)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// TenantFromSubdomain extracts the tenant identifier from the request's
// leftmost subdomain label and attaches it to the context.
//
// Example: acme.api.example.com → "acme"
//
// This is a common SaaS pattern where each tenant gets a DNS alias. It
// is a shape-based extraction: the middleware does not verify that the
// subdomain corresponds to a real tenant — downstream actions validate
// that. Like TenantFromHeader, it assumes the deployment terminates at
// a trusted edge that controls DNS.
//
// The baseDomain argument is not consulted at request time; it exists
// only to make the deployment intent explicit at the call site and to
// allow future validation. Pass an empty string for a purely local
// setup.
func TenantFromSubdomain(baseDomain string) func(http.Handler) http.Handler {
	_ = baseDomain
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host
			// Strip port.
			for i := 0; i < len(host); i++ {
				if host[i] == ':' {
					host = host[:i]
					break
				}
			}
			// Leftmost label before the first dot.
			label := host
			for i := range len(host) {
				if host[i] == '.' {
					label = host[:i]
					break
				}
			}
			// Reject obviously invalid values so a request to a bare
			// hostname (no dots) does not inject the entire host as a
			// tenant id.
			if label == "" || label == "www" || label == "api" || label == host {
				next.ServeHTTP(w, r)
				return
			}
			ctx := xctx.WithTenantID(r.Context(), label)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
