package thttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/nexssp/kernel/xctx"
)

// NDJSONRoute binds an action to a request/response route that emits NDJSON
// events while the action runs and a final done or error event when it returns.
type NDJSONRoute struct {
	Method string
	Path   string
}

func (route NDJSONRoute) String() string { return route.Method + " " + route.Path + " (NDJSON stream)" }

// StreamRoute constructs an NDJSON route binding.
func StreamRoute(method, path string) NDJSONRoute {
	return NDJSONRoute{Method: method, Path: path}
}

// StreamEvent is the standard event envelope for NDJSON routes.
type StreamEvent struct {
	Type   string `json:"type"`
	Delta  string `json:"delta,omitempty"`
	Result any    `json:"result,omitempty"`
	Error  any    `json:"error,omitempty"`
}

// StreamEventWriter writes one JSON value followed by a newline and flushes it.
// Implementations are scoped to a single live HTTP response.
type StreamEventWriter interface {
	WriteEvent(context.Context, any) error
}

var streamEventWriterKey = xctx.NewKey[StreamEventWriter]("transport.thttp.stream_event_writer")

// WithStreamEventWriter attaches the live response event writer to a request
// context so pipeline actions can emit events without depending on AI packages.
func WithStreamEventWriter(ctx context.Context, writer StreamEventWriter) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if writer == nil {
		return ctx
	}
	return streamEventWriterKey.With(ctx, writer)
}

// StreamEventWriterFromContext returns the writer attached to an NDJSON route.
func StreamEventWriterFromContext(ctx context.Context) (StreamEventWriter, bool) {
	if ctx == nil {
		return nil, false
	}
	writer, ok := streamEventWriterKey.From(ctx)
	return writer, ok && writer != nil
}

type httpStreamEventWriter struct {
	mu         sync.Mutex
	writer     http.ResponseWriter
	controller *http.ResponseController
	encoder    *json.Encoder
	started    bool
}

func newHTTPStreamEventWriter(writer http.ResponseWriter) *httpStreamEventWriter {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return &httpStreamEventWriter{
		writer:     writer,
		controller: http.NewResponseController(writer),
		encoder:    encoder,
	}
}

func (stream *httpStreamEventWriter) WriteEvent(ctx context.Context, event any) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if !stream.started {
		stream.writer.Header().Set("Content-Type", "application/x-ndjson")
		stream.writer.Header().Set("Cache-Control", "no-cache")
		stream.writer.Header().Set("X-Accel-Buffering", "no")
		_ = stream.controller.SetWriteDeadline(time.Time{}) //nolint:errcheck // best-effort for writers without deadline support
		stream.writer.WriteHeader(http.StatusOK)
		stream.started = true
	}
	if err := stream.encoder.Encode(event); err != nil {
		return err
	}
	if err := stream.controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}
