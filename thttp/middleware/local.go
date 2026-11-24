package middleware

import (
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// LoopbackOnly rejects requests unless the peer, Host, and any supplied Origin
// are local to the machine. It intentionally does not trust forwarded headers.
func LoopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !isLoopbackRemoteAddr(request.RemoteAddr) || !isLoopbackHost(request.Host) || !sameLocalOrigin(request) {
			writer.Header().Set("Cache-Control", "no-store")
			http.Error(writer, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

// MaxConcurrentRequests rejects excess work immediately instead of queueing it.
// A non-positive limit leaves the handler unchanged.
func MaxConcurrentRequests(limit int) func(http.Handler) http.Handler {
	if limit <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	semaphore := make(chan struct{}, limit)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
				next.ServeHTTP(writer, request)
			default:
				writer.Header().Set("Retry-After", "1")
				http.Error(writer, "server busy", http.StatusServiceUnavailable)
			}
		})
	}
}

func isLoopbackRemoteAddr(remoteAddr string) bool {
	address, err := netip.ParseAddrPort(remoteAddr)
	return err == nil && address.Addr().IsLoopback()
}

func isLoopbackHost(host string) bool {
	parsed, err := url.Parse("//" + host)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" {
		return false
	}
	name := parsed.Hostname()
	if strings.EqualFold(name, "localhost") {
		return true
	}
	address, err := netip.ParseAddr(name)
	return err == nil && address.IsLoopback()
}

func sameLocalOrigin(request *http.Request) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return strings.EqualFold(parsed.Scheme, scheme) && strings.EqualFold(parsed.Host, request.Host) && isLoopbackHost(parsed.Host)
}
