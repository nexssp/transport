package thttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticResponseServesEmbeddedFilesAndSPA(t *testing.T) {
	files := map[string][]byte{
		"index.html":     []byte("<main>app</main>"),
		"assets/app.js":  []byte("console.log('app')"),
		"assets/app.css": []byte("main { color: navy }"),
	}
	for _, test := range []struct {
		name         string
		path         string
		wantStatus   int
		wantBody     string
		wantType     string
		wantFallback bool
	}{
		{name: "index", path: "/", wantStatus: http.StatusOK, wantBody: "<main>app</main>", wantType: "text/html", wantFallback: true},
		{name: "asset", path: "/assets/app.js", wantStatus: http.StatusOK, wantBody: "console.log('app')", wantType: "javascript", wantFallback: true},
		{name: "spa route", path: "/chat/session/1", wantStatus: http.StatusOK, wantBody: "<main>app</main>", wantType: "text/html", wantFallback: true},
		{name: "missing asset", path: "/assets/missing.js", wantStatus: http.StatusNotFound, wantBody: "404 page not found", wantFallback: true},
		{name: "traversal", path: "/../secret.txt", wantStatus: http.StatusNotFound, wantBody: "404 page not found", wantFallback: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:8080"+test.path, http.NoBody)
			response, err := staticResponse(StaticRequest{
				Files:                 files,
				Index:                 "index.html",
				SPA:                   test.wantFallback,
				ContentSecurityPolicy: "default-src 'self'",
			}, request)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.wantStatus)
			}
			if !strings.Contains(response.Header.Get("Content-Type"), test.wantType) {
				t.Fatalf("Content-Type = %q, want %q", response.Header.Get("Content-Type"), test.wantType)
			}
			if !strings.Contains(string(response.Body), test.wantBody) {
				t.Fatalf("body = %q, want substring %q", response.Body, test.wantBody)
			}
			if response.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q, want nosniff", response.Header.Get("X-Content-Type-Options"))
			}
			if response.StatusCode == http.StatusOK && response.Header.Get("Content-Security-Policy") != "default-src 'self'" {
				t.Fatalf("CSP = %q", response.Header.Get("Content-Security-Policy"))
			}
		})
	}
}

func TestStaticResponseSupportsBasePathAndHead(t *testing.T) {
	files := map[string][]byte{"index.html": []byte("<main>base</main>")}
	input := StaticRequest{Files: files, Index: "index.html", BasePath: "/app", SPA: true}

	request := httptest.NewRequestWithContext(t.Context(), http.MethodHead, "http://127.0.0.1:8080/app/", http.NoBody)
	response, err := staticResponse(input, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(response.Body) != 0 {
		t.Fatalf("HEAD response = %#v, want 200 with empty body", response)
	}
	if got := response.Header.Get("Content-Length"); got != "17" {
		t.Fatalf("HEAD Content-Length = %q, want 17", got)
	}

	request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:8080/outside/", http.NoBody)
	response, err = staticResponse(input, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("out-of-base-path status = %d, want 404", response.StatusCode)
	}
}

func TestStaticResponseRejectsUnsupportedMethodAndInvalidIndex(t *testing.T) {
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:8080/", http.NoBody)
	response, err := staticResponse(StaticRequest{Files: map[string][]byte{"index.html": []byte("ok")}}, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusMethodNotAllowed || response.Header.Get("Allow") != "GET, HEAD" {
		t.Fatalf("method response = %#v, want 405 and Allow", response)
	}

	request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:8080/", http.NoBody)
	if _, err := staticResponse(StaticRequest{Files: map[string][]byte{}, Index: "../secret"}, request); err == nil {
		t.Fatal("invalid index path unexpectedly succeeded")
	}
}
