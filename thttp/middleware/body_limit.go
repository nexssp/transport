package middleware

import (
	"io"
	"net/http"
)

// RequestBodyLimit rejects requests whose declared body is larger than maxBytes
// and wraps other bodies with http.MaxBytesReader before the handler runs. This
// bounds reads by typed routes, raw routes, and raw handlers. A raw handler must
// check read errors so it can turn an unknown-length overflow into an HTTP error.
// A non-positive limit leaves the request unchanged. The optional rejection
// status defaults to 413; values outside 400–599 are ignored.
func RequestBodyLimit(maxBytes int64, statuses ...int) func(http.Handler) http.Handler {
	bodyLimitStatus := http.StatusRequestEntityTooLarge
	if len(statuses) > 0 && statuses[0] >= http.StatusBadRequest && statuses[0] <= 599 {
		bodyLimitStatus = statuses[0]
	}
	if maxBytes <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				writeBodyTooLarge(w, bodyLimitStatus)
				return
			}
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeBodyTooLarge(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := io.WriteString(w, `{"error":"request body too large"}`+"\n"); err != nil {
		return
	}
}
