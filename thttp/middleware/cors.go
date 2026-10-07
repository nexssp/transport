package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Option configures CORS middleware.
type Option func(*corsConfig) error

type corsConfig struct {
	origins        []string
	originMap      map[string]struct{}
	allowAll       bool
	headers        []string
	headerMap      map[string]struct{}
	headerFallback string
	methods        []string
	methodsString  string
	exposed        string
	maxAge         time.Duration
	allowCred      bool
}

var defaultMethods = []string{
	http.MethodGet, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodHead,
}

// WithOrigins sets the allowed origins. Use "*" to allow any origin. Wildcard
// origins cannot be combined with credentials.
func WithOrigins(origins ...string) Option {
	return func(config *corsConfig) error {
		for _, origin := range origins {
			if strings.ContainsAny(origin, "\r\n") {
				return fmt.Errorf("invalid origin %q", origin)
			}
		}
		config.origins = append([]string(nil), origins...)
		return nil
	}
}

// WithHeaders sets allowed request headers for preflight requests.
func WithHeaders(headers ...string) Option {
	return func(config *corsConfig) error {
		for _, header := range headers {
			if strings.ContainsAny(header, "\r\n") {
				return fmt.Errorf("invalid header %q", header)
			}
		}
		config.headers = append([]string(nil), headers...)
		return nil
	}
}

// WithMethods overrides the methods advertised for preflight requests.
func WithMethods(methods ...string) Option {
	return func(config *corsConfig) error {
		if len(methods) == 0 {
			return errors.New("at least one method required")
		}
		for _, method := range methods {
			if strings.ContainsAny(method, "\r\n") {
				return fmt.Errorf("invalid method %q", method)
			}
		}
		config.methods = append([]string(nil), methods...)
		return nil
	}
}

// WithExposedHeaders sets response headers visible to browser JavaScript.
func WithExposedHeaders(headers ...string) Option {
	return func(config *corsConfig) error {
		for _, header := range headers {
			if strings.ContainsAny(header, "\r\n") {
				return fmt.Errorf("invalid exposed header %q", header)
			}
		}
		config.exposed = strings.Join(headers, ", ")
		return nil
	}
}

// WithMaxAge sets the preflight cache lifetime in seconds.
func WithMaxAge(seconds int) Option {
	return func(config *corsConfig) error {
		if seconds < 0 {
			return fmt.Errorf("maxAge must be >= 0, got %d", seconds)
		}
		const maxDurationSeconds = int64(1<<63-1) / int64(time.Second)
		if int64(seconds) > maxDurationSeconds {
			return fmt.Errorf("maxAge is too large: %d", seconds)
		}
		config.maxAge = time.Duration(seconds) * time.Second
		return nil
	}
}

// WithCredentials enables Access-Control-Allow-Credentials. It cannot be used
// with a wildcard origin.
func WithCredentials() Option {
	return func(config *corsConfig) error {
		config.allowCred = true
		return nil
	}
}

// WithEnv reads CORS_ORIGINS, CORS_HEADERS, CORS_METHODS, CORS_EXPOSED,
// CORS_MAX_AGE, and CORS_CREDENTIALS from the process environment. It is
// evaluated when CORS is constructed, not on each request.
func WithEnv() Option {
	return func(config *corsConfig) error {
		if value := os.Getenv("CORS_ORIGINS"); value != "" {
			if err := WithOrigins(splitCSV(value)...)(config); err != nil {
				return err
			}
		}
		if value := os.Getenv("CORS_HEADERS"); value != "" {
			if err := WithHeaders(splitCSV(value)...)(config); err != nil {
				return err
			}
		}
		if value := os.Getenv("CORS_METHODS"); value != "" {
			if err := WithMethods(splitCSV(value)...)(config); err != nil {
				return err
			}
		}
		if value := os.Getenv("CORS_EXPOSED"); value != "" {
			if err := WithExposedHeaders(splitCSV(value)...)(config); err != nil {
				return err
			}
		}
		if value := os.Getenv("CORS_MAX_AGE"); value != "" {
			seconds, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("CORS_MAX_AGE: %w", err)
			}
			if err := WithMaxAge(seconds)(config); err != nil {
				return err
			}
		}
		if strings.EqualFold(os.Getenv("CORS_CREDENTIALS"), "true") {
			return WithCredentials()(config)
		}
		return nil
	}
}

