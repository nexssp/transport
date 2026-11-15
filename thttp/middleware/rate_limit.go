package middleware

import (
	"container/list"
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nexssp/kernel/action"
	"golang.org/x/time/rate"
)

const (
	defaultRateLimitMaxKeys = 65_536
	defaultRateLimitTTL     = 3 * time.Minute
	maxRateLimitEvictions   = 16
)

// RateLimitConfig controls a process-local HTTP request limiter. Requests are
// keyed by the connected peer address unless KeyFunc is supplied. Forwarded-IP
// headers are deliberately not trusted; behind a proxy, provide a KeyFunc based
// on verified identity if the proxy address should not be the quota key.
//
// Limiter may be set to a Kernel action.RateLimiter for shared or distributed
// quota state. If it is nil, the middleware uses a bounded in-memory token
// bucket per key. RequestsPerSecond must be positive for the in-memory limiter;
// Burst defaults to one. MaxKeys and IdleTTL bound and age local key state.
type RateLimitConfig struct {
	RequestsPerSecond float64
	Burst             int
	KeyFunc           func(*http.Request) string
	MaxKeys           int
	IdleTTL           time.Duration
	Limiter           action.RateLimiter
}

// NewRateLimit constructs an HTTP middleware that rejects excess requests with
// status 429. A supplied Kernel limiter is treated as fail-closed: backend
// errors return 503 instead of allowing requests through.
func NewRateLimit(cfg RateLimitConfig) (func(http.Handler) http.Handler, error) {
	if cfg.Limiter == nil {
		if cfg.RequestsPerSecond <= 0 || math.IsNaN(cfg.RequestsPerSecond) || math.IsInf(cfg.RequestsPerSecond, 0) {
			return nil, errors.New("rate limit requests per second must be finite and greater than zero")
		}
		if cfg.Burst < 1 {
			cfg.Burst = 1
		}
		if cfg.MaxKeys <= 0 {
			cfg.MaxKeys = defaultRateLimitMaxKeys
		}
		if cfg.IdleTTL <= 0 {
			cfg.IdleTTL = defaultRateLimitTTL
		}
		cfg.Limiter = newKeyedRateLimiter(cfg.RequestsPerSecond, cfg.Burst, cfg.MaxKeys, cfg.IdleTTL)
	}
	if cfg.KeyFunc == nil {
		cfg.KeyFunc = requestClientIP
	}

	retryAfter := "1"
	if cfg.RequestsPerSecond > 0 && !math.IsNaN(cfg.RequestsPerSecond) && !math.IsInf(cfg.RequestsPerSecond, 0) {
		seconds := math.Ceil(1 / cfg.RequestsPerSecond)
		if seconds < 1 {
			seconds = 1
		}
		if seconds > 86_400 {
			seconds = 86_400
		}
		retryAfter = strconv.Itoa(int(seconds))
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Context().Err() != nil {
				return
			}
			allowed, err := cfg.Limiter.Allow(r.Context(), cfg.KeyFunc(r))
			if err != nil {
				if r.Context().Err() != nil {
					return
				}
				http.Error(w, "rate limiter unavailable", http.StatusServiceUnavailable)
				return
			}
			if !allowed {
				w.Header().Set("Retry-After", retryAfter)
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

func requestClientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

type keyedRateLimiter struct {
	mu                sync.Mutex
	entries           map[string]*list.Element
	order             *list.List
	requestsPerSecond rate.Limit
	burst             int
	maxKeys           int
	ttl               time.Duration
}

type rateLimitEntry struct {
	key      string
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newKeyedRateLimiter(requestsPerSecond float64, burst, maxKeys int, ttl time.Duration) *keyedRateLimiter {
	return &keyedRateLimiter{
		entries:           make(map[string]*list.Element),
		order:             list.New(),
		requestsPerSecond: rate.Limit(requestsPerSecond),
		burst:             burst,
		maxKeys:           maxKeys,
		ttl:               ttl,
	}
}

func (l *keyedRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if key == "" {
		key = "global"
	}

	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	for range maxRateLimitEvictions {
		element := l.order.Back()
		if element == nil {
			break
		}
		entry := element.Value.(*rateLimitEntry)
		if now.Sub(entry.lastSeen) <= l.ttl {
			break
		}
		l.remove(element)
	}

	if element := l.entries[key]; element != nil {
		entry := element.Value.(*rateLimitEntry)
		if now.Sub(entry.lastSeen) > l.ttl {
			entry.limiter = rate.NewLimiter(l.requestsPerSecond, l.burst)
		}
		entry.lastSeen = now
		l.order.MoveToFront(element)
		return entry.limiter.Allow(), nil
	}

	if l.order.Len() >= l.maxKeys {
		if oldest := l.order.Back(); oldest != nil {
			l.remove(oldest)
		}
	}
	entry := &rateLimitEntry{
		key:      key,
		limiter:  rate.NewLimiter(l.requestsPerSecond, l.burst),
		lastSeen: now,
	}
	element := l.order.PushFront(entry)
	l.entries[key] = element
	return entry.limiter.Allow(), nil
}

func (l *keyedRateLimiter) remove(element *list.Element) {
	entry := element.Value.(*rateLimitEntry)
	delete(l.entries, entry.key)
	l.order.Remove(element)
}

var _ action.RateLimiter = (*keyedRateLimiter)(nil)
