package thttp

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func multipartFileRequest(t *testing.T, contents []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("upload", "example.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func TestFormFileReadFieldEnforcesPerFieldLimit(t *testing.T) {
	const limit = 4

	t.Run("exact limit succeeds", func(t *testing.T) {
		request := multipartFileRequest(t, []byte("four"))
		var file FormFile
		if err := file.ReadField(request, "upload", limit); err != nil {
			t.Fatalf("ReadField() error = %v", err)
		}
		if string(file.Bytes) != "four" {
			t.Errorf("Bytes = %q, want %q", file.Bytes, "four")
		}
		if file.Filename != "example.bin" {
			t.Errorf("Filename = %q, want %q", file.Filename, "example.bin")
		}
	})

	t.Run("limit plus one is rejected and state is cleared", func(t *testing.T) {
		request := multipartFileRequest(t, []byte("five!"))
		file := FormFile{Filename: "stale", ContentType: "stale", Bytes: []byte("stale")}
		if err := file.ReadField(request, "upload", limit); err == nil {
			t.Fatal("ReadField() error = nil, want oversize rejection")
		}
		if file.Filename != "" || file.ContentType != "" || file.Bytes != nil {
			t.Errorf("receiver not cleared after failure: %+v", file)
		}
	})
}
