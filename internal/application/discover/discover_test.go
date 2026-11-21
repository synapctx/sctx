package discover

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func classify(cmd string) (Kind, string) {
	switch {
	case strings.HasPrefix(cmd, "go test"):
		return KindWrap, "go test"
	case strings.HasPrefix(cmd, "terraform plan"):
		return KindGap, "terraform plan"
	case strings.HasPrefix(cmd, "weirdtool"):
		return KindGap, "weirdtool"
	}
	return KindSkip, ""
}

func use(id, cmd string) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":"2026-11-20T10:00:00Z","sessionId":"s1","message":{"content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":%q}}]}}`, id, cmd)
}

func result(id, text string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":"2026-11-20T10:00:01Z","sessionId":"s1","message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":%q}]}}`, id, text)
}

func resultParts(id, text string) string {
	return fmt.Sprintf(`{"type":"user","sessionId":"s1","message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":[{"type":"text","text":%q}]}]}}`, id, text)
}

func write(t *testing.T, dir, name string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScan(t *testing.T) {
	big := strings.Repeat("x", 4000)
	tests := []struct {
		name       string
		lines      []string
		wrapped    map[string]map[string]int64
		wantMissed int64
		wantBytes  int64
		wantUncov  int64
		wantWrap   int64
		wantCmds   int64
	}{
		{
			name:       "missed and uncovered",
			lines:      []string{use("a", "go test ./..."), result("a", big), use("b", "weirdtool --secret-host db.internal"), resultParts("b", big), use("c", "echo hi"), result("c", "hi")},
			wantMissed: 1, wantBytes: 4000, wantUncov: 1, wantCmds: 3,
		},
		{
			name:       "hook wrapped runs are discounted",
			lines:      []string{use("a", "go test ./..."), result("a", big), use("b", "go test ./x"), result("b", big)},
			wrapped:    map[string]map[string]int64{"s1": {"go test": 1}},
			wantMissed: 1, wantBytes: 4000, wantWrap: 1, wantCmds: 2,
		},
		{
			name:       "malformed lines, api-request-blob duplicates and non-bash tools are ignored",
			lines:      []string{"{not json tool_use", `{"type":"api-request-blob","message":{"content":[{"type":"tool_use","id":"z","name":"Bash","input":{"command":"go test"}}]}}`, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r","name":"Read","input":{}}]}}`, use("a", "go test ./..."), result("a", "ok")},
			wantMissed: 1, wantBytes: 2, wantCmds: 1,
		},
		{
			name:       "duplicate tool_use id counts once; result-less command counts with unknown size",
			lines:      []string{use("a", "go test"), use("a", "go test"), result("a", big), use("n", "go test ./y")},
			wantMissed: 2, wantBytes: 4000, wantCmds: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "sess.jsonl", tc.lines...)
			rep, err := Scan(context.Background(), Options{Dirs: []string{dir}, Classify: classify, Wrapped: tc.wrapped})
			if err != nil {
				t.Fatal(err)
			}
			if rep.MissedCount != tc.wantMissed || rep.UncoveredCount != tc.wantUncov ||
				rep.AlreadyWrapped != tc.wantWrap || rep.BashCommands != tc.wantCmds {
				t.Fatalf("got missed=%d uncov=%d wrapped=%d cmds=%d", rep.MissedCount, rep.UncoveredCount, rep.AlreadyWrapped, rep.BashCommands)
			}
			var b int64
			for _, r := range rep.Missed {
				b += r.OutputBytes
			}
			if b != tc.wantBytes {
				t.Fatalf("missed bytes = %d, want %d", b, tc.wantBytes)
			}
		})
	}
}

func TestScanSinceFiltersByTimestamp(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "s.jsonl", use("a", "go test"), result("a", "ok"))
	since := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	rep, err := Scan(context.Background(), Options{Dirs: []string{dir}, Classify: classify, Since: since})
	if err != nil {
		t.Fatal(err)
	}
	if rep.BashCommands != 0 {
		t.Fatalf("old command counted: %d", rep.BashCommands)
	}
}

func TestScanOversizedLineIsSkippedNotFatal(t *testing.T) {
	dir := t.TempDir()
	huge := `{"type":"user","x":"` + strings.Repeat("a", maxLine+10) + `"}`
	write(t, dir, "s.jsonl", huge, use("a", "go test"), result("a", "ok"))
	rep, err := Scan(context.Background(), Options{Dirs: []string{dir}, Classify: classify})
	if err != nil {
		t.Fatal(err)
	}
	if rep.SkippedLines != 1 || rep.MissedCount != 1 {
		t.Fatalf("skipped=%d missed=%d", rep.SkippedLines, rep.MissedCount)
	}
}

func TestApplySavings(t *testing.T) {
	r := Report{Missed: []Row{{Key: "go test", EstTokens: 1000}, {Key: "other", EstTokens: 1000}}}
	r.ApplySavings(map[string]float64{"go test": 0.8}, 0.5)
	if r.Missed[0].EstSavedTokens != 800 || r.Missed[0].Estimated {
		t.Fatalf("row0 = %+v", r.Missed[0])
	}
	if r.Missed[1].EstSavedTokens != 500 || !r.Missed[1].Estimated {
		t.Fatalf("row1 = %+v", r.Missed[1])
	}
	if r.MissedSaved != 1300 {
		t.Fatalf("total = %d", r.MissedSaved)
	}
}
