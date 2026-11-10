package thttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	actionpkg "github.com/nexssp/kernel/action"

	"github.com/nexssp/transport/thttp"
)

type typedResponseBody struct {
	Value string `json:"value"`
}

type legacyResponseBody struct {
	OK bool `json:"ok"`
}

type formPostRequest struct {
	Token string `json:"token" form:"token"`
}

type multipartPostRequest struct {
	Upload thttp.FormFile `form:"upload"`
}

type appleCallbackRequest struct {
	Code  string `form:"code"`
	State string `form:"state"`
}

func serveTypedAction(t *testing.T, act actionpkg.AnyAction, method, target, body, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	return serveTypedRequest(t, act, request)
}

func serveTypedRequest(t *testing.T, act actionpkg.AnyAction, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	server := thttp.New("")
	server.Mount([]actionpkg.AnyAction{act})
	writer := httptest.NewRecorder()
	server.Handler().ServeHTTP(writer, request)
	return writer
}

func recordTypedResponse[T any](t *testing.T, response thttp.Response[T]) *httptest.ResponseRecorder {
	t.Helper()
	writer := httptest.NewRecorder()
	if err := response.WriteResponseTo(writer); err != nil {
		t.Fatal(err)
	}
	return writer
}

func TestTypedResponseSetCookie(t *testing.T) {
	act := actionpkg.New("typed.cookies", func(context.Context, struct{}) (thttp.Response[any], error) {
		return thttp.Response[any]{SetCookies: []*http.Cookie{
			{Name: "session", Value: "session-value", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode},
			{Name: "csrf", Value: "csrf-value", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode},
		}}, nil
	}).Route(thttp.POST("/typed/cookies")).Build()

	response := serveTypedAction(t, act, http.MethodPost, "/typed/cookies", "", "")
	cookies := response.Header().Values("Set-Cookie")
	if response.Code != http.StatusOK || len(cookies) != 2 {
		t.Fatalf("status=%d Set-Cookie=%q, want 200 and two cookies", response.Code, cookies)
	}
}

func TestTypedResponseZeroValueBodyIsAbsent(t *testing.T) {
	tests := []struct {
		name  string
		write func(http.ResponseWriter) error
	}{
		{"int", func(writer http.ResponseWriter) error { return thttp.Response[int]{Body: 0}.WriteResponseTo(writer) }},
		{"bool", func(writer http.ResponseWriter) error {
			return thttp.Response[bool]{Body: false}.WriteResponseTo(writer)
		}},
		{"string", func(writer http.ResponseWriter) error {
			return thttp.Response[string]{Body: ""}.WriteResponseTo(writer)
		}},
		{"struct", func(writer http.ResponseWriter) error {
			return thttp.Response[struct{ Value int }]{Body: struct{ Value int }{}}.WriteResponseTo(writer)
		}},
		{"empty-struct", func(writer http.ResponseWriter) error {
			return thttp.Response[struct{}]{Body: struct{}{}}.WriteResponseTo(writer)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := httptest.NewRecorder()
			if err := test.write(writer); err != nil {
				t.Fatal(err)
			}
			if writer.Body.Len() != 0 || writer.Header().Get("Content-Type") != "" || writer.Header().Get("Content-Length") != "" {
				t.Fatalf("body=%q headers=%v, want no body or body headers", writer.Body.String(), writer.Header())
			}
		})
	}
}

func TestTypedResponsePointerToZeroValueIsJSON(t *testing.T) {
	zero := 0
	writer := recordTypedResponse(t, thttp.Response[*int]{Body: &zero})
	if writer.Body.String() != "0" || writer.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("body=%q Content-Type=%q, want JSON zero", writer.Body.String(), writer.Header().Get("Content-Type"))
	}
}

func TestTypedResponseIsEmptyBodyMatchesZeroValueSemantics(t *testing.T) {
	if !(thttp.Response[int]{Body: 0}).IsEmptyBody(http.MethodPost) {
		t.Fatal("zero int body should be reported absent")
	}
	zero := 0
	if (thttp.Response[*int]{Body: &zero}).IsEmptyBody(http.MethodPost) {
		t.Fatal("pointer to zero should be reported present")
	}
}

