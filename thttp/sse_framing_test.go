package thttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type fixedStreamBroadcaster struct {
	payload []byte
}

func (b fixedStreamBroadcaster) Subscribe(_ string) (events <-chan []byte, unsubscribe func()) {
	ch := make(chan []byte, 1)
	ch <- b.payload
	close(ch)
	return ch, func() {}
}

func (fixedStreamBroadcaster) Publish(_ string, _ []byte) (delivered, dropped int) { return 0, 0 }

func TestSSEHandlerFramesPayloadAsDataLines(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "multiline payload",
			payload: "first\nsecond\nthird",
			want:    "data: first\ndata: second\ndata: third\n",
		},
		{
			name:    "CRLF and CR normalize as line boundaries",
			payload: "first\r\nsecond\rthird\nfourth",
			want:    "data: first\ndata: second\ndata: third\ndata: fourth\n",
		},
		{
			name:    "attempted event and id injection",
			payload: "safe\nevent: forged\rid: forged",
			want:    "data: safe\ndata: event: forged\ndata: id: forged\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/events", http.NoBody)
			New("").sseHandler("test", fixedStreamBroadcaster{payload: []byte(tt.payload)})(recorder, request)

			want := "event: connected\ndata: {\"status\":\"ready\"}\n\nevent: message\n" + tt.want + "\n"
			if got := recorder.Body.String(); got != want {
				t.Fatalf("SSE output mismatch\n got: %q\nwant: %q", got, want)
			}
		})
	}
}
