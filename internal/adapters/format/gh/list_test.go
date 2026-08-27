package gh

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveNativeLists(t *testing.T) {
	f := New()
	tests := []struct {
		name, raw, want string
		argv            []string
	}{
		{
			name: "pull requests",
			argv: []string{"gh", "pr", "list"},
			raw: "14148\tUpdate glamour to v2\theaths:issue3718\tDRAFT\t2026-08-15T00:09:53Z\n" +
				"14136\tAdd worktree checkout to gh issue develop\tfeature/worktree\tOPEN\t2026-08-13T10:26:41Z\n" +
				"14130\tSupport additional repository selectors\tfeature/selectors\tOPEN\t2026-08-12T09:20:11Z\n" +
				"14122\tAvoid duplicate API requests in status\tfix/status\tOPEN\t2026-08-11T08:19:10Z\n" +
				"14118\tDocument extension authentication behavior\tdocs/auth\tDRAFT\t2026-08-10T07:18:09Z\n",
			want: "#14148 DRAFT Update glamour to v2 [heaths:issue3718]",
		},
		{
			name: "issues",
			argv: []string{"gh", "issue", "list"},
			raw: "14151\tOPEN\t3x-ui\tneeds-triage\t2026-08-15T02:25:48Z\n" +
				"14145\tOPEN\tLs\tneeds-triage, bug\t2026-08-14T04:09:36Z\n" +
				"14140\tOPEN\tImprove authentication error details\tneeds-triage\t2026-08-13T03:08:35Z\n" +
				"14133\tOPEN\tStatus output is difficult to scan\tbug\t2026-08-12T02:07:34Z\n" +
				"14125\tOPEN\tRepository list should retain visibility\tenhancement\t2026-08-11T01:06:33Z\n",
			want: "#14145 OPEN Ls [needs-triage, bug]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := f.Aggressive(context.Background(), format.Input{Argv: tt.argv, Stdout: strings.NewReader(tt.raw)})
			if err != nil {
				t.Fatalf("Aggressive() error = %v", err)
			}
			if !strings.Contains(string(out.Body), tt.want) {
				t.Fatalf("body missing %q: %q", tt.want, out.Body)
			}
		})
	}
}

func TestAggressiveReleaseListCapsLargeResult(t *testing.T) {
	f := New()
	var raw strings.Builder
	for i := range listCap + 5 {
		fmt.Fprintf(&raw, "Release %d\t\tv2.%d.0\t2026-07-%02dT02:04:00Z\n", i, i, i%28+1)
	}
	out, err := f.Aggressive(context.Background(), format.Input{Argv: []string{"gh", "release", "list", "--limit", "35"}, Stdout: strings.NewReader(raw.String())})
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if !strings.Contains(string(out.Body), "…+5 more rows") {
		t.Fatalf("missing release elision: %q", out.Body)
	}
}

func TestAggressiveListEmptyAndUnexpectedDecline(t *testing.T) {
	f := New()
	for _, raw := range []string{"", "not a native table row\n"} {
		in := format.Input{Argv: []string{"gh", "pr", "list"}, Stdout: strings.NewReader(raw)}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("raw %q error = %v, want ErrTierInapplicable", raw, err)
		}
	}
}