func TestTypedResponseNonNilEmptyMapIsJSON(t *testing.T) {
	writer := recordTypedResponse(t, thttp.Response[map[string]string]{Body: map[string]string{}})
	if writer.Body.String() != "{}" || writer.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("body=%q Content-Type=%q, want empty JSON object", writer.Body.String(), writer.Header().Get("Content-Type"))
	}
}

func TestTypedResponseRedirect(t *testing.T) {
	act := actionpkg.New("typed.redirect", func(context.Context, struct{}) (thttp.Response[typedResponseBody], error) {
		return thttp.Response[typedResponseBody]{
			Status:  http.StatusSeeOther,
			Headers: http.Header{"Location": {"https://example.test/callback"}},
		}, nil
	}).Route(thttp.POST("/typed/redirect")).Build()

	response := serveTypedAction(t, act, http.MethodPost, "/typed/redirect", "", "")
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "https://example.test/callback" {
		t.Fatalf("response status=%d Location=%q", response.Code, response.Header().Get("Location"))
	}
	if response.Body.Len() != 0 {
		t.Fatalf("redirect body=%q, want empty", response.Body.String())
	}
}

func TestTypedResponseCustomHeaders(t *testing.T) {
	act := actionpkg.New("typed.headers", func(context.Context, struct{}) (thttp.Response[typedResponseBody], error) {
		return thttp.Response[typedResponseBody]{
			Headers: http.Header{"X-Custom": {"val"}},
			Body:    typedResponseBody{Value: "ok"},
		}, nil
	}).Route(thttp.POST("/typed/headers")).Build()

	response := serveTypedAction(t, act, http.MethodPost, "/typed/headers", "", "")
	if response.Code != http.StatusOK || response.Header().Get("X-Custom") != "val" {
		t.Fatalf("status=%d X-Custom=%q", response.Code, response.Header().Get("X-Custom"))
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type=%q, want application/json", response.Header().Get("Content-Type"))
	}
}

func TestTypedResponse204(t *testing.T) {
	action := actionpkg.New("typed.no_content", func(context.Context, struct{}) (thttp.Response[map[string]string], error) {
		return thttp.Response[map[string]string]{Status: http.StatusNoContent, Body: map[string]string{"ignored": "body"}}, nil
	}).Route(thttp.POST("/typed/no-content")).Build()

	response := serveTypedAction(t, action, http.MethodPost, "/typed/no-content", "", "")
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "" || response.Header().Get("Content-Length") != "" {
		t.Fatalf("body headers on 204: %v", response.Header())
	}
}

func TestTypedResponseHeadNoBody(t *testing.T) {
	action := actionpkg.New("typed.head", func(context.Context, struct{}) (thttp.Response[typedResponseBody], error) {
		return thttp.Response[typedResponseBody]{Body: typedResponseBody{Value: "hidden"}}, nil
	}).Route(thttp.HEAD("/typed/head")).Build()

	response := serveTypedAction(t, action, http.MethodHead, "/typed/head", "", "")
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q, want representation type application/json", got)
	}
	if got := response.Header().Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length=%q, want absent", got)
	}
}

