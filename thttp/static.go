package thttp

import (
	"context"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
)

// StaticRequest serves a bounded directory map produced by Flow's @embed
// directive. Files are read-only and keyed by slash-separated relative paths.
type StaticRequest struct {
	Files                 map[string][]byte `json:"files"`
	Index                 string            `json:"index,omitempty"`
	BasePath              string            `json:"base_path,omitempty"`
	SPA                   bool              `json:"spa,omitempty"`
	CacheControl          string            `json:"cache_control,omitempty"`
	ContentSecurityPolicy string            `json:"content_security_policy,omitempty"`
}

// StaticAction serves embedded assets from a raw route using the live request.
func StaticAction() action.AnyAction {
	return action.New("thttp.static", func(ctx context.Context, input StaticRequest) (RawHTTPResponse, error) {
		rawRequest, ok := RawHTTPRequestFromContext(ctx)
		if !ok {
			return RawHTTPResponse{}, xerr.BadRequest("thttp.static requires a :raw_route request")
		}
		return staticResponse(input, rawRequest.Request())
	}).Build()
}

func staticResponse(input StaticRequest, request *http.Request) (RawHTTPResponse, error) {
	if request == nil {
		return RawHTTPResponse{}, xerr.BadRequest("thttp.static request is nil")
	}
	if input.Files == nil {
		return RawHTTPResponse{}, xerr.Validation("thttp.static files are required")
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		header := http.Header{"Allow": []string{"GET, HEAD"}}
		return NewRawHTTPResponse(http.StatusMethodNotAllowed, header, []byte("method not allowed\n")), nil
	}

	index := strings.TrimSpace(input.Index)
	if index == "" {
		index = "index.html"
	}
	if !fs.ValidPath(index) {
		return RawHTTPResponse{}, xerr.Validation("thttp.static index must be a valid relative path")
	}
	name, ok := staticRelativePath(input.BasePath, request.URL.Path)
	if !ok {
		return staticNotFound(), nil
	}
	if name == "" {
		name = index
	}
	data, found := input.Files[name]
	servedName := name
	if !found && input.SPA && path.Ext(name) == "" {
		data, found = input.Files[index]
		servedName = index
	}
	if !found {
		return staticNotFound(), nil
	}

	contentType := mime.TypeByExtension(path.Ext(servedName))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	cacheControl := strings.TrimSpace(input.CacheControl)
	if cacheControl == "" {
		cacheControl = "no-store"
	}
	header := make(http.Header, 5)
	header.Set("Content-Type", contentType)
	header.Set("Content-Length", strconv.Itoa(len(data)))
	header.Set("Cache-Control", cacheControl)
	header.Set("X-Content-Type-Options", "nosniff")
	if csp := strings.TrimSpace(input.ContentSecurityPolicy); csp != "" {
		header.Set("Content-Security-Policy", csp)
	}
	body := data
	if request.Method == http.MethodHead {
		body = nil
	}
	return RawHTTPResponse{StatusCode: http.StatusOK, Header: header, Body: body}, nil
}

func staticRelativePath(basePath, requestPath string) (string, bool) {
	if !strings.HasPrefix(requestPath, "/") {
		return "", false
	}
	base := strings.TrimSpace(basePath)
	if base != "" && base != "/" {
		if !strings.HasPrefix(base, "/") {
			return "", false
		}
		base = strings.TrimRight(base, "/")
		if path.Clean(base) != base {
			return "", false
		}
		if requestPath == base || requestPath == base+"/" {
			return "", true
		}
		if !strings.HasPrefix(requestPath, base+"/") {
			return "", false
		}
		requestPath = strings.TrimPrefix(requestPath, base)
	}
	name := strings.Trim(strings.TrimPrefix(requestPath, "/"), "/")
	if name == "" {
		return "", true
	}
	if !fs.ValidPath(name) {
		return "", false
	}
	return name, true
}

func staticNotFound() RawHTTPResponse {
	header := http.Header{
		"Content-Type":           []string{"text/plain; charset=utf-8"},
		"Cache-Control":          []string{"no-store"},
		"X-Content-Type-Options": []string{"nosniff"},
	}
	return NewRawHTTPResponse(http.StatusNotFound, header, []byte("404 page not found\n"))
}
