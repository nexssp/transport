package middleware

import (
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/nexssp/kernel/xctx"
)

var requestIDCounter atomic.Uint64

const hexTable = "0123456789abcdef"

// writeHex16 writes exactly 16 lowercase hex characters representing v
// into dst. dst must be exactly 16 bytes; the guard documents and
// enforces that contract.
func writeHex16(dst []byte, v uint64) {
	if len(dst) != 16 {
		panic("writeHex16: destination must be exactly 16 bytes")
	}
	for i := range dst {
		dst[len(dst)-1-i] = hexTable[v&0xf]
		v >>= 4
	}
}

// RequestScope provisions a pooled xctx.RequestScope for every HTTP
// request. It MUST be the outermost middleware in the chain.
func RequestScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCtx, scope, release := xctx.NewScope(r.Context())
		defer release()

		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = generateRequestID()
		}
		scope.RequestID = reqID
		w.Header().Set("X-Request-ID", reqID)

		scope.Endpoint = r.URL.Path
		scope.ClientIP = realIP(r)

		next.ServeHTTP(w, r.WithContext(reqCtx))
	})
}

// generateRequestID produces a 24-character ID: 16 hex characters from
// a monotonic counter, then 8 hex characters from the low 32 bits of
// the nanosecond clock.
func generateRequestID() string {
	seq := requestIDCounter.Add(1)
	ts := uint64(time.Now().UnixNano())

	var buf [24]byte
	writeHex16(buf[:16], seq)
	writeHex16(buf[16:24], ts&0xffffffff)

	return string(buf[:])
}

// realIP extracts the client address without allocating.
//
// SECURITY: X-Forwarded-For and X-Real-IP are client-controlled. Only
// trust them when the deployment terminates a trusted proxy in front of
// this service.
func realIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		end := len(xff)
		for i := range xff {
			if xff[i] == ',' {
				end = i
				break
			}
		}
		start := 0
		for start < end && (xff[start] == ' ' || xff[start] == '\t') {
			start++
		}
		for end > start && (xff[end-1] == ' ' || xff[end-1] == '\t') {
			end--
		}
		return xff[start:end]
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
