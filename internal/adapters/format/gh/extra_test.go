package gh

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveSearchNativeShapes(t *testing.T) {
	tests := []struct {
		argv []string
		raw  string
		want string
	}{
		{
			[]string{"gh", "search", "prs", "--repo", "cli/cli"},
			"cli/cli\t14157\topen\tdocs: warn about auth token output\texternal, ready-for-review\t2026-08-15T17:41:27Z\n" +
				"cli/cli\t14155\topen\ttest: stabilize Windows development checks\tneeds-triage, external\t2026-08-15T15:41:01Z\n" +
				"cli/cli\t14154\topen\tfix skills install path\texternal\t2026-08-15T15:30:24Z\n" +
				"cli/cli\t14148\topen\tUpdate glamour to v2\tneeds-triage\t2026-08-15T05:12:26Z\n" +
				"cli/cli\t14147\tmerged\tchore deps update\tdependencies\t2026-08-14T14:40:42Z\n",
			"cli/cli#14157 open docs: warn about auth token output",
		},
		{
			[]string{"gh", "search", "commits", "fix", "--repo", "cli/cli"},
			"cli/cli\t78de8634435243cb2db1735a4874dce7fdfb5c98\tFix project item-add output for non-TTY\tbabakks\t2026-08-10T10:07:04+01:00\n" +
				"cli/cli\te83adbc0642994fae7c39a9a012eb34b8c81f4f1\tFix RESTWithNext error type\twilliammartin\t2026-08-01T09:52:46+02:00\n" +
				"cli/cli\ta6bcd08d07d1cbcb17cfce497c0cf261a966f703\tFix item-add output\tzwick\t2026-08-03T13:41:03-04:00\n" +
				"cli/cli\t0492fd1135a54ef2d67b507d5dabfe39f5e65d12\tFix typos\tpstoeckle\t2026-07-22T12:21:10+02:00\n" +
				"cli/cli\tc2ad3b0eb7ead66eec3a8a61239e10a765332ec7\tFix picker wrapping\ttommaso-moro\t2026-07-30T14:43:52+01:00\n",
			"cli/cli 78de86344352 Fix project item-add output for non-TTY — babakks",
		},
	}
	for _, tt := range tests {
		out, err := New().Aggressive(context.Background(), format.Input{Argv: tt.argv, Stdout: strings.NewReader(tt.raw)})
		if err != nil {
			t.Fatalf("Aggressive(%v) error = %v", tt.argv, err)
		}
		if got := string(out.Body); !strings.Contains(got, tt.want) || !strings.Contains(got, "omitted") {
			t.Fatalf("Aggressive(%v) = %q", tt.argv, got)
		}
		if tt.argv[2] == "commits" && !strings.Contains(string(out.Body), "SHAs shortened to 12 chars ×5") {
			t.Fatalf("commit SHA elision was not marked: %q", out.Body)
		}
	}
}

func TestAggressiveWorkflowAndCacheLists(t *testing.T) {
	workflow := "Unit and Integration Tests\tactive\t25016\nLint\tactive\t925645\nCode Scanning\tactive\t1208059\nDeployment\tactive\t54774190\nDependabot Updates\tactive\t132478870\nBump Go\tactive\t171817935\nGo Vulnerability Check\tactive\t176555307\nCopilot cloud agent\tactive\t182497967\nCopilot code review\tactive\t201707557\nDependency Graph\tactive\t214673364\n"
	out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"gh", "workflow", "list"}, Stdout: strings.NewReader(workflow)})
	if err != nil || !strings.Contains(string(out.Body), "states omitted ×10") {
		t.Fatalf("workflow render = %q, error %v", out.Body, err)
	}

	cache := "6654840402\tagentic-workflow-usage-dependabottriage-31807740215\t855 B\t2026-08-14T20:06:22Z\t2026-08-15T19:46:03Z\n" +
		"5625479776\tagentic-workflow-usage-issuetriage-29052874144\t821 B\t2026-07-09T21:58:12Z\t2026-08-15T19:13:54Z\n"
	out, err = New().Aggressive(context.Background(), format.Input{Argv: []string{"gh", "cache", "list"}, Stdout: strings.NewReader(cache)})
	if err != nil || !strings.Contains(string(out.Body), "timestamps omitted ×4") {
		t.Fatalf("cache render = %q, error %v", out.Body, err)
	}
}

func TestLargeGistAndProjectOutputsUseCountedCaps(t *testing.T) {
	var gist strings.Builder
	for i := 0; i < 205; i++ {
		fmt.Fprintf(&gist, "gist content line %03d with enough text to matter\n", i)
	}
	out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"gh", "gist", "view", "abc"}, Stdout: strings.NewReader(gist.String())})
	if err != nil || !strings.Contains(string(out.Body), "…+5 more gist lines") {
		t.Fatalf("gist render = %q, error %v", out.Body, err)
	}

	var project strings.Builder
	for i := 0; i < 35; i++ {
		fmt.Fprintf(&project, "Issue\tTitle %d\t%d\towner/repo\tPVTI_%d\n", i, i, i)
	}
	out, err = New().Aggressive(context.Background(), format.Input{Argv: []string{"gh", "project", "item-list", "1"}, Stdout: strings.NewReader(project.String())})
	if err != nil || !strings.Contains(string(out.Body), "…+5 more project rows") {
		t.Fatalf("project render = %q, error %v", out.Body, err)
	}
}

func TestAuthoritativeExtraOutputsDecline(t *testing.T) {
	for _, argv := range [][]string{
		{"gh", "gist", "view", "abc", "--raw"},
		{"gh", "gist", "view", "abc", "--allow-escape-sequences"},
		{"gh", "workflow", "view", "go.yml", "--yaml"},
		{"gh", "search", "prs", "--json", "number", "--jq", ".[]"},
	} {
		for name, call := range map[string]func(context.Context, format.Input) (format.Rendered, error){"aggressive": New().Aggressive, "relaxed": New().Relaxed} {
			_, err := call(context.Background(), format.Input{Argv: argv, Stdout: strings.NewReader("content\ncontent\ncontent\n")})
			if err != format.ErrTierInapplicable {
				t.Errorf("%s(%v) error = %v", name, argv, err)
			}
		}
	}
}

func TestProjectFormatJSONCompacts(t *testing.T) {
	raw := "{\n  \"projects\": [\n    {\"number\": 1, \"title\": \"Roadmap\"}\n  ]\n}\n"
	out, err := New().Aggressive(context.Background(), format.Input{Argv: []string{"gh", "project", "list", "--format", "json"}, Stdout: strings.NewReader(raw)})
	if err != nil || strings.Contains(string(out.Body), "\n") {
		t.Fatalf("project JSON render = %q, error %v", out.Body, err)
	}
}
