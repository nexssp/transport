package thttp

import "net/http"

type HTTPRoute struct {
	Method string
	Path   string
}

func (r HTTPRoute) String() string { return r.Method + " " + r.Path }

func GET(path string) HTTPRoute    { return HTTPRoute{Method: http.MethodGet, Path: path} }
func POST(path string) HTTPRoute   { return HTTPRoute{Method: http.MethodPost, Path: path} }
func PUT(path string) HTTPRoute    { return HTTPRoute{Method: http.MethodPut, Path: path} }
func DELETE(path string) HTTPRoute { return HTTPRoute{Method: http.MethodDelete, Path: path} }
func PATCH(path string) HTTPRoute  { return HTTPRoute{Method: http.MethodPatch, Path: path} }
func HEAD(path string) HTTPRoute   { return HTTPRoute{Method: http.MethodHead, Path: path} }

type SSERoute struct {
	Path        string
	Channel     string
	Broadcaster StreamBroadcaster
}

func (r SSERoute) String() string { return "SSE " + r.Path }

func Stream(path string) SSERoute { return SSERoute{Path: path, Channel: path} }

func (r SSERoute) WithChannel(ch string) SSERoute {
	r.Channel = ch
	return r
}

func (r SSERoute) WithBroadcaster(b StreamBroadcaster) SSERoute {
	r.Broadcaster = b
	return r
}

type RawHTTPHandler struct {
	Method  string
	Path    string
	Handler http.HandlerFunc
}

func (r RawHTTPHandler) String() string { return r.Method + " " + r.Path + " (raw)" }

func RawHandler(method, path string, h http.HandlerFunc) RawHTTPHandler {
	return RawHTTPHandler{Method: method, Path: path, Handler: h}
}
