package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/nexssp/kernel/xctx"
	"github.com/nexssp/kernel/xerr"
)

// Recovery catches panics that escape from downstream handlers and
// middleware, converts them into a 500 response, and logs the panic with
// its stack trace.
func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			defer func(ctx context.Context) {
				rec := recover()
				if rec == nil {
					return
				}

				appErr := xerr.PanicRecovery(rec)

				reqID := xctx.RequestIDFrom(ctx)
				endpoint := xctx.EndpointFrom(ctx)

				logger.ErrorContext(ctx, "http_handler_panic",
					"request_id", reqID,
					"endpoint", endpoint,
					"method", r.Method,
					"path", r.URL.Path,
					"panic", rec,
					"stack", string(debug.Stack()),
				)

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)

				//nolint:errcheck // response write failure is terminal
				_ = writeErrorJSON(w, appErr.Public(reqID))
			}(ctx)

			next.ServeHTTP(w, r)
		})
	}
}

// writeErrorJSON keeps the Recovery middleware self-contained without
// importing thttp (which would create an import cycle).
func writeErrorJSON(w http.ResponseWriter, v any) error {
	switch t := v.(type) {
	case interface{ MarshalJSON() ([]byte, error) }:
		b, err := t.MarshalJSON()
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	default:
		_, err := w.Write([]byte(`{"error":"Internal","message":"internal server error"}`))
		return err
	}
}
