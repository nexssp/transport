package thttp

import (
	"context"
	"fmt"
	"iter"
	"net/http"
	"time"

	"github.com/nexssp/kernel/xerr"
	"github.com/nexssp/transport"
	"github.com/nexssp/transport/codec"
)

// StreamNDJSON writes an iterator directly to http.ResponseWriter as NDJSON with O(1) memory.
func StreamNDJSON[E any](c codec.Codec, getIter func(ctx context.Context) (iter.Seq2[E, error], error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		seq, err := getIter(r.Context())
		if err != nil {
			appErr := xerr.From(err)
			w.Header().Set("Content-Type", c.ContentType())
			w.WriteHeader(transport.MapToHTTPStatus(appErr.Kind))
			_ = c.NewEncoder(w).Encode(appErr.Public(""))
			return
		}

		_ = StreamIterator(r.Context(), w, seq, c)
	}
}

// StreamIterator writes an iter.Seq2 stream directly to the response socket.
func StreamIterator[T any](ctx context.Context, w http.ResponseWriter, seq iter.Seq2[T, error], c codec.Codec) error {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Cache-Control", "no-cache")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	enc := c.NewEncoder(w)

	for item, err := range seq {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("stream client disconnected: %w", ctxErr)
		}
		if err != nil {
			return xerr.Internal("stream iterator returned error", err)
		}

		if err := enc.Encode(item); err != nil {
			return fmt.Errorf("failed encoding stream item: %w", err)
		}

		if flusher != nil {
			flusher.Flush()
		}
	}
	return nil
}
