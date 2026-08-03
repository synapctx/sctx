package gh

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveListPRs(t *testing.T) {
	f := New()
	stdout := strings.Join([]string{
		"101\tFix crash in parser\tfix-parser\tOPEN",
		"102\tAdd retry logic to the network client so it survives flaky intermittent connections gracefully\tadd-retry\tOPEN",
		"103\tUpdate deps\tdeps\tDRAFT",
		"104\tRefactor storage layer\trefactor-storage\tOPEN",
		"105\tBump go version\tbump-go\tMERGED",
	}, "\n")
	in := format.Input{
		Argv:   []string{"gh", "pr", "list"},
		Stdout: strings.NewReader(stdout),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.HasPrefix(body, "5 pull requests\n") {
		t.Errorf("missing summary line: %q", body)
	}
	if !strings.Contains(body, "#101 OPEN Fix crash in parser") {
		t.Errorf("missing row: %q", body)
	}
	if strings.Contains(body, "gracefully") {
		t.Errorf("title not truncated: %q", body)
	}
}

func TestAggressiveListIssues(t *testing.T) {
	f := New()
	stdout := "5\tCrash on startup\tbug\n6\tFeature request\tenhancement\n"
	in := format.Input{
		Argv:   []string{"gh", "issue", "list"},
		Stdout: strings.NewReader(stdout),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if !strings.HasPrefix(string(out.Body), "2 issues\n") {
		t.Errorf("missing summary line: %q", out.Body)
	}
}

func TestAggressiveListEmptyPassthrough(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"gh", "pr", "list"},
		Stdout: strings.NewReader("no pull requests match your search in owner/repo\n"),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if string(out.Body) != "no pull requests match your search in owner/repo" {
		t.Errorf("body = %q, want verbatim passthrough", out.Body)
	}
}
