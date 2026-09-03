package tcli_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexssp/transport/tcli"
)

func TestDebugCommands(t *testing.T) {
	t.Parallel()

	debugActions := tcli.AddDebugCommands()
	if len(debugActions) != 2 {
		t.Fatalf("expected 2 debug commands, got %d", len(debugActions))
	}

	t.Run("Host Check against live test server", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		var stdout, stderr bytes.Buffer
		cli := tcli.New(
			tcli.WithArgs("-host-check", ts.URL),
			tcli.WithOutput(&stdout, &stderr),
		)
		cli.Mount(debugActions)

		res, err := cli.Do(context.Background(), nil)
		if err != nil {
			t.Fatalf("unexpected host-check error: %v", err)
		}
		if !strings.Contains(res.(string), "OK (HTTP 200)") {
			t.Fatalf("expected 'OK (HTTP 200)', got %q", res)
		}
	})
}
