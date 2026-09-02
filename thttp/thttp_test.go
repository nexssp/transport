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

	act := action.New("user.update", func(ctx context.Context, req UserReq) (UserRes, error) {
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/usr_42?limit=50", strings.NewReader(`{"email":"test@nexss.com"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Role", "admin")

	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", w.Code, w.Body.String())
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
	server := thttp.New(":0", nil, nil)
	if server == nil {
		t.Fatal("expected non-nil server instance")
	}
}
