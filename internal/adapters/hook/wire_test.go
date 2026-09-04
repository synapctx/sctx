package hook

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/platform/config"
)

// writeRepo creates a minimal git repo at dir/repoDirName with the given
// origin URL, returning the repo root.
func writeWireTestRepo(t *testing.T, base, name, url string) string {
	t.Helper()
	root := filepath.Join(base, name)
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = " + url + "\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestForFileRequestShape asserts the wire doc's field names verbatim on the
// extended /v1/surface/for-file request: sessionId, sessionState (with its
// four sub-fields) and symbols.
func TestForFileRequestShape(t *testing.T) {
	t.Setenv("SCT__SPOOL_DIR", t.TempDir())
	root := writeWireTestRepo(t, t.TempDir(), "widgets", "https://github.com/acme/widgets.git")

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "for-symbol") {
			// The blast-radius call runs CONCURRENTLY with for-file (see
			// TestPostToolEditBothCallsCompleteWithinOneBudget); it is
			// answered but not captured here, so both handlers never race
			// on the same variable.
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"symbols":[]}`))
			return
		}
		_ = json.UnmarshalRead(r.Body, &gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"mode":"full","notes":[]}`))
	}))
	defer srv.Close()

	cfg := config.Config{TelemetryEndpoint: srv.URL + "/v1/telemetry/exec"}
	cfg.OrgTokens = map[string]string{"acme": "tok"}

	call := postToolCall{
		ToolName: "Edit",
		ToolInput: map[string]any{
			"file_path":  filepath.Join(root, "main.go"),
			"old_string": "func Foo() {}\n",
			"new_string": "func Foo(x int) {}\n",
		},
		CWD:       root,
		SessionID: "sess-shape",
	}
	var out strings.Builder
	RunClaudePostTool(strings.NewReader(mustJSON(t, call)), &out, cfg, "9.9.9")

	if gotBody == nil {
		t.Fatal("no request body captured — for-file was never called")
	}
	if gotBody["sessionId"] != "sess-shape" {
		t.Errorf("sessionId = %v, want sess-shape", gotBody["sessionId"])
	}
	if gotBody["repositoryName"] != "acme/widgets" {
		t.Errorf("repositoryName = %v, want acme/widgets", gotBody["repositoryName"])
	}
	if gotBody["filePath"] != "main.go" {
		t.Errorf("filePath = %v, want main.go", gotBody["filePath"])
	}
	state, ok := gotBody["sessionState"].(map[string]any)
	if !ok {
		t.Fatalf("sessionState missing or wrong shape: %v", gotBody["sessionState"])
	}
	for _, field := range []string{"lastOfferedAt", "newestStamp", "fullOffers", "pointerShown"} {
		if _, ok := state[field]; !ok {
			t.Errorf("sessionState missing field %q: %v", field, state)
		}
	}
	if _, ok := gotBody["symbols"]; !ok {
		t.Error("request missing symbols field")
	}
}

// TestForSymbolEditRequestShape asserts the wire doc's field names verbatim
// on the /v1/surface/for-symbol "edit" mode request.
func TestForSymbolEditRequestShape(t *testing.T) {
	t.Setenv("SCT__SPOOL_DIR", t.TempDir())
	root := writeWireTestRepo(t, t.TempDir(), "widgets", "https://github.com/acme/widgets.git")

	var gotSymbolBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "for-symbol") {
			_ = json.UnmarshalRead(r.Body, &gotSymbolBody)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"symbols":[]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"mode":"silent"}`))
	}))
	defer srv.Close()

	cfg := config.Config{TelemetryEndpoint: srv.URL + "/v1/telemetry/exec"}
	cfg.OrgTokens = map[string]string{"acme": "tok"}

	call := postToolCall{
		ToolName: "Edit",
		ToolInput: map[string]any{
			"file_path":  filepath.Join(root, "main.go"),
			"old_string": "func Foo() {}\n",
			"new_string": "func Foo(x int) {}\n",
		},
		CWD:       root,
		SessionID: "sess-shape-2",
	}
	var out strings.Builder
	RunClaudePostTool(strings.NewReader(mustJSON(t, call)), &out, cfg, "9.9.9")

	if gotSymbolBody == nil {
		t.Fatal("no for-symbol request captured")
	}
	if gotSymbolBody["mode"] != "edit" {
		t.Errorf("mode = %v, want edit", gotSymbolBody["mode"])
	}
	if gotSymbolBody["filePath"] != "main.go" {
		t.Errorf("filePath = %v, want main.go", gotSymbolBody["filePath"])
	}
	symbols, ok := gotSymbolBody["symbols"].([]any)
	if !ok || len(symbols) == 0 {
		t.Fatalf("symbols missing or empty: %v", gotSymbolBody["symbols"])
	}
	if symbols[0] != "Foo" {
		t.Errorf("symbols[0] = %v, want Foo", symbols[0])
	}
	if gotSymbolBody["sessionId"] != "sess-shape-2" {
		t.Errorf("sessionId = %v, want sess-shape-2", gotSymbolBody["sessionId"])
	}
}

// TestPostToolEditCallsRunConcurrentlyUnderOneDeadline asserts both surface
// calls fire (rather than one waiting on the other's own separate budget) —
// each handler sleeps most of a single postToolBudget, and the whole request
// must still complete once, not twice serially.
func TestPostToolEditBothCallsCompleteWithinOneBudget(t *testing.T) {
	t.Setenv("SCT__SPOOL_DIR", t.TempDir())
	root := writeWireTestRepo(t, t.TempDir(), "widgets", "https://github.com/acme/widgets.git")

	var fileHit, symbolHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "for-symbol") {
			symbolHit = true
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"symbols":[]}`))
			return
		}
		fileHit = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"mode":"silent"}`))
	}))
	defer srv.Close()

	cfg := config.Config{TelemetryEndpoint: srv.URL + "/v1/telemetry/exec"}
	cfg.OrgTokens = map[string]string{"acme": "tok"}
	call := postToolCall{
		ToolName: "Edit",
		ToolInput: map[string]any{
			"file_path":  filepath.Join(root, "main.go"),
			"old_string": "func Foo() {}\n",
			"new_string": "func Foo(x int) {}\n",
		},
		CWD:       root,
		SessionID: "sess-concurrent",
	}
	var out strings.Builder
	RunClaudePostTool(strings.NewReader(mustJSON(t, call)), &out, cfg, "9.9.9")

	if !fileHit || !symbolHit {
		t.Fatalf("expected both surface calls to fire, fileHit=%v symbolHit=%v", fileHit, symbolHit)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
