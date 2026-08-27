package gh

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveViewCurrentNativeShape(t *testing.T) {
	f := New()
	var body strings.Builder
	for i := 1; i <= 45; i++ {
		fmt.Fprintf(&body, "body line %02d\n", i)
	}
	raw := "title:\tUpdate glamour to v2\n" +
		"state:\tDRAFT\n" +
		"author:\theaths\n" +
		"labels:\texternal, needs-triage\n" +
		"assignees:\t\n" +
		"reviewers:\tCopilot (AI)\n" +
		"projects:\t\n" +
		"milestone:\t\n" +
		"number:\t14148\n" +
		"url:\thttps://github.com/cli/cli/pull/14148\n" +
		"additions:\t36\n" +
		"deletions:\t40\n" +
		"auto-merge:\tdisabled\n--\n" + body.String()

	out, err := f.Aggressive(context.Background(), format.Input{
		Argv: []string{"gh", "pr", "view", "14148"}, Stdout: strings.NewReader(raw),
	})
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	got := string(out.Body)
	for _, want := range []string{"title:\tUpdate glamour to v2", "…+3 empty metadata fields", "body line 40", "…+5 body lines"} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "body line 41") {
		t.Errorf("body cap was not applied: %q", got)
	}
}

func TestAggressiveReleaseViewCountsAssets(t *testing.T) {
	var raw strings.Builder
	raw.WriteString("title:\tGitHub CLI 2.97.0\ntag:\tv2.97.0\ndraft:\tfalse\nprerelease:\tfalse\n")
	for i := range 15 {
		fmt.Fprintf(&raw, "asset:\tgh_2.97.0_%02d.tar.gz\n", i)
	}
	raw.WriteString("--\nRelease notes.\n")
	out, err := New().Aggressive(context.Background(), format.Input{
		Argv: []string{"gh", "release", "view", "v2.97.0"}, Stdout: strings.NewReader(raw.String()),
	})
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if got := string(out.Body); !strings.Contains(got, "…+3 more assets") {
		t.Fatalf("missing counted asset elision: %q", got)
	}
}

func TestAggressiveViewUnexpectedShapeDeclines(t *testing.T) {
	for _, raw := range []string{"", "Title first\nOpen • old presentation format\n"} {
		_, err := New().Aggressive(context.Background(), format.Input{
			Argv: []string{"gh", "pr", "view", "1"}, Stdout: strings.NewReader(raw),
		})
		if err != format.ErrTierInapplicable {
			t.Errorf("raw %q error = %v, want ErrTierInapplicable", raw, err)
		}
	}
}