func TestTypedResponseBackwardCompat(t *testing.T) {
	action := actionpkg.New("legacy.response", func(context.Context, struct{}) (legacyResponseBody, error) {
		return legacyResponseBody{OK: true}, nil
	}).Route(thttp.POST("/typed/legacy")).Build()

	response := serveTypedAction(t, action, http.MethodPost, "/typed/legacy", "", "")
	if response.Code != http.StatusOK || response.Body.String() != "{\"ok\":true}\n" {
		t.Fatalf("legacy response status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestTypedResponseBackwardCompatPointerForm(t *testing.T) {
	action := actionpkg.New("legacy.pointer", func(context.Context, struct{}) (*legacyResponseBody, error) {
		return &legacyResponseBody{OK: true}, nil
	}).Route(thttp.POST("/typed/legacy-pointer")).Build()

	response := serveTypedAction(t, action, http.MethodPost, "/typed/legacy-pointer", "", "")
	if response.Code != http.StatusOK || response.Body.String() != "{\"ok\":true}\n" {
		t.Fatalf("legacy pointer response status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestTypedResponsePointerForm(t *testing.T) {
	action := actionpkg.New("typed.pointer", func(context.Context, struct{}) (*thttp.Response[typedResponseBody], error) {
		return &thttp.Response[typedResponseBody]{Body: typedResponseBody{Value: "pointer"}}, nil
	}).Route(thttp.POST("/typed/pointer")).Build()

	response := serveTypedAction(t, action, http.MethodPost, "/typed/pointer", "", "")
	if response.Code != http.StatusOK || response.Body.String() != `{"value":"pointer"}` {
		t.Fatalf("typed pointer response status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestTypedResponseStatusFallback(t *testing.T) {
	action := actionpkg.New("typed.status_fallback", func(context.Context, struct{}) (thttp.Response[typedResponseBody], error) {
		return thttp.Response[typedResponseBody]{Body: typedResponseBody{Value: "created"}}, nil
	}).SuccessStatus(http.StatusCreated).Route(thttp.POST("/typed/status-fallback")).Build()

	response := serveTypedAction(t, action, http.MethodPost, "/typed/status-fallback", "", "")
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d, want 201", response.Code)
	}
}

func TestTypedRouteFormPost(t *testing.T) {
	action := actionpkg.New("typed.form", func(_ context.Context, request formPostRequest) (typedResponseBody, error) {
		return typedResponseBody{Value: request.Token}, nil
	}).Route(thttp.POST("/typed/form")).Build()

	response := serveTypedAction(t, action, http.MethodPost, "/typed/form", "token=form-token", "application/x-www-form-urlencoded")
	var body typedResponseBody
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Value != "form-token" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestTypedRouteFormValuesNotMergedIntoMap(t *testing.T) {
	action := actionpkg.New("typed.form_map", func(_ context.Context, request any) (any, error) {
		return request, nil
	}).Route(thttp.POST("/typed/form-map")).Build()
	response := serveTypedAction(t, action, http.MethodPost, "/typed/form-map?q=query", "token=form", "application/x-www-form-urlencoded")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode map response: %v", err)
	}
	if len(got) != 1 || got["q"] != "query" {
		t.Fatalf("request map = %#v, want only query parameter q=query (form token is not merged)", got)
	}
}

func TestTypedRouteFormQueryConflictFollowsNetHTTPPrecedence(t *testing.T) {
	action := actionpkg.New("typed.form_conflict", func(_ context.Context, request formPostRequest) (typedResponseBody, error) {
		return typedResponseBody{Value: request.Token}, nil
	}).Route(thttp.POST("/typed/form-conflict")).Build()
	response := serveTypedAction(t, action, http.MethodPost, "/typed/form-conflict?token=from-query", "token=from-body", "application/x-www-form-urlencoded")
	var body typedResponseBody
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Value != "from-body" {
		t.Fatalf("status=%d body=%q, want URL-encoded POST body value to win per net/http", response.Code, response.Body.String())
	}
}

func TestTypedRouteMultipartQueryConflictFollowsNetHTTPPrecedence(t *testing.T) {
	var requestBody bytes.Buffer
	form := multipart.NewWriter(&requestBody)
	if err := form.WriteField("token", "from-multipart"); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	action := actionpkg.New("typed.multipart_conflict", func(_ context.Context, request formPostRequest) (typedResponseBody, error) {
		return typedResponseBody{Value: request.Token}, nil
	}).Route(thttp.POST("/typed/multipart-conflict")).Build()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/typed/multipart-conflict?token=from-query", &requestBody)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := serveTypedRequest(t, action, request)
	var body typedResponseBody
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Value != "from-query" {
		t.Fatalf("status=%d body=%q, want query value to win over multipart field per net/http", response.Code, response.Body.String())
	}
}

func TestTypedRouteMultipart(t *testing.T) {
	var requestBody bytes.Buffer
	form := multipart.NewWriter(&requestBody)
	file, err := form.CreateFormFile("upload", "proof.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("multipart-proof")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	action := actionpkg.New("typed.multipart", func(_ context.Context, request multipartPostRequest) (typedResponseBody, error) {
		return typedResponseBody{Value: request.Upload.Filename + ":" + string(request.Upload.Bytes)}, nil
	}).Route(thttp.POST("/typed/multipart")).Build()

	response := serveTypedAction(t, action, http.MethodPost, "/typed/multipart", requestBody.String(), form.FormDataContentType())
	var body typedResponseBody
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Value != "proof.txt:multipart-proof" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestTypedRoute415(t *testing.T) {
	action := actionpkg.New("typed.unsupported", func(context.Context, formPostRequest) (legacyResponseBody, error) {
		return legacyResponseBody{OK: true}, nil
	}).Route(thttp.POST("/typed/unsupported")).Build()

	response := serveTypedAction(t, action, http.MethodPost, "/typed/unsupported", "<data/>", "application/xml")
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status=%d body=%q, want 415", response.Code, response.Body.String())
	}
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 415 body: %v", err)
	}
	if body.Error != "UnsupportedMediaType" || !strings.Contains(body.Message, "application/x-www-form-urlencoded") {
		t.Fatalf("415 error=%q message=%q, want UnsupportedMediaType and supported types", body.Error, body.Message)
	}
}

func TestTypedRoute415PreservesRequestID(t *testing.T) {
	action := actionpkg.New("typed.unsupported_request_id", func(context.Context, formPostRequest) (legacyResponseBody, error) {
		return legacyResponseBody{OK: true}, nil
	}).Route(thttp.POST("/typed/unsupported-request-id")).Build()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/typed/unsupported-request-id", strings.NewReader("<data/>"))
	request.Header.Set("Content-Type", "application/xml")
	request.Header.Set("X-Request-ID", "unsupported-media-request")
	response := serveTypedRequest(t, action, request)
	if response.Code != http.StatusUnsupportedMediaType || response.Header().Get("X-Request-ID") != "unsupported-media-request" {
		t.Fatalf("status=%d X-Request-ID=%q, want 415 with request ID", response.Code, response.Header().Get("X-Request-ID"))
	}
	var body struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.RequestID != "unsupported-media-request" {
		t.Fatalf("response request_id=%q decode error=%v, want request ID in error body", body.RequestID, err)
	}
}

func TestTypedRouteRejectsMalformedAndNearMatchContentTypes(t *testing.T) {
	for _, contentType := range []string{
		"application/jsonp",
		`application/json; charset="unterminated`,
		"multipart/form-data",
	} {
		t.Run(contentType, func(t *testing.T) {
			act := actionpkg.New("typed.bad_content_type", func(context.Context, formPostRequest) (legacyResponseBody, error) {
				return legacyResponseBody{OK: true}, nil
			}).Route(thttp.POST("/typed/bad-content-type")).Build()
			response := serveTypedAction(t, act, http.MethodPost, "/typed/bad-content-type", `{"token":"value"}`, contentType)
			if response.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("Content-Type=%q status=%d body=%q, want 415", contentType, response.Code, response.Body.String())
			}
		})
	}
}

func TestTypedRouteEmptyContentType(t *testing.T) {
	action := actionpkg.New("typed.no_content_type", func(_ context.Context, request formPostRequest) (typedResponseBody, error) {
		return typedResponseBody{Value: request.Token}, nil
	}).Route(thttp.POST("/typed/no-content-type")).Build()

	response := serveTypedAction(t, action, http.MethodPost, "/typed/no-content-type", `{"token":"json-without-content-type"}`, "")
	var body typedResponseBody
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Value != "json-without-content-type" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestAppleFormPostEndToEnd(t *testing.T) {
	action := actionpkg.New("typed.apple_callback", func(_ context.Context, request appleCallbackRequest) (typedResponseBody, error) {
		return typedResponseBody{Value: request.Code + ":" + request.State}, nil
	}).Route(thttp.POST("/typed/apple/callback")).Build()

	form := url.Values{"code": {"authorization-code"}, "state": {"csrf-state"}}
	response := serveTypedAction(t, action, http.MethodPost, "/typed/apple/callback", form.Encode(), "application/x-www-form-urlencoded")
	var body typedResponseBody
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Value != "authorization-code:csrf-state" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestTypedRouteJSONContentTypeWithCharset(t *testing.T) {
	act := actionpkg.New("typed.json_charset", func(_ context.Context, request formPostRequest) (typedResponseBody, error) {
		return typedResponseBody{Value: request.Token}, nil
	}).Route(thttp.POST("/typed/json-charset")).Build()

	response := serveTypedAction(t, act, http.MethodPost, "/typed/json-charset", `{"token":"charset-json"}`, "application/json; charset=utf-8")
	var body typedResponseBody
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Value != "charset-json" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestTypedRouteEmptyBodyIgnoresUnsupportedContentType(t *testing.T) {
	act := actionpkg.New("typed.empty_unknown", func(context.Context, struct{}) (legacyResponseBody, error) {
		return legacyResponseBody{OK: true}, nil
	}).Route(thttp.POST("/typed/empty-unknown")).Build()
	response := serveTypedAction(t, act, http.MethodPost, "/typed/empty-unknown", "", "application/xml")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want empty body to skip media-type validation", response.Code, response.Body.String())
	}
}

func TestTypedRouteEmptyGetBodyIgnoresUnsupportedContentType(t *testing.T) {
	action := actionpkg.New("typed.empty_get_unknown", func(context.Context, struct{}) (legacyResponseBody, error) {
		return legacyResponseBody{OK: true}, nil
	}).Route(thttp.GET("/typed/empty-get-unknown")).Build()
	response := serveTypedAction(t, action, http.MethodGet, "/typed/empty-get-unknown", "", "application/xml")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want empty GET to skip media-type validation", response.Code, response.Body.String())
	}
}

func TestTypedRouteEmptyChunkedBodyIgnoresUnsupportedContentType(t *testing.T) {
	act := actionpkg.New("typed.empty_chunked", func(context.Context, struct{}) (legacyResponseBody, error) {
		return legacyResponseBody{OK: true}, nil
	}).Route(thttp.POST("/typed/empty-chunked")).Build()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/typed/empty-chunked", io.NopCloser(strings.NewReader("")))
	request.ContentLength = -1
	request.TransferEncoding = []string{"chunked"}
	request.Header.Set("Content-Type", "application/xml")
	response := serveTypedRequest(t, act, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want empty chunked body to skip media-type validation", response.Code, response.Body.String())
	}
}

func TestTypedRouteSkipsBodyDecodingForGetHeadAndOptions(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			path := "/typed/skip-" + strings.ToLower(method)
			act := actionpkg.New("typed.skip_body", func(context.Context, struct{}) (legacyResponseBody, error) {
				return legacyResponseBody{OK: true}, nil
			}).Route(thttp.HTTPRoute{Method: method, Path: path}).Build()
			response := serveTypedAction(t, act, method, path, "unsupported body", "application/xml")
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%q, want %s request to skip body decoding", response.Code, response.Body.String(), method)
			}
		})
	}
}

