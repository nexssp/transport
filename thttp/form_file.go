package thttp

import (
	"io"
	"net/http"

	"github.com/nexssp/kernel/xerr"
)

// FormFile provides multipart/form-data upload decoding.
type FormFile struct {
	Filename    string
	ContentType string
	Bytes       []byte
}

// ReadField parses a multipart form field into f. The caller MUST have
// already bounded r.Body (typically via http.MaxBytesReader in the HTTP
// handler). maxBytes caps the per-field read below; it does not replace
// bounding the request body upstream.
func (f *FormFile) ReadField(r *http.Request, fieldName string, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = 10 << 20 // 10 MiB default per-field cap
	}
	if err := r.ParseMultipartForm(maxBytes); err != nil { //nolint:gosec // request body is bounded by the caller's http.MaxBytesReader; maxBytes caps this field read
		return xerr.BadRequest("multipart form body too large")
	}
	file, header, err := r.FormFile(fieldName)
	if err != nil {
		return xerr.BadRequest("missing file in form field: " + fieldName)
	}
	defer file.Close()

	f.Filename = header.Filename
	f.ContentType = header.Header.Get("Content-Type")
	f.Bytes, err = io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return xerr.BadRequest("failed reading uploaded file")
	}
	return nil
}

// ReadFormFile extracts and reads a file from a multipart form request.
func ReadFormFile(r *http.Request, fieldName string, maxBytes int64) (data []byte, contentType string, err error) {
	var f FormFile
	if err := f.ReadField(r, fieldName, maxBytes); err != nil {
		return nil, "", err
	}
	return f.Bytes, f.ContentType, nil
}
