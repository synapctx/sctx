package ghargv

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		argv   []string
		l1, l2 string
		ok     bool
	}{
		{[]string{"gh", "pr", "list"}, "pr", "list", true},
		{[]string{"gh", "-R", "cli/cli", "pr", "list"}, "pr", "list", true},
		{[]string{"gh", "pr", "-Rcli/cli", "list"}, "pr", "list", true},
		{[]string{"gh", "--repo=cli/cli", "issue", "view", "1"}, "issue", "view", true},
		{[]string{"gh", "api", "repos/cli/cli"}, "api", "", true},
		{[]string{"gh", "--unknown", "value", "pr", "list"}, "", "", false},
		{[]string{"gh", "-R"}, "", "", false},
	}
	for _, tt := range tests {
		inv, ok := Parse(tt.argv)
		if ok != tt.ok || inv.Level1 != tt.l1 || inv.Level2 != tt.l2 {
			t.Errorf("Parse(%v) = (%+v, %v), want (%q, %q, %v)", tt.argv, inv, ok, tt.l1, tt.l2, tt.ok)
		}
	}
}

func TestSafeReadOnly(t *testing.T) {
	tests := []struct {
		argv []string
		want bool
	}{
		{[]string{"gh", "pr", "list"}, true},
		{[]string{"gh", "pr", "checks", "1", "--watch"}, false},
		{[]string{"gh", "run", "watch", "1"}, false},
		{[]string{"gh", "run", "view", "1", "--log-failed"}, true},
		{[]string{"gh", "repo", "view", "--web"}, false},
		{[]string{"gh", "api", "repos/cli/cli"}, true},
		{[]string{"gh", "api", "graphql", "--input", "-"}, false},
		{[]string{"gh", "api", "repos/cli/cli/issues", "-f", "title=x"}, false},
		{[]string{"gh", "api", "repos/cli/cli/issues", "--method", "POST"}, false},
		{[]string{"gh", "api", "repos/cli/cli/issues", "-XPOST"}, false},
		{[]string{"gh", "api", "repos/cli/cli/issues", "-ftitle=x"}, false},
		{[]string{"gh", "api", "search/issues", "--method", "GET", "-f", "q=x"}, true},
		{[]string{"gh", "search", "prs", "--repo", "cli/cli"}, true},
		{[]string{"gh", "search", "prs", "-w"}, false},
		{[]string{"gh", "workflow", "view"}, false},
		{[]string{"gh", "workflow", "view", "go.yml", "-R", "cli/cli"}, true},
		{[]string{"gh", "gist", "view"}, false},
		{[]string{"gh", "gist", "view", "abc"}, true},
		{[]string{"gh", "project", "item-list", "1", "--owner", "cli"}, true},
		{[]string{"gh", "project", "item-add", "1"}, false},
		{[]string{"gh", "pr", "create"}, false},
	}
	for _, tt := range tests {
		inv, ok := Parse(tt.argv)
		if !ok {
			t.Fatalf("Parse(%v) failed", tt.argv)
		}
		if got := SafeReadOnly(inv); got != tt.want {
			t.Errorf("SafeReadOnly(%v) = %v, want %v", tt.argv, got, tt.want)
		}
	}
}
