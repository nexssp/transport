package thttp

import "net/http"

type HTTPDecoder interface {
	FromHTTPRequest(r *http.Request) error
}
