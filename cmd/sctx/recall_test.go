package main

import (
	"bytes"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/synapctx/sctx/internal/platform/config"
	"github.com/synapctx/sctx/internal/platform/rawcache"
)

func recallFixture(t *testing.T, stdout, stderr string) (config.Config, string) {
	t.Helper()
	cfg := config.Config{RawCacheDir: filepath.Join(t.TempDir(), "raw"), RawCacheTTL: time.Hour, RawCacheMaxBytes: 1 << 20}
	entry, err := rawcache.New(cfg.RawCacheDir, cfg.RawCacheTTL, cfg.RawCacheMaxBytes).StoreFor("/usr/bin/make", []byte(stdout), []byte(stderr))
	if err != nil {
		t.Fatal(err)
	}
	return cfg, entry.ID
}

func TestRecall(t *testing.T) {
	out := "alpha\nbeta\x00\ngamma\ndelta\nno-newline"
	cfg, id := recallFixture(t, out, "oops\n")
	tests := []struct {
		name     string
		args     []string
		want     string
		wantCode int
	}{
		{"byte exact", []string{id}, out, 0},
		{"flags before id", []string{"--grep", "a$", id}, "1:alpha\n3:gamma\n4:delta\n", 0},
		{"grep last line without newline", []string{id, "--grep", "newline"}, "5:no-newline\n", 0},
		{"window", []string{id, "--from", "2", "--lines", "2"}, "beta\x00\ngamma\n", 0},
		{"from only", []string{id, "--from", "4"}, "delta\nno-newline", 0},
		{"stderr", []string{id, "--stderr"}, "oops\n", 0},
		{"all labelled", []string{id, "--all"}, "== stdout ==\n" + out + "== stderr ==\noops\n", 0},
		{"unknown id", []string{"deadbeef0000"}, "", 1},
		{"malformed id", []string{"../../etc"}, "", 1},
		{"no id", nil, "", 2},
		{"bad regex", []string{id, "--grep", "("}, "", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var o, e bytes.Buffer
			if code := runRecall(cfg, tt.args, &o, &e); code != tt.wantCode {
				t.Fatalf("code = %d (%s)", code, e.String())
			}
			if o.String() != tt.want {
				t.Fatalf("stdout = %q, want %q", o.String(), tt.want)
			}
		})
	}
}

func TestRecallPathAndMissingStderr(t *testing.T) {
	cfg, id := recallFixture(t, "x\n", "")
	var o, e bytes.Buffer
	if code := runRecall(cfg, []string{id, "--path"}, &o, &e); code != 0 || strings.TrimSpace(o.String()) != filepath.Join(cfg.RawCacheDir, id) {
		t.Fatalf("path: %d %q", code, o.String())
	}
	o.Reset()
	if code := runRecall(cfg, []string{id, "--stderr"}, &o, &e); code != 1 {
		t.Fatalf("missing stderr code = %d", code)
	}
}

func TestRecallList(t *testing.T) {
	cfg, id := recallFixture(t, "hello\n", "")
	var o, e bytes.Buffer
	if code := runRecall(cfg, []string{"--list"}, &o, &e); code != 0 {
		t.Fatalf("code = %d", code)
	}
	for _, want := range []string{id, "make", "6"} {
		if !strings.Contains(o.String(), want) {
			t.Fatalf("list missing %q:\n%s", want, o.String())
		}
	}
	if strings.Contains(o.String(), "/usr/bin") {
		t.Fatalf("list leaks a path: %s", o.String())
	}
}

func TestBypassPassesExitCodeThrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SCT__TELEMETRY_ENABLED", "false")
	t.Setenv("SCT__NO_SETUP_NUDGE", "1")
	t.Setenv("SCT__FORCE_TIER", "")
	t.Setenv("SCT__RAW_CACHE_ENABLED", "false")
	if code := realMain([]string{"bypass", "sh", "-c", "exit 7"}); code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
	if code := realMain([]string{"bypass"}); code != 2 {
		t.Fatalf("bare bypass code = %d, want 2", code)
	}
}