func Test405AllowSingleMethod(t *testing.T) {
	action := actionpkg.New("typed.only_post", func(context.Context, struct{}) (legacyResponseBody, error) {
		return legacyResponseBody{OK: true}, nil
	}).Route(thttp.POST("/auth/login")).Build()
	response := serveTypedAction(t, action, http.MethodGet, "/auth/login", "", "")
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "POST" {
		t.Fatalf("status=%d Allow=%q", response.Code, response.Header().Get("Allow"))
	}
}

func Test405AllowMultipleMethods(t *testing.T) {
	getAction := actionpkg.New("typed.get", func(context.Context, struct{}) (legacyResponseBody, error) {
		return legacyResponseBody{OK: true}, nil
	}).Route(thttp.GET("/shared")).Build()
	postAction := actionpkg.New("typed.post", func(context.Context, struct{}) (legacyResponseBody, error) {
		return legacyResponseBody{OK: true}, nil
	}).Route(thttp.POST("/shared")).Build()
	server := thttp.New("")
	server.Mount([]actionpkg.AnyAction{getAction, postAction})
	writer := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/shared", http.NoBody)
	request.Header.Set("X-Request-ID", "method-not-allowed-request")
	server.Handler().ServeHTTP(writer, request)
	if writer.Code != http.StatusMethodNotAllowed || writer.Header().Get("Allow") != "GET, HEAD, POST" {
		t.Fatalf("status=%d Allow=%q", writer.Code, writer.Header().Get("Allow"))
	}
	if got := writer.Header().Get("X-Request-ID"); got != "method-not-allowed-request" {
		t.Fatalf("X-Request-ID=%q, want request correlation ID", got)
	}
}

