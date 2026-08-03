package gh

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAggressiveViewLongBody(t *testing.T) {
	f := New()
	var bodyLines []string
	for i := 1; i <= 40; i++ {
		bodyLines = append(bodyLines, "body line")
		_ = i
	}
	stdout := "Fix crash in parser #123\n" +
		"Open • octocat wants to merge 3 commits into main from fix-branch\n" +
		"Labels: bug, urgent\n" +
		"\n" +
		strings.Join(bodyLines, "\n") + "\n" +
		"\n" +
		"View this pull request on GitHub: https://github.com/owner/repo/pull/123\n"

	in := format.Input{
		Argv:   []string{"gh", "pr", "view", "123"},
		Stdout: strings.NewReader(stdout),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.HasPrefix(body, "Fix crash in parser #123") {
		t.Errorf("missing title: %q", body)
	}
	if !strings.Contains(body, "…+15 lines") {
		t.Errorf("missing truncation marker: %q", body)
	}
	if !strings.Contains(body, "View this pull request on GitHub") {
		t.Errorf("missing footer: %q", body)
	}
}

func TestAggressiveViewWithComments(t *testing.T) {
	f := New()
	stdout := "Add feature X #42\n" +
		"Open • octocat wants to merge 1 commit into main from feature-x\n" +
		"\n" +
		"Summary of the change.\n" +
		"\n" +
		"Comments\n" +
		"--------\n" +
		"user1 commented: looks good\n" +
		"user2 commented: needs a test\n" +
		"\n" +
		"View this pull request on GitHub: https://github.com/owner/repo/pull/42\n"

	in := format.Input{
		Argv:   []string{"gh", "pr", "view", "42"},
		Stdout: strings.NewReader(stdout),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "…+2 comments") {
		t.Errorf("missing comments marker: %q", body)
	}
	if strings.Contains(body, "user1 commented") {
		t.Errorf("comment thread not dropped: %q", body)
	}
}