// CORS validates the configuration immediately and returns an opt-in middleware.
func CORS(options ...Option) (func(http.Handler) http.Handler, error) {
	config := &corsConfig{methods: append([]string(nil), defaultMethods...)}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(config); err != nil {
			return nil, fmt.Errorf("CORS: %w", err)
		}
	}
	if len(config.origins) == 0 {
		return nil, errors.New("CORS: at least one origin required")
	}
	config.originMap = make(map[string]struct{}, len(config.origins))
	for _, origin := range config.origins {
		if origin == "*" {
			config.allowAll = true
		} else {
			config.originMap[origin] = struct{}{}
		}
	}
	if config.allowAll && config.allowCred {
		return nil, errors.New("CORS: wildcard origin cannot be combined with credentials")
	}
	config.headerMap = make(map[string]struct{}, len(config.headers))
	for _, header := range config.headers {
		config.headerMap[strings.ToLower(header)] = struct{}{}
	}
	if len(config.headers) > 0 {
		config.headerFallback = strings.Join(config.headers, ", ")
	}
	config.methodsString = strings.Join(config.methods, ", ")
	middleware := &corsMiddleware{config: config}
	return middleware.handle, nil
}

// MustCORS panics when CORS configuration is invalid, for applications that
// prefer configuration failures during startup.
func MustCORS(options ...Option) func(http.Handler) http.Handler {
	middleware, err := CORS(options...)
	if err != nil {
		panic(err)
	}
	return middleware
}

type corsMiddleware struct{ config *corsConfig }

func (middleware *corsMiddleware) handle(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		w.Header().Add("Vary", "Origin")
		if origin == "" || !middleware.isAllowed(origin) {
			next.ServeHTTP(w, r)
			return
		}
		if middleware.config.allowAll {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			if middleware.config.allowCred {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
		}
		if middleware.config.exposed != "" {
			w.Header().Set("Access-Control-Expose-Headers", middleware.config.exposed)
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Add("Vary", "Access-Control-Request-Headers")
			w.Header().Set("Access-Control-Allow-Methods", middleware.config.methodsString)
			if middleware.config.maxAge > 0 {
				w.Header().Set("Access-Control-Max-Age", strconv.Itoa(int(middleware.config.maxAge.Seconds())))
			}
			requestedHeaders := r.Header.Get("Access-Control-Request-Headers")
			if requestedHeaders != "" {
				if allowed := middleware.filterHeaders(requestedHeaders); allowed != "" {
					w.Header().Set("Access-Control-Allow-Headers", allowed)
				}
			} else if middleware.config.headerFallback != "" {
				w.Header().Set("Access-Control-Allow-Headers", middleware.config.headerFallback)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (middleware *corsMiddleware) isAllowed(origin string) bool {
	if strings.ContainsAny(origin, "\r\n\t ") {
		return false
	}
	if middleware.config.allowAll {
		return true
	}
	_, ok := middleware.config.originMap[origin]
	return ok
}

func (middleware *corsMiddleware) filterHeaders(requested string) string {
	allowed := make([]string, 0)
	for header := range strings.SplitSeq(requested, ",") {
		header = strings.TrimSpace(header)
		if header == "" {
			continue
		}
		if _, ok := middleware.config.headerMap[strings.ToLower(header)]; ok {
			allowed = append(allowed, header)
		}
	}
	return strings.Join(allowed, ", ")
}

func splitCSV(value string) []string { return strings.Split(value, ",") }
