package hook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/synapctx/sctx/internal/platform/config"
	"github.com/synapctx/sctx/internal/platform/httpclient"
)

// TestPostSurfaceSendsSctxUserAgent covers the one HTTP shape both the
// for-file and for-symbol surface calls share (fetchNotes/fetchElsewhere):
// the request must identify itself as sctx traffic rather than the default
// Go-http-client.
func TestPostSurfaceSendsSctxUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"notes":[]}`))
	}))
	defer srv.Close()

	cfg := config.Config{TelemetryEndpoint: srv.URL + "/v1/telemetry/exec"}
	var out forFileResponse
	if err := postSurface(context.Background(), cfg, "tok", surfacePath, map[string]string{"repositoryName": "acme/widgets"}, &out, "9.9.9"); err != nil {
		t.Fatalf("postSurface: %v", err)
	}
	want := httpclient.UserAgent("9.9.9", "claude-code")
	if gotUA != want {
		t.Errorf("User-Agent = %q, want %q", gotUA, want)
	}
}
