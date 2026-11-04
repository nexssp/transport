package thttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xctx"
)

func TestIdempotencyRequestQueryMismatchConflicts(t *testing.T) {
	first := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/items?view=compact", strings.NewReader(`{"id":1}`))
	second := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/items?view=full", strings.NewReader(`{"id":1}`))
	first.Header.Set("Content-Type", "application/json")
	second.Header.Set("Content-Type", "application/json")

	store := action.NewMemoryIdempotencyStore(time.Hour)
	calls := 0
	handler := withIdempotency(store, action.IdempotencyConfig{}, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte("created"))
	}, 0)
	first.Header.Set("Idempotency-Key", "same-key")
	second.Header.Set("Idempotency-Key", "same-key")

	firstRecorder := httptest.NewRecorder()
	handler(firstRecorder, first)
	if firstRecorder.Code != http.StatusOK || firstRecorder.Body.String() != "created" {
		t.Fatalf("first request = %d %q, want 200 created", firstRecorder.Code, firstRecorder.Body.String())
	}

	secondRecorder := httptest.NewRecorder()
	handler(secondRecorder, second)
	if secondRecorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("query mismatch status = %d, want %d", secondRecorder.Code, http.StatusUnprocessableEntity)
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1 after rejecting mismatched replay", calls)
	}
}

func TestIdempotencyKeyIsScopedToTenantAndUser(t *testing.T) {
	store := action.NewMemoryIdempotencyStore(time.Hour)
	calls := 0
	handler := withIdempotency(store, action.IdempotencyConfig{}, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(xctx.TenantIDFrom(r.Context()) + ":" + xctx.UserIDFrom(r.Context())))
	}, 0)

	request := func(tenant, user string) *http.Request {
		ctx := xctx.WithTenantID(t.Context(), tenant)
		ctx = xctx.WithUserID(ctx, user)
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/items", strings.NewReader(`{"id":1}`))
		r.Header.Set("Idempotency-Key", "shared-key")
		return r
	}
	serve := func(r *http.Request) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler(recorder, r)
		return recorder
	}

	first := serve(request("tenant-a", "user-1"))
	if first.Code != http.StatusOK || first.Body.String() != "tenant-a:user-1" {
		t.Fatalf("first response = %d %q", first.Code, first.Body.String())
	}

	otherCaller := serve(request("tenant-b", "user-1"))
	if otherCaller.Code != http.StatusOK || otherCaller.Body.String() != "tenant-b:user-1" {
		t.Fatalf("other caller response = %d %q, want independent execution", otherCaller.Code, otherCaller.Body.String())
	}
	otherUser := serve(request("tenant-a", "user-2"))
	if otherUser.Code != http.StatusOK || otherUser.Body.String() != "tenant-a:user-2" {
		t.Fatalf("other user response = %d %q, want independent execution", otherUser.Code, otherUser.Body.String())
	}

	replay := serve(request("tenant-a", "user-1"))
	if replay.Code != http.StatusOK || replay.Body.String() != "tenant-a:user-1" || replay.Header().Get("X-Idempotent-Replayed") != "true" {
		t.Fatalf("same-scope replay = %d %q, replay marker %q", replay.Code, replay.Body.String(), replay.Header().Get("X-Idempotent-Replayed"))
	}
	if calls != 3 {
		t.Fatalf("handler calls = %d, want one execution per caller scope", calls)
	}
}
