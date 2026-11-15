package middleware

import (
	"io"
	"net/http"
)

// RequestBodyLimit rejects requests whose declared body is larger than maxBytes
// and wraps other bodies with http.MaxBytesReader before the handler runs. This
// bounds reads by typed routes, raw routes, and raw handlers. A raw handler must
// check read errors so it can turn an unknown-length overflow into an HTTP 413.
// A non-positive limit leaves the request unchanged.
func RequestBodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	if maxBytes <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				writeBodyTooLarge(w)
				return
			}
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeBodyTooLarge(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusRequestEntityTooLarge)
	if _, err := io.WriteString(w, `{"error":"request body too large"}`+"\n"); err != nil {
		return
	}
}
