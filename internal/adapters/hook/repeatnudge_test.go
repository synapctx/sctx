package hook

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/synapctx/sctx/internal/adapters/stats/sqlite"
	"github.com/synapctx/sctx/internal/domain/stats"
	"github.com/synapctx/sctx/internal/platform/config"
)

// seedIdenticalRuns records n runs for sessionID/argv, all with the same
// rawBytes, so repeatedRunNudge's stats.db queries have something to count.
func seedIdenticalRuns(t *testing.T, dbPath, sessionID, argv string, rawBytes int64, n int) {
	t.Helper()
	store, err := sqlite.NewStore(dbPath)
	if err != nil {
		t.Fatalf("sqlite.NewStore: %v", err)
	}
	defer store.Close()
	for i := 0; i < n; i++ {
		r := stats.Run{
			ID:        fmt.Sprintf("01SEED-%s-%s-%d-%d", sessionID, argv, rawBytes, i),
			At:        time.Now().UTC().Add(time.Duration(i) * time.Second),
			Command:   strings.Fields(argv)[0],
			Argv:      argv,
			Formatter: "verbatim",
			Tier:      "verbatim",
			RawBytes:  rawBytes,
			SessionID: sessionID,
		}
		if err := store.Record(context.Background(), r); err != nil {
			t.Fatalf("seeding run %d: %v", i, err)
		}
	}
}

func testConfigWithStatsDB(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SCT__SPOOL_DIR", t.TempDir())
	return config.Config{StatsDBPath: filepath.Join(dir, "stats.db")}
}

// TestRepeatedRunNudgeThreshold is the table test for the count boundary:
// silent below repeatedRunThreshold, fires at and above it.
func TestRepeatedRunNudgeThreshold(t *testing.T) {
	tests := []struct {
		name    string
		seeded  int
		wantHit bool
	}{
		{name: "one run: far below threshold", seeded: 1, wantHit: false},
		{name: "one below threshold", seeded: repeatedRunThreshold - 1, wantHit: false},
		{name: "exactly at threshold", seeded: repeatedRunThreshold, wantHit: true},
		{name: "above threshold", seeded: repeatedRunThreshold + 1, wantHit: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfigWithStatsDB(t)
			seedIdenticalRuns(t, cfg.StatsDBPath, "sess1", "go vet ./...", 1234, tc.seeded)

			got := repeatedRunNudge(cfg, "sess1", "sctx go vet ./...")
			if hit := got != ""; hit != tc.wantHit {
				t.Errorf("repeatedRunNudge with %d seeded runs = %q, wantHit=%v", tc.seeded, got, tc.wantHit)
			}
		})
	}
}

// TestRepeatedRunNudgeAllowlist is the table test for commands that must
// never nudge regardless of how many times they repeat.
func TestRepeatedRunNudgeAllowlist(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
	}{
		{name: "git status", cmd: "git status"},
		{name: "git log", cmd: "git log"},
		{name: "git diff", cmd: "git diff"},
		{name: "kubectl get", cmd: "kubectl get pods"},
		{name: "ls", cmd: "ls -la"},
		{name: "cat", cmd: "cat README.md"},
		{name: "head", cmd: "head -50 file.txt"},
		{name: "tail", cmd: "tail -f log.txt"},
		{name: "absolute path git", cmd: "/usr/bin/git status"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfigWithStatsDB(t)
			seedIdenticalRuns(t, cfg.StatsDBPath, "sess1", tc.cmd, 999, repeatedRunThreshold+2)

			if got := repeatedRunNudge(cfg, "sess1", tc.cmd); got != "" {
				t.Errorf("repeatedRunNudge(%q) = %q, want silence (allowlisted)", tc.cmd, got)
			}
		})
	}
}

// TestRepeatedRunNudgeNotAllowlisted proves the allowlist is scoped to the
// exact (program, subcommand) pairs above, not to every git/kubectl call —
// `git push` repeatedly succeeding at the same byte count IS worth a nudge.
func TestRepeatedRunNudgeNotAllowlisted(t *testing.T) {
	cfg := testConfigWithStatsDB(t)
	seedIdenticalRuns(t, cfg.StatsDBPath, "sess1", "git push", 500, repeatedRunThreshold)

	if got := repeatedRunNudge(cfg, "sess1", "git push"); got == "" {
		t.Error("repeatedRunNudge(\"git push\") = \"\", want a nudge — git push is not on the allowlist")
	}
}

// TestRepeatedRunNudgeRateLimit covers both halves of the rate limit: at
// most once per (session, argv), and at most maxNudgesPerSession nudges
// total per session.
func TestRepeatedRunNudgeRateLimit(t *testing.T) {
	t.Run("same argv nudges at most once", func(t *testing.T) {
		cfg := testConfigWithStatsDB(t)
		seedIdenticalRuns(t, cfg.StatsDBPath, "sess1", "go vet ./...", 1000, repeatedRunThreshold)

		first := repeatedRunNudge(cfg, "sess1", "go vet ./...")
		if first == "" {
			t.Fatal("first call: want a nudge")
		}
		second := repeatedRunNudge(cfg, "sess1", "go vet ./...")
		if second != "" {
			t.Errorf("second call for the SAME argv = %q, want silence", second)
		}
	})

	t.Run("at most maxNudgesPerSession distinct nudges", func(t *testing.T) {
		cfg := testConfigWithStatsDB(t)
		commands := []string{"go vet ./...", "go build ./...", "go test ./...", "go generate ./..."}
		if len(commands) <= maxNudgesPerSession {
			t.Fatalf("test needs more distinct commands than maxNudgesPerSession (%d)", maxNudgesPerSession)
		}
		for i, cmd := range commands {
			seedIdenticalRuns(t, cfg.StatsDBPath, "sess1", cmd, int64(100+i), repeatedRunThreshold)
		}

		nudges := 0
		for _, cmd := range commands {
			if repeatedRunNudge(cfg, "sess1", cmd) != "" {
				nudges++
			}
		}
		if nudges != maxNudgesPerSession {
			t.Errorf("nudges fired = %d across %d qualifying commands, want exactly maxNudgesPerSession=%d", nudges, len(commands), maxNudgesPerSession)
		}
	})
}

// TestRepeatedRunNudgeUnknownSessionIsSilent proves an empty or unsanitizable
// session id never nudges — with no session there is no (session, argv) key
// to rate-limit against, so speaking would be unbounded.
func TestRepeatedRunNudgeUnknownSessionIsSilent(t *testing.T) {
	cfg := testConfigWithStatsDB(t)
	// Seed under an EMPTY session id directly, in case a bug ever let "" reach
	// the store — the guard must be in repeatedRunNudge itself, not merely a
	// side effect of nothing being seeded for "".
	seedIdenticalRuns(t, cfg.StatsDBPath, "", "go vet ./...", 1234, repeatedRunThreshold+5)

	if got := repeatedRunNudge(cfg, "", "go vet ./..."); got != "" {
		t.Errorf("repeatedRunNudge with an empty session id = %q, want silence", got)
	}
}

func TestNormalizeSessionArgvStripsTheSctxRewritePrefix(t *testing.T) {
	tests := []struct{ cmd, want string }{
		{"go vet ./...", "go vet ./..."},
		{"sctx go vet ./...", "go vet ./..."},
		{"/Users/x/.local/bin/sctx go test ./...", "go test ./..."},
		{"", ""},
		{"   ", ""},
	}
	for _, tc := range tests {
		if got := normalizeSessionArgv(tc.cmd); got != tc.want {
			t.Errorf("normalizeSessionArgv(%q) = %q, want %q", tc.cmd, got, tc.want)
		}
	}
}