func Test405AllowIncludesHeadForGetFallback(t *testing.T) {
	action := actionpkg.New("typed.get_fallback", func(context.Context, struct{}) (thttp.Response[typedResponseBody], error) {
		return thttp.Response[typedResponseBody]{Body: typedResponseBody{Value: "get representation"}}, nil
	}).Route(thttp.GET("/typed/get-fallback")).Build()

	headResponse := serveTypedAction(t, action, http.MethodHead, "/typed/get-fallback", "", "")
	if headResponse.Code != http.StatusOK || headResponse.Body.Len() != 0 || headResponse.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("HEAD status=%d body=%q Content-Type=%q, want 200, no body, and application/json", headResponse.Code, headResponse.Body.String(), headResponse.Header().Get("Content-Type"))
	}
	putResponse := serveTypedAction(t, action, http.MethodPut, "/typed/get-fallback", "", "")
	if putResponse.Code != http.StatusMethodNotAllowed || putResponse.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("PUT status=%d Allow=%q, want 405 and GET, HEAD", putResponse.Code, putResponse.Header().Get("Allow"))
	}
}

func Test404NotFound(t *testing.T) {
	action := actionpkg.New("typed.known", func(context.Context, struct{}) (legacyResponseBody, error) {
		return legacyResponseBody{OK: true}, nil
	}).Route(thttp.POST("/known")).Build()
	response := serveTypedAction(t, action, http.MethodGet, "/missing", "", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q, want 404", response.Code, response.Body.String())
	}
}

