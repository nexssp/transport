package transport

import (
	"net/http"
	"testing"

	"github.com/nexssp/kernel/xerr"
)

func TestMapToHTTPStatusUnsupportedMediaType(t *testing.T) {
	if got := MapToHTTPStatus(xerr.KindUnsupportedMediaType); got != http.StatusUnsupportedMediaType {
		t.Fatalf("MapToHTTPStatus(UnsupportedMediaType) = %d, want %d", got, http.StatusUnsupportedMediaType)
	}
}
