package thttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/transport"
)

var sequence atomic.Uint64

type AdapterConfig struct {
	MaxBodyBytes        int64
	GenerateExecutionID bool
	Methods             []string
}

type AdapterOption func(*AdapterConfig)

func WithMaxBodyBytes(n int64) AdapterOption {
	return func(c *AdapterConfig) {
		if n > 0 {
			c.MaxBodyBytes = n
		}
	}
}

func WithMethods(methods ...string) AdapterOption {
	return func(c *AdapterConfig) {
		if len(methods) > 0 {
			c.Methods = append(c.Methods[:0], methods...)
		}
	}
}

func HTTP[Req, Res any](act *action.BuiltAction[Req, Res], options ...AdapterOption) http.Handler {
	cfg := AdapterConfig{
		MaxBodyBytes:        10 << 20,
		GenerateExecutionID: true,
		Methods:             []string{http.MethodPost},
	}
	for _, opt := range options {
		if opt != nil {
			opt(&cfg)
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedMethod(r.Method, cfg.Methods) {
			w.Header().Set("Allow", strings.Join(cfg.Methods, ", "))
			writeErrorWithRequestID(w, r, xerr.MethodNotAllowed("method not allowed"), r.Header.Get(transport.HeaderRequestID))
			return
		}
		if act == nil {
			writeErrorWithRequestID(w, r, xerr.Internal("nil action"), r.Header.Get(transport.HeaderRequestID))
			return
		}

		ctx := r.Context()
		requestID := r.Header.Get(transport.HeaderRequestID)
		executionID := r.Header.Get(transport.HeaderExecutionID)
		if executionID == "" && cfg.GenerateExecutionID {
			executionID = "exec-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.FormatUint(sequence.Add(1), 10)
		}
		if executionID != "" {
			ctx = action.WithExecutionID(ctx, executionID)
			w.Header().Set(transport.HeaderExecutionID, executionID)
		}
		if requestID != "" {
			w.Header().Set(transport.HeaderRequestID, requestID)
		}
		traceID, spanID := r.Header.Get(transport.HeaderTraceID), r.Header.Get(transport.HeaderSpanID)
		if traceID != "" || spanID != "" {
			ctx = action.WithTraceContext(ctx, traceID, spanID)
		}
		if cfg.MaxBodyBytes > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxBodyBytes)
		}
		defer r.Body.Close()

		decoder := json.NewDecoder(r.Body)
		decodeFailed := false
		decode := func(v any) error {
			if err := decoder.Decode(v); err != nil {
				if errors.Is(err, io.EOF) {
					return nil // Treat empty body safely as zero-value payload
				}
				decodeFailed = true
				return err
			}
			var extra any
			if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
				decodeFailed = true
				if err == nil {
					return errors.New("multiple JSON values in payload")
				}
				return err
			}
			return nil
		}

		result, err := act.ExecuteDecoded(ctx, decode)
		if err != nil {
			if decodeFailed || strings.Contains(err.Error(), "request body too large") {
				err = xerr.BadRequest("invalid JSON payload", err)
			}
			writeErrorWithRequestID(w, r, err, requestID)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}

func allowedMethod(method string, methods []string) bool {
	for _, m := range methods {
		if method == m {
			return true
		}
	}
	return false
}

func writeErrorWithRequestID(w http.ResponseWriter, _ *http.Request, err error, requestID string) {
	appErr := xerr.From(err)
	status := transport.MapToHTTPStatus(appErr.Kind)
	response := appErr.Public(requestID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}
