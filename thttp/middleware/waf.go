package middleware

import (
	"bytes"
	"io"
	"net/http"
	"sync"

	"github.com/nexssp/kernel/xctx"
)

// WAFConfig controls the Web Application Firewall.
type WAFConfig struct {
	MaxBodySize int64
	BlockedIPs  map[string]struct{}
	EnableSQLi  bool
	EnableXSS   bool
}

var bufferPool = sync.Pool{
	New: func() any { return bytes.NewBuffer(make([]byte, 0, 4096)) },
}

var (
	xssThreats = [][]byte{
		[]byte("<script"),
		[]byte("javascript:"),
		[]byte("onerror="),
		[]byte("onload="),
		[]byte("<iframe"),
		[]byte("<svg/onload"),
	}
	sqliThreats = [][]byte{
		[]byte("union select"),
		[]byte("union all select"),
		[]byte("drop table"),
		[]byte("insert into"),
		[]byte("delete from"),
		[]byte("; --"),
		[]byte("' or '1'='1"),
	}
)

// WAF returns an HTTP middleware that enforces the configured policy.
func WAF(cfg WAFConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ipBlocked(r, cfg.BlockedIPs) {
				http.Error(w, `{"error":"Forbidden"}`, http.StatusForbidden)
				return
			}

			limit := cfg.MaxBodySize
			if limit <= 0 {
				limit = 10 << 20
			}

			if r.ContentLength > limit {
				http.Error(w, `{"error":"Payload Too Large"}`, http.StatusRequestEntityTooLarge)
				return
			}

			if !cfg.EnableSQLi && !cfg.EnableXSS {
				next.ServeHTTP(w, r)
				return
			}
			if r.Body == nil || r.ContentLength == 0 {
				next.ServeHTTP(w, r)
				return
			}

			data, ok := readBoundedBody(w, r, limit)
			if !ok {
				return
			}
			if data == nil {
				next.ServeHTTP(w, r)
				return
			}

			if status, reason := inspectThreats(data, cfg); status != 0 {
				http.Error(w, reason, status)
				return
			}

			r.Body = io.NopCloser(bytes.NewReader(data))
			next.ServeHTTP(w, r)
		})
	}
}

func ipBlocked(r *http.Request, blocked map[string]struct{}) bool {
	if len(blocked) == 0 {
		return false
	}
	_, found := blocked[xctx.ClientIPFrom(r.Context())]
	return found
}

// readBoundedBody reads up to limit+1 bytes into a pooled buffer.
func readBoundedBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	buf, ok := bufferPool.Get().(*bytes.Buffer)
	if !ok || buf == nil {
		buf = bytes.NewBuffer(make([]byte, 0, 4096))
	}
	buf.Reset()

	if buf.Cap() <= 64*1024 {
		defer bufferPool.Put(buf)
	}

	if _, err := io.Copy(buf, io.LimitReader(r.Body, limit+1)); err != nil {
		_ = r.Body.Close()
		http.Error(w, `{"error":"Bad Request"}`, http.StatusBadRequest)
		return nil, false
	}
	_ = r.Body.Close()

	if int64(buf.Len()) > limit {
		http.Error(w, `{"error":"Payload Too Large"}`, http.StatusRequestEntityTooLarge)
		return nil, false
	}

	if buf.Len() == 0 {
		return nil, true
	}
	return buf.Bytes(), true
}

// inspectThreats scans data against the enabled signature sets. It
// returns status==0 when no threat is found, or an HTTP status and
// message when the request must be rejected.
func inspectThreats(data []byte, cfg WAFConfig) (status int, message string) {
	if cfg.EnableXSS {
		for _, threat := range xssThreats {
			if containsFold(data, threat) {
				return http.StatusForbidden, `{"error":"WAF: XSS Detected"}`
			}
		}
	}
	if cfg.EnableSQLi {
		for _, threat := range sqliThreats {
			if containsFold(data, threat) {
				return http.StatusForbidden, `{"error":"WAF: SQLi Detected"}`
			}
		}
	}
	return 0, ""
}

// containsFold reports whether haystack contains needle, comparing
// ASCII case-insensitively. needle must already be lowercase.
func containsFold(haystack, needle []byte) bool {
	n := len(needle)
	if n == 0 || len(haystack) < n {
		return false
	}
	last := len(haystack) - n

outer:
	for i := 0; i <= last; i++ {
		for j := range n {
			b := haystack[i+j]
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if b != needle[j] {
				continue outer
			}
		}
		return true
	}
	return false
}
