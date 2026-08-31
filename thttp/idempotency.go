package thttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/nexssp/kernel/action"
	"golang.org/x/sync/singleflight"
)

const (
	maxIdempotencyBodySize     = 10 << 20
	idempotencyFinalizeTimeout = 5 * time.Second
)

type capturedResponse struct {
	status int
	header http.Header
	body   []byte
	hash   string
}

func hashRequest(r *http.Request, cfg action.IdempotencyConfig, key string, body []byte) string {
	h := sha256.New()
	_, _ = io.WriteString(h, r.Method)
	_, _ = io.WriteString(h, r.URL.Path)
	_, _ = h.Write(body)
	if cfg.KeyFunc != nil {
		_, _ = io.WriteString(h, key)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func withIdempotency(store action.IdempotencyStore, cfg action.IdempotencyConfig, next http.HandlerFunc) http.HandlerFunc {
	if coordinator, ok := store.(action.IdempotencyCoordinator); ok {
		return withCoordinatedIdempotency(coordinator, cfg, next)
	}
	return withLocalIdempotency(store, cfg, next)
}

func withCoordinatedIdempotency(coordinator action.IdempotencyCoordinator, cfg action.IdempotencyConfig, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, key, reqHash, apply := idempotencyRequest(w, r, cfg)
		if !apply {
			return
		}
		if key == "" {
			next(w, r)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		claim, err := coordinator.Claim(r.Context(), key, reqHash, cfg.EffectiveLeaseTTL())
		if err != nil {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"idempotency coordination unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		switch claim.State {
		case action.IdempotencyClaimCompleted:
			writeIdempotencyReplay(w, claim.Entry, reqHash)
			return
		case action.IdempotencyClaimConflict:
			http.Error(w, `{"error":"idempotency key already used with a different request payload"}`, http.StatusUnprocessableEntity)
			return
		case action.IdempotencyClaimInProgress:
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"an identical request is still in progress"}`, http.StatusConflict)
			return
		case action.IdempotencyClaimAcquired:
		default:
			http.Error(w, `{"error":"invalid idempotency claim state"}`, http.StatusInternalServerError)
			return
		}

		captured := captureHTTPResponse(next, r, reqHash)
		if captured.status < http.StatusOK || captured.status >= http.StatusMultipleChoices {
			_ = coordinator.Release(r.Context(), key, claim.Token)
			writeCapturedResponse(w, captured, false)
			return
		}

		entry := capturedEntry(captured)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), idempotencyFinalizeTimeout)
		defer cancel()

		if err := coordinator.Complete(ctx, key, claim.Token, entry, cfg.TTL); err != nil {
			http.Error(w, `{"error":"idempotency outcome is indeterminate; query the operation before retrying"}`, http.StatusConflict)
			return
		}
		writeCapturedResponse(w, captured, false)
	}
}

func withLocalIdempotency(store action.IdempotencyStore, cfg action.IdempotencyConfig, next http.HandlerFunc) http.HandlerFunc {
	var sf singleflight.Group
	return func(w http.ResponseWriter, r *http.Request) {
		body, key, reqHash, apply := idempotencyRequest(w, r, cfg)
		if !apply {
			return
		}
		if key == "" {
			next(w, r)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		if entry, found := store.Get(r.Context(), key); found {
			writeIdempotencyReplay(w, entry, reqHash)
			return
		}

		v, err, shared := sf.Do(key, func() (any, error) { //nolint:contextcheck // singleflight.Do has no context-aware callback; request context is propagated via closure.
			captured := captureHTTPResponse(next, r, reqHash)

			// Store atomically inside the flight before releasing waiting callers
			if captured.status >= http.StatusOK && captured.status < http.StatusMultipleChoices {
				store.Set(context.WithoutCancel(r.Context()), key, capturedEntry(captured), cfg.TTL)
			}
			return captured, nil
		})
		if err != nil {
			http.Error(w, `{"error":"idempotency execution failed"}`, http.StatusInternalServerError)
			return
		}

		captured := v.(*capturedResponse)
		if captured.hash != reqHash {
			http.Error(w, `{"error":"idempotency key already used with a different request payload"}`, http.StatusUnprocessableEntity)
			return
		}
		writeCapturedResponse(w, captured, shared)
	}
}

func idempotencyRequest(w http.ResponseWriter, r *http.Request, cfg action.IdempotencyConfig) ([]byte, string, string, bool) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return nil, "", "", true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxIdempotencyBodySize+1))
	if err != nil {
		http.Error(w, `{"error":"unable to read idempotent request body"}`, http.StatusBadRequest)
		return nil, "", "", false
	}
	if len(body) > maxIdempotencyBodySize {
		http.Error(w, `{"error":"idempotent request body exceeds 10 MiB"}`, http.StatusRequestEntityTooLarge)
		return nil, "", "", false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	key := r.Header.Get(cfg.Header())
	if cfg.KeyFunc != nil {
		if derived := cfg.KeyFunc(body); derived != "" {
			key = derived
		}
	}
	if key == "" {
		return body, "", "", true
	}
	return body, key, hashRequest(r, cfg, key, body), true
}

func captureHTTPResponse(next http.HandlerFunc, r *http.Request, reqHash string) *capturedResponse {
	rec := httptest.NewRecorder()
	next(rec, r)
	return &capturedResponse{status: rec.Code, header: rec.Header(), body: rec.Body.Bytes(), hash: reqHash}
}

func capturedEntry(captured *capturedResponse) action.IdempotencyEntry {
	headers := make(map[string]string, 2)
	for _, h := range []string{"Content-Type", "X-Request-ID"} {
		if v := captured.header.Get(h); v != "" {
			headers[h] = v
		}
	}
	return action.IdempotencyEntry{Status: captured.status, Body: captured.body, Headers: headers, StoredAt: time.Now().UTC(), RequestHash: captured.hash}
}

func writeIdempotencyReplay(w http.ResponseWriter, entry action.IdempotencyEntry, reqHash string) {
	if entry.RequestHash != "" && entry.RequestHash != reqHash {
		http.Error(w, `{"error":"idempotency key already used with a different request payload"}`, http.StatusUnprocessableEntity)
		return
	}
	for k, v := range entry.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("X-Idempotent-Replayed", "true")
	w.WriteHeader(entry.Status)
	_, _ = w.Write(entry.Body)
}

func writeCapturedResponse(w http.ResponseWriter, captured *capturedResponse, replayed bool) {
	for k, vs := range captured.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if replayed {
		w.Header().Set("X-Idempotent-Replayed", "true")
	}
	w.WriteHeader(captured.status)
	_, _ = w.Write(captured.body)
}
