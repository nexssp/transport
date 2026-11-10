package thttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nexssp/flow/compiler/contracts"
	"github.com/nexssp/kernel/action"

	"github.com/nexssp/transport"
	"github.com/nexssp/transport/codec"
)

var _ transport.Transport = (*Transport)(nil)

type Option func(*Transport)

type Transport struct {
	addr              string
	mux               *http.ServeMux
	routes            map[string]*pathDispatcher
	server            *http.Server
	mdws              []func(http.Handler) http.Handler
	broadcaster       StreamBroadcaster
	idemStore         action.IdempotencyStore
	codec             codec.Codec
	maxBodyBytes      int64
	maxIdemRespBytes  int
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
		routes:            make(map[string]*pathDispatcher),
		broadcaster:       NewBroadcaster(16),
		idemStore:         action.NewMemoryIdempotencyStore(0),
		codec:             codec.Default,
		maxBodyBytes:      10 << 20,
		maxIdemRespBytes:  defaultMaxIdempotentResponseBytes,
		readHeaderTimeout: 5 * time.Second,
		readTimeout:       15 * time.Second,
		writeTimeout:      30 * time.Second,
		idleTimeout:       120 * time.Second,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
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
func WithReadHeaderTimeout(d time.Duration) Option {
	return func(t *Transport) { t.readHeaderTimeout = d }
}
func WithIdleTimeout(d time.Duration) Option { return func(t *Transport) { t.idleTimeout = d } }

func WithIdempotencyResponseLimit(limit int) Option {
	return func(t *Transport) {
		if limit >= 0 {
			t.maxIdemRespBytes = limit
		}
	}
}

func (t *Transport) CanHandle(b action.Binding) bool {
	switch b.(type) {
	case HTTPRoute, RawHTTPRoute, SSERoute, RawHTTPHandler:
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
				h := t.httpHandler(ex, meta, modeTyped)
				if meta != nil && meta.Idempotency.Enabled && t.idemStore != nil {
					h = withIdempotency(t.idemStore, meta.Idempotency, h, t.maxIdemRespBytes)
				}
				t.registerRoute(r.Path, r.Method, h)
			case RawHTTPRoute:
				t.registerRoute(r.Path, r.Method, t.httpHandler(ex, meta, modeRaw))
			case SSERoute:
				broadcaster := r.Broadcaster
				if broadcaster == nil {
					broadcaster = t.broadcaster
				}
				t.registerRoute(r.Path, http.MethodGet, t.sseHandler(r.Channel, broadcaster))
			case RawHTTPHandler:
				t.registerRoute(r.Path, r.Method, r.Handler)
			}
		}
	}
}

func (t *Transport) Handler() http.Handler {
	var h http.Handler = t.mux
	for _, m := range slices.Backward(t.mdws) {
		h = m(h)
	}
	return RequestIdentity(h)
}

func (t *Transport) Do(ctx context.Context, _ any) (any, error) {
	if ctx == nil {
		return nil, errors.New("http server context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", t.addr)
	if err != nil {
		return nil, fmt.Errorf("http server crash: %w", err)
	}
	server := &http.Server{
		Addr:              t.addr,
		Handler:           t.Handler(),
		ReadHeaderTimeout: t.readHeaderTimeout,
		ReadTimeout:       t.readTimeout,
		WriteTimeout:      t.writeTimeout,
		IdleTimeout:       t.idleTimeout,
	}
	t.server = server

	serveErr := make(chan error, 1)
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("http server crash: %w", err)
		}
	}()
	if err := ctx.Err(); err != nil {
		_ = server.Close()
		<-serveDone
		return nil, err
	}
	contracts.SignalReady(ctx)

	select {
	case err := <-serveErr:
		<-serveDone
		return nil, err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		<-serveDone
		return nil, fmt.Errorf("http server graceful shutdown failed: %w", err)
	}
	<-serveDone
	return nil, ctx.Err()
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
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{}) //nolint:errcheck // best-effort; not all ResponseWriters support it

		ch, unsubscribe := broadcaster.Subscribe(channel)
		defer unsubscribe()

		_, _ = w.Write([]byte("event: connected\ndata: {\"status\":\"ready\"}\n\n")) //nolint:errcheck // response write failure is terminal
		flusher.Flush()

		pingTicker := time.NewTicker(25 * time.Second)
		defer pingTicker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-pingTicker.C:
				_, _ = w.Write([]byte(": ping\n\n")) //nolint:errcheck // response write failure is terminal
				flusher.Flush()
			case payload, ok := <-ch:
				if !ok {
					return
				}
				message := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(string(payload))
				_, _ = w.Write([]byte("event: message\ndata: " + strings.ReplaceAll(message, "\n", "\ndata: ") + "\n\n")) //nolint:errcheck // response write failure is terminal
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
