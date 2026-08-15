package gh

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveRunListNativeColumns(t *testing.T) {
	f := New()
	raw := strings.Join([]string{
		"completed\tfailure\tDependabot PR Triage\tDependabot PR Triage\ttrunk\tschedule\t31880402035\t28s\t2026-08-15T10:47:26Z",
		"completed\tsuccess\tIssue title\tTriage Scheduled Tasks\ttrunk\tissue_comment\t31880309237\t10s\t2026-08-15T10:45:13Z",
		"in_progress\t\tCI\tCI\tfeature\tpush\t31880300000\t30s\t2026-08-15T10:44:13Z",
	}, "\n") + "\n"
	out, err := f.Aggressive(context.Background(), format.Input{Argv: []string{"gh", "run", "list"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	for _, want := range []string{"3 runs (1 failed, 1 active)", "failure Dependabot PR Triage [trunk schedule] id=31880402035 28s", "success Triage Scheduled Tasks: Issue title", "in_progress CI"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %q", want, body)
		}
	}
}

func TestAggressiveRunViewCollapsesOnlySuccessfulSteps(t *testing.T) {
	f := New()
	raw := "X trunk CI · 123\n\nJOBS\nX test in 2m\n" +
		"  ✓ Set up job\n  ✓ Checkout\n  ✓ Set up Go\n  ✓ Build\n  ✓ Vet\n" +
		"  X Test\n\nANNOTATIONS\nX Process completed with exit code 1.\n"
	out, err := f.Aggressive(context.Background(), format.Input{Argv: []string{"gh", "run", "view", "123"}, Stdout: strings.NewReader(raw)})
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "…+5 successful steps") || !strings.Contains(body, "  X Test") || !strings.Contains(body, "exit code 1") {
		t.Fatalf("run view lost failure signal: %q", body)
	}

	logInput := format.Input{Argv: []string{"gh", "run", "view", "123", "--log-failed"}, Stdout: strings.NewReader("job\tstep\tline\n")}
	if _, err := f.Aggressive(context.Background(), logInput); err != format.ErrTierInapplicable {
		t.Fatalf("log Aggressive() error = %v, want ErrTierInapplicable", err)
	}
}
