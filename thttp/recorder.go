package thttp

import (
	"bytes"
	"net/http"
	"sync"
)

// bufferedResponseWriter is a bounded, poolable replacement for
// httptest.ResponseRecorder. It captures status, headers, and body for
// idempotency replay without unbounded memory growth.
//
// limit == 0 means unbounded (matches old ResponseRecorder behavior).
// A response exceeding a non-zero limit is marked truncated and not cached.
type bufferedResponseWriter struct {
	header    http.Header
	body      bytes.Buffer
	status    int
	limit     int
	written   bool
	truncated bool
}

var bufferedWriterPool = sync.Pool{
	New: func() any {
		return &bufferedResponseWriter{header: make(http.Header, 8)}
	},
}

func acquireBufferedWriter(limit int) *bufferedResponseWriter {
	w := bufferedWriterPool.Get().(*bufferedResponseWriter) //nolint:forcetypeassert // pool.New always returns *bufferedResponseWriter
	w.header = make(http.Header, 8)
	w.body.Reset()
	w.status = http.StatusOK
	w.limit = limit
	w.written = false
	w.truncated = false
	return w
}

func releaseBufferedWriter(w *bufferedResponseWriter) {
	// Do not pin huge buffers in the pool.
	if w.body.Cap() > 64<<10 {
		return
	}
	w.header = nil
	bufferedWriterPool.Put(w)
}

func (w *bufferedResponseWriter) Header() http.Header { return w.header }

func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.written {
		return
	}
	w.status = status
	w.written = true
}

func (w *bufferedResponseWriter) Write(p []byte) (int, error) {
	if !w.written {
		w.written = true
	}
	if w.truncated {
		return len(p), nil
	}
	if w.limit > 0 {
		remaining := w.limit - w.body.Len()
		if len(p) > remaining {
			w.truncated = true
			return len(p), nil
		}
	}
	return w.body.Write(p)
}
