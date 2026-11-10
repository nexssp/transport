package thttp

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/nexssp/kernel/xctx"
	"github.com/nexssp/kernel/xerr"

	"github.com/nexssp/transport"
	"github.com/nexssp/transport/codec"
)

type pathDispatcher struct {
	codec    codec.Codec
	routes   map[string]http.Handler
	methods  []string
	wildcard http.Handler
}

func newPathDispatcher(responseCodec codec.Codec) *pathDispatcher {
	if responseCodec == nil {
		responseCodec = codec.Default
	}
	return &pathDispatcher{codec: responseCodec, routes: make(map[string]http.Handler)}
}

func (dispatcher *pathDispatcher) add(method string, handler http.Handler) {
	if handler == nil {
		panic("thttp: route handler must not be nil")
	}
	if method == "" {
		if dispatcher.wildcard != nil {
			panic("thttp: duplicate method-agnostic route")
		}
		dispatcher.wildcard = handler
		return
	}
	if _, exists := dispatcher.routes[method]; exists {
		panic("thttp: duplicate route method " + method)
	}
	dispatcher.routes[method] = handler
	dispatcher.methods = append(dispatcher.methods, method)
}

func (t *Transport) registerRoute(path, method string, handler http.Handler) {
	pattern := formatPattern("", path)
	dispatcher, exists := t.routes[pattern]
	if !exists {
		dispatcher = newPathDispatcher(t.codec)
		t.routes[pattern] = dispatcher
		t.mux.Handle(pattern, dispatcher)
	}
	dispatcher.add(strings.ToUpper(strings.TrimSpace(method)), handler)
}

func (dispatcher *pathDispatcher) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler, ok := dispatcher.routes[request.Method]
	if !ok && request.Method == http.MethodHead {
		handler, ok = dispatcher.routes[http.MethodGet]
	}
	if ok {
		handler.ServeHTTP(writer, request)
		return
	}
	if dispatcher.wildcard != nil {
		dispatcher.wildcard.ServeHTTP(writer, request)
		return
	}
	if request.Method == http.MethodOptions {
		setDispatchRequestID(writer, request)
		writer.Header().Set("Allow", dispatcher.allowHeader())
		writer.WriteHeader(http.StatusNoContent)
		return
	}

	requestID := setDispatchRequestID(writer, request)
	writer.Header().Set("Allow", dispatcher.allowHeader())
	writer.Header().Set("Content-Type", dispatcher.codec.ContentType())
	writer.WriteHeader(transport.MapToHTTPStatus(xerr.KindMethodNotAllowed))
	if err := dispatcher.codec.NewEncoder(writer).Encode(xerr.MethodNotAllowed("method not allowed").Public(requestID)); err != nil {
		slog.ErrorContext(request.Context(), "thttp: write 405 response", "error", err, "request_id", requestID)
	}
}

func (dispatcher *pathDispatcher) allowHeader() string {
	methods := make([]string, 0, len(dispatcher.methods)+1)
	for _, method := range dispatcher.methods {
		methods = append(methods, method)
		if method == http.MethodGet {
			if _, hasExplicitHead := dispatcher.routes[http.MethodHead]; !hasExplicitHead {
				methods = append(methods, http.MethodHead)
			}
		}
	}
	return strings.Join(methods, ", ")
}

func setDispatchRequestID(writer http.ResponseWriter, request *http.Request) string {
	requestID := xctx.RequestIDFrom(request.Context())
	if requestID == "" {
		requestID = request.Header.Get(transport.HeaderRequestID)
	}
	if requestID != "" {
		writer.Header().Set(transport.HeaderRequestID, requestID)
	}
	return requestID
}
