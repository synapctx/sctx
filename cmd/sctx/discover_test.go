package main

import "testing"

func TestClaudeProjectDirName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/Users/a/src/example.com/org/repo", "-Users-a-src-example-com-org-repo"},
		{"/x/.claude/worktrees/agent-1", "-x--claude-worktrees-agent-1"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := claudeProjectDirName(tc.in); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestParseDiscoverArgs(t *testing.T) {
	f, err := parseDiscoverArgs([]string{"--all", "--since", "7d", "--format", "json"})
	if err != nil || !f.all || f.format != "json" || f.since.IsZero() {
		t.Fatalf("got %+v err %v", f, err)
	}
	for _, bad := range [][]string{{"--bogus"}, {"--since"}, {"--format", "xml"}} {
		if _, err := parseDiscoverArgs(bad); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}
