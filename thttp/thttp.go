package thttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/transport"
	"github.com/nexssp/transport/codec"
)

var payloadPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, 4096))
	},
}

type Option func(*Transport)

type Transport struct {
	addr              string
	mux               *http.ServeMux
	server            *http.Server
	mdws              []func(http.Handler) http.Handler
	broadcaster       StreamBroadcaster
	idemStore         action.IdempotencyStore
	codec             codec.Codec
	maxBodyBytes      int64
	readHeaderTimeout time.Duration
	readTimeout       time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
}

var _ transport.Transport = (*Transport)(nil)

func New(addr string, opts ...Option) *Transport {
	if addr != "" && !strings.Contains(addr, ":") {
		addr = ":" + addr
	}

	t := &Transport{
		addr:              addr,
		mux:               http.NewServeMux(),
		broadcaster:       NewBroadcaster(16),
		idemStore:         action.NewMemoryIdempotencyStore(0),
		codec:             codec.Default,
		maxBodyBytes:      10 << 20,
		readHeaderTimeout: 5 * time.Second,
		readTimeout:       15 * time.Second,
		writeTimeout:      30 * time.Second,
		idleTimeout:       120 * time.Second,
	}

	for _, opt := range opts {
		opt(t)
	}
	return t
}

func WithCodec(c codec.Codec) Option {
	return func(t *Transport) {
		if c != nil {
			t.codec = c
		}
	}
}

func WithIdempotencyStore(store action.IdempotencyStore) Option {
	return func(t *Transport) {
		t.idemStore = store
	}
}

func WithBroadcaster(b StreamBroadcaster) Option {
	return func(t *Transport) {
		if b != nil {
			t.broadcaster = b
		}
	}
}

func WithReadTimeout(d time.Duration) Option  { return func(t *Transport) { t.readTimeout = d } }
func WithWriteTimeout(d time.Duration) Option { return func(t *Transport) { t.writeTimeout = d } }

func (t *Transport) CanHandle(b action.Binding) bool {
	switch b.(type) {
	case HTTPRoute, SSERoute, RawHTTPHandler:
		return true
	default:
		return false
	}
}

func (t *Transport) String() string {
	return "http(" + t.addr + ")"
}

func (t *Transport) Mux() *http.ServeMux { return t.mux }

func (t *Transport) Use(mw ...func(http.Handler) http.Handler) *Transport {
	t.mdws = append(t.mdws, mw...)
	return t
}

func (t *Transport) Mount(actions []action.AnyAction) {
	validateBindings(actions)
	for _, act := range actions {
		ex, ok := act.(action.Executable)
		if !ok {
			continue
		}
		meta := act.Describe()

		for _, b := range act.GetBindings() {
			switch r := b.(type) {
			case HTTPRoute:
				pattern := formatPattern(r.Method, r.Path)
				h := t.httpHandler(ex, meta)
				if meta != nil && meta.Idempotency.Enabled && t.idemStore != nil {
					h = withIdempotency(t.idemStore, meta.Idempotency, h)
				}
				t.mux.HandleFunc(pattern, h)
			case SSERoute:
				pattern := formatPattern(http.MethodGet, r.Path)
				broadcaster := r.Broadcaster
				if broadcaster == nil {
					broadcaster = t.broadcaster
				}
				t.mux.HandleFunc(pattern, t.sseHandler(r.Channel, broadcaster))
			case RawHTTPHandler:
				pattern := formatPattern(r.Method, r.Path)
				t.mux.HandleFunc(pattern, r.Handler)
			}
		}
	}
}

func (t *Transport) Handler() http.Handler {
	var h http.Handler = t.mux
	for _, m := range slices.Backward(t.mdws) {
		h = m(h)
	}
	return h
}

