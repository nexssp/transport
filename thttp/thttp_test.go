package thttp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transport/thttp"
	httpmiddleware "github.com/nexssp/transport/thttp/middleware"
)

type UserReq struct {
	ID    string `path:"id"`
	Limit int    `query:"limit"`
	Role  string `header:"X-User-Role"`
	Email string `json:"email"`
}

type UserRes struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
	Email  string `json:"email"`
	Limit  int    `json:"limit"`
}

func TestTHTTP_AutomaticTagBinding(t *testing.T) {
	t.Parallel()

	act := action.New("user.update", func(_ context.Context, req UserReq) (UserRes, error) {
		return UserRes{
			UserID: req.ID,
			Limit:  req.Limit,
			Role:   req.Role,
			Email:  req.Email,
		}, nil
	}).Route(thttp.POST("/api/v1/users/{id}")).Build()

	server := thttp.New(":0")
	server.Mount([]action.AnyAction{act})

	// Test: Path {id} + Query ?limit=50 + Header X-User-Role + JSON Body {"email": "..."}
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/api/v1/users/usr_42?limit=50",
		strings.NewReader(`{"email":"test@nexss.com"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Role", "admin")

	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var res UserRes
	_ = json.Unmarshal(w.Body.Bytes(), &res)

	if res.UserID != "usr_42" {
		t.Errorf("expected UserID 'usr_42' from path tag, got %q", res.UserID)
	}
	if res.Limit != 50 {
		t.Errorf("expected Limit 50 from query tag, got %d", res.Limit)
	}
	if res.Role != "admin" {
		t.Errorf("expected Role 'admin' from header tag, got %q", res.Role)
	}
	if res.Email != "test@nexss.com" {
		t.Errorf("expected Email from body, got %q", res.Email)
	}
}

func TestTHTTP_NilOptionSafety(t *testing.T) {
	t.Parallel()

	// Should not panic when nil option is passed
	server := thttp.New(":0", nil)
	if server == nil {
		t.Fatal("expected non-nil server instance")
	}
}

func TestTHTTP_MapInputReceivesQueryAndPathParameters(t *testing.T) {
	t.Parallel()

	act := action.New("tasks.get", func(_ context.Context, req any) (any, error) {
		return req, nil
	}).Route(thttp.GET("/api/tasks/{task_id}")).Build()
	server := thttp.New(":0")
	server.Mount([]action.AnyAction{act})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/tasks/tsk_42?page=2&search=demo", http.NoBody)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET returned %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["task_id"] != "tsk_42" || got["page"] != "2" || got["search"] != "demo" {
		t.Fatalf("request map = %#v, want path and query strings", got)
	}
}

func TestSecurityHeadersSetsAPIAndBrowserHeaders(t *testing.T) {
	t.Parallel()
	server := thttp.New(":0", nil).Use(httpmiddleware.SecurityHeaders, httpmiddleware.NoStoreAPIResponses)
	server.Mux().HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/health", http.NoBody))
	for header, want := range map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Referrer-Policy":            "strict-origin-when-cross-origin",
		"Cross-Origin-Opener-Policy": "same-origin",
		"Cache-Control":              "no-store",
	} {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if got := w.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("unexpected application-owned Content-Security-Policy: %q", got)
	}
	if got := w.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS should not be set for plain HTTP: %q", got)
	}
}