func TestOptionsUnregistered(t *testing.T) {
	action := actionpkg.New("typed.options", func(context.Context, struct{}) (legacyResponseBody, error) {
		return legacyResponseBody{OK: true}, nil
	}).Route(thttp.POST("/options")).Build()
	response := serveTypedAction(t, action, http.MethodOptions, "/options", "", "")
	if response.Code != http.StatusNoContent || response.Header().Get("Allow") != "POST" || response.Body.Len() != 0 {
		t.Fatalf("status=%d Allow=%q body=%q", response.Code, response.Header().Get("Allow"), response.Body.String())
	}
}

func TestTypedResponseIdempotencyReplay(t *testing.T) {
	var calls atomic.Int32
	act := actionpkg.New("typed.idempotent", func(context.Context, struct{}) (thttp.Response[typedResponseBody], error) {
		calls.Add(1)
		return thttp.Response[typedResponseBody]{Status: http.StatusCreated, Body: typedResponseBody{Value: "once"}}, nil
	}).Idempotent().Route(thttp.POST("/typed/idempotent")).Build()
	server := thttp.New("")
	server.Mount([]actionpkg.AnyAction{act})

	for i := range 2 {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/typed/idempotent", strings.NewReader(""))
		request.Header.Set("Idempotency-Key", "typed-response-key")
		writer := httptest.NewRecorder()
		server.Handler().ServeHTTP(writer, request)
		if writer.Code != http.StatusCreated || writer.Body.String() != `{"value":"once"}` {
			t.Fatalf("attempt %d status=%d body=%q", i+1, writer.Code, writer.Body.String())
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("action executed %d times, want once", got)
	}
}

func TestMountRejectsIdempotentOnRawRoute(t *testing.T) {
	act := actionpkg.New("typed.raw_idempotent", func(context.Context, struct{}) (thttp.RawHTTPResponse, error) {
		return thttp.NewRawHTTPResponse(http.StatusNoContent, nil, nil), nil
	}).Idempotent().Route(thttp.RawRoute(http.MethodPost, "/raw-idempotent")).Build()

	defer func() {
		recovered := recover()
		if recovered == nil || !strings.Contains(fmt.Sprint(recovered), ":idempotent with :raw_route") {
			t.Fatalf("panic=%v, want clear :idempotent/:raw_route message", recovered)
		}
	}()
	thttp.New("").Mount([]actionpkg.AnyAction{act})
}