func (t *Transport) Do(ctx context.Context, _ any) (any, error) {
	t.server = &http.Server{
		Addr:              t.addr,
		Handler:           t.Handler(),
		ReadHeaderTimeout: t.readHeaderTimeout,
		ReadTimeout:       t.readTimeout,
		WriteTimeout:      t.writeTimeout,
		IdleTimeout:       t.idleTimeout,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = t.server.Shutdown(shutCtx)
	}()

	if err := t.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return nil, fmt.Errorf("http server crash: %w", err)
	}
	return nil, nil
}

func (t *Transport) httpHandler(ex action.Executable, meta *action.Meta) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		reqID := r.Header.Get(transport.HeaderRequestID)
		if reqID != "" {
			w.Header().Set(transport.HeaderRequestID, reqID)
		}
		if execID := r.Header.Get(transport.HeaderExecutionID); execID != "" {
			ctx = action.WithExecutionID(ctx, execID)
			w.Header().Set(transport.HeaderExecutionID, execID)
		}
		traceID, spanID := r.Header.Get(transport.HeaderTraceID), r.Header.Get(transport.HeaderSpanID)
		if traceID != "" || spanID != "" {
			ctx = action.WithTraceContext(ctx, traceID, spanID)
		}

		res, err := ex.ExecuteDecoded(ctx, func(v any) error {
			if d, ok := v.(HTTPDecoder); ok {
				return d.FromHTTPRequest(r)
			}

			// Automatic struct tag binding (path, query, header, cookie, form)
			if err := BindFromTags(v, r); err != nil {
				return err
			}

			// Skip body decoding for methods without request payload
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Body == nil || r.Body == http.NoBody {
				return nil
			}

			// Acquire buffer ONLY when reading actual body payload
			buf := payloadPool.Get().(*bytes.Buffer)
			buf.Reset()
			defer func() {
				if buf.Cap() <= 64*1024 { // Ensure buffers larger than 64 KiB are released to GC rather than retained in `sync.Pool`.
					payloadPool.Put(buf)
				}
			}()

			n, readErr := buf.ReadFrom(io.LimitReader(r.Body, t.maxBodyBytes+1))
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return xerr.BadRequest("failed reading request body", readErr)
			}
			if n > t.maxBodyBytes {
				return xerr.BadRequest(fmt.Sprintf("request body exceeds limit of %d bytes", t.maxBodyBytes))
			}
			if buf.Len() == 0 {
				return nil
			}

			if unmarshalErr := t.codec.Unmarshal(buf.Bytes(), v); unmarshalErr != nil {
				return xerr.BadRequest(fmt.Sprintf("invalid %s payload: %v", t.codec.Name(), unmarshalErr))
			}
			return nil
		})

		w.Header().Set("Content-Type", t.codec.ContentType())

		if err != nil {
			appErr := xerr.From(err)
			w.WriteHeader(transport.MapToHTTPStatus(appErr.Kind))
			_ = t.codec.NewEncoder(w).Encode(appErr.Public(reqID))
			return
		}

		status := http.StatusOK
		if r.Method == http.MethodPost {
			status = http.StatusCreated
		}
		if meta != nil && meta.SuccessStatus != 0 {
			status = meta.SuccessStatus
		}

		w.WriteHeader(status)
		_ = t.codec.NewEncoder(w).Encode(res)
	}
}

func (t *Transport) sseHandler(channel string, broadcaster StreamBroadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

		ch, unsubscribe := broadcaster.Subscribe(channel)
		defer unsubscribe()

		_, _ = w.Write([]byte("event: connected\ndata: {\"status\":\"ready\"}\n\n"))
		flusher.Flush()

		pingTicker := time.NewTicker(25 * time.Second)
		defer pingTicker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-pingTicker.C:
				_, _ = w.Write([]byte(": ping\n\n"))
				flusher.Flush()
			case payload, ok := <-ch:
				if !ok {
					return
				}
				_, _ = w.Write([]byte("event: message\ndata: " + string(payload) + "\n\n"))
				flusher.Flush()
			}
		}
	}
}

func formatPattern(method, path string) string {
	if path == "" {
		path = "/"
	} else if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if method == "" {
		return path
	}
	return method + " " + path
}
