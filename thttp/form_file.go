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

func (f *FormFile) ReadField(r *http.Request, fieldName string, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = 10 << 20 // 10 MB limit
	}
	if err := r.ParseMultipartForm(maxBytes); err != nil {
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
func ReadFormFile(r *http.Request, fieldName string, maxBytes int64) ([]byte, string, error) {
	var f FormFile
	if err := f.ReadField(r, fieldName, maxBytes); err != nil {
		return nil, "", err
	}
	return f.Bytes, f.ContentType, nil
}
