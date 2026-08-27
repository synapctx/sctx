package main

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synapctx/sctx/internal/adapters/telemetry/spool"
	"github.com/synapctx/sctx/internal/domain/telemetry"
	"github.com/synapctx/sctx/internal/platform/config"
)

// withPipedStdin replaces os.Stdin for the duration of the test with a pipe
// pre-loaded with body, restoring the original afterward.
func withPipedStdin(t *testing.T, body string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	if _, err := w.WriteString(body); err != nil {
		t.Fatalf("writing stdin pipe: %v", err)
	}
	w.Close()
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	return config.Config{
		ConfigFilePath: filepath.Join(dir, "config.toml"),
		SpoolDir:       filepath.Join(dir, "spool"),
	}
}

// testConfigAtHome is like testConfig but points its ConfigFilePath at the
// same path config.Load() would resolve for $HOME, so a config.Load() call
// mid-test (e.g. runInit's backlog-drain reload) sees what was just written.
func testConfigAtHome(t *testing.T) config.Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "sctx")
	return config.Config{
		ConfigFilePath: filepath.Join(dir, "config.toml"),
		SpoolDir:       filepath.Join(dir, "spool"),
	}
}

func TestRunInitSuccessWritesConfigAndDrainsBacklog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sctx_live_validtoken" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.MarshalEncode(jsontext.NewEncoder(w), map[string]string{"organization": "acme"})
	}))
	defer srv.Close()

	for _, k := range []string{"SCT__TELEMETRY_TOKEN", "SCT__TELEMETRY_ENDPOINT", "SCT__TELEMETRY_DEFAULT_ORG"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	cfg := testConfigAtHome(t)
	if err := spool.Append(cfg.SpoolDir, telemetry.Event{ID: "01A", Kind: telemetry.KindExecSavings, At: time.Now().UTC()}); err != nil {
		t.Fatalf("seeding spool: %v", err)
	}

	withPipedStdin(t, "sctx_live_validtoken\n")

	code := runInit(context.Background(), cfg, []string{"--endpoint", srv.URL})
	if code != 0 {
		t.Fatalf("runInit exit = %d, want 0", code)
	}

	info, err := os.Stat(cfg.ConfigFilePath)
	if err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config file mode = %o, want 0600", perm)
	}
	data, err := os.ReadFile(cfg.ConfigFilePath)
	if err != nil {
		t.Fatalf("reading config file: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, `token = "sctx_live_validtoken"`) || !strings.Contains(got, "[org.acme]") || !strings.Contains(got, srv.URL) {
		t.Fatalf("config file content = %q, missing expected endpoint/org section/token", got)
	}

	if pending := spool.CountPending(cfg.SpoolDir); pending != 0 {
		t.Fatalf("pending events after init = %d, want 0 (backlog should drain)", pending)
	}
}

func TestRunInitFailureWritesNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := testConfig(t)
	withPipedStdin(t, "sctx_live_badtoken\n")

	code := runInit(context.Background(), cfg, []string{"--endpoint", srv.URL})
	if code != 1 {
		t.Fatalf("runInit exit = %d, want 1", code)
	}
	if _, err := os.Stat(cfg.ConfigFilePath); !os.IsNotExist(err) {
		t.Fatalf("config file must not be written on auth failure, stat err = %v", err)
	}
}

// --key is what the console's per-organization setup panel hands a developer.
// It did not exist until 2026-07-27 while the console printed it anyway, so the
// documented one-liner simply failed with `unknown flag "--key"`.
//
// Stdin remains the secure path (a key in argv is visible in `ps`), so this also
// pins that --key does NOT read stdin — otherwise a piped invocation and a
// flagged one could disagree about which key won.
func TestRunInitAcceptsTheKeyFlagWithoutReadingStdin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sctx_live_flagtoken" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.MarshalEncode(jsontext.NewEncoder(w), map[string]string{"organization": "parlitrack"})
	}))
	defer srv.Close()

	for _, k := range []string{"SCT__TELEMETRY_TOKEN", "SCT__TELEMETRY_ENDPOINT", "SCT__TELEMETRY_DEFAULT_ORG"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	cfg := testConfigAtHome(t)

	// Deliberately pipe a DIFFERENT key: the flag must win and stdin must be
	// left alone, not merged or preferred.
	withPipedStdin(t, "sctx_live_stdintoken\n")

	code := runInit(context.Background(), cfg, []string{"--endpoint", srv.URL, "--key", "sctx_live_flagtoken"})
	if code != 0 {
		t.Fatalf("runInit exit = %d, want 0 — --key was rejected", code)
	}
	data, err := os.ReadFile(cfg.ConfigFilePath)
	if err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, `token = "sctx_live_flagtoken"`) {
		t.Errorf("config does not carry the --key value:\n%s", got)
	}
	if strings.Contains(got, "sctx_live_stdintoken") {
		t.Error("stdin was consumed even though --key was given")
	}
	// The org comes from the server's ping, never from the caller.
	if !strings.Contains(got, "[org.parlitrack]") {
		t.Errorf("config missing the org section the server reported:\n%s", got)
	}
}

// A --key with no value is a usage error, not a silent fall-through to stdin.
func TestRunInitKeyFlagRequiresAValue(t *testing.T) {
	cfg := testConfigAtHome(t)
	if code := runInit(context.Background(), cfg, []string{"--key"}); code != 2 {
		t.Errorf("exit = %d, want 2 for a valueless --key", code)
	}
}

// Piping still works: adding a flag must not break the path that existed first,
// which is the one to use on a shared machine.
func TestRunInitStillAcceptsPipedStdin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sctx_live_pipedtoken" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.MarshalEncode(jsontext.NewEncoder(w), map[string]string{"organization": "cloudresty"})
	}))
	defer srv.Close()

	for _, k := range []string{"SCT__TELEMETRY_TOKEN", "SCT__TELEMETRY_ENDPOINT", "SCT__TELEMETRY_DEFAULT_ORG"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	cfg := testConfigAtHome(t)
	withPipedStdin(t, "sctx_live_pipedtoken\n")

	if code := runInit(context.Background(), cfg, []string{"--endpoint", srv.URL}); code != 0 {
		t.Fatalf("runInit exit = %d, want 0", code)
	}
	data, _ := os.ReadFile(cfg.ConfigFilePath)
	if !strings.Contains(string(data), "[org.cloudresty]") {
		t.Errorf("piped init did not write the org section:\n%s", string(data))
	}
}
