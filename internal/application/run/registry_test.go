package run

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

type stubFormatter struct{ match format.Match }

func (s *stubFormatter) Descriptor() format.Match { return s.match }
func (s *stubFormatter) Aggressive(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
func (s *stubFormatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}

func TestResolveByArgv(t *testing.T) {
	goFmt := &stubFormatter{match: format.Match{Command: "go"}}
	goTestFmt := &stubFormatter{match: format.Match{Command: "go", Subcommands: []string{"test"}}}
	gitFmt := &stubFormatter{match: format.Match{Command: "git"}}

	r := NewRegistry()
	r.Register(goFmt)
	r.Register(goTestFmt)
	r.Register(gitFmt)

	tests := []struct {
		name  string
		argv  []string
		want  format.Formatter
		found bool
	}{
		{"program only", []string{"git", "status"}, gitFmt, true},
		{"longest subcommand wins", []string{"go", "test", "./..."}, goTestFmt, true},
		{"subcommand after flags", []string{"go", "-count=1", "test"}, goTestFmt, true},
		{"other go subcommand falls to program match", []string{"go", "build", "./..."}, goFmt, true},
		{"absolute path program", []string{"/usr/local/bin/git", "log"}, gitFmt, true},
		{"env assignment prefix", []string{"CGO_ENABLED=0", "go", "test"}, goTestFmt, true},
		{"unknown program", []string{"cargo", "test"}, nil, false},
		{"empty argv", nil, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := r.ResolveByArgv(tt.argv)
			if found != tt.found {
				t.Fatalf("found = %t, want %t", found, tt.found)
			}
			if got != tt.want {
				t.Fatalf("formatter = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProjectFormatterCannotShadowBuiltinWithoutExplicitOverride(t *testing.T) {
	r := NewRegistry()
	builtin := &stubFormatter{match: format.Match{Command: "make"}}
	local := &stubFormatter{match: format.Match{Command: "make", Subcommands: []string{"lint"}}}
	r.Register(builtin)
	r.RegisterProject(local, false)
	if got, _ := r.ResolveByArgv([]string{"make", "lint"}); got != builtin {
		t.Fatal("unprivileged project formatter shadowed built-in")
	}

	r = NewRegistry()
	r.Register(builtin)
	r.RegisterProject(local, true)
	if got, _ := r.ResolveByArgv([]string{"make", "lint"}); got != local {
		t.Fatal("explicit trusted override did not select project formatter")
	}
}

type exactProjectFormatter struct {
	*stubFormatter
	want []string
}

func (f *exactProjectFormatter) MatchesArgv(argv []string) bool {
	return strings.Join(argv, "\x00") == strings.Join(f.want, "\x00")
}
func (f *exactProjectFormatter) MatchSpecificity() int { return len(f.want) - 1 }

func TestProjectFormatterUsesExactArgvMatcherIncludingFlags(t *testing.T) {
	r := NewRegistry()
	local := &exactProjectFormatter{
		stubFormatter: &stubFormatter{match: format.Match{Command: "check"}},
		want:          []string{"./scripts/check", "--ci"},
	}
	r.RegisterProject(local, false)
	if got, found := r.ResolveByArgv([]string{"./scripts/check", "--ci"}); !found || got != local {
		t.Fatal("exact project argv did not resolve")
	}
	if _, found := r.ResolveByArgv([]string{"./scripts/check", "--local"}); found {
		t.Fatal("non-matching project argv resolved")
	}
	if _, found := r.ResolveBuiltInByArgv([]string{"./scripts/check", "--ci"}); found {
		t.Fatal("project formatter leaked into nested built-in resolver")
	}
}

func TestCommandKey(t *testing.T) {
	tests := []struct {
		argv []string
		want string
	}{
		{[]string{"go", "test", "./..."}, "go test"},
		{[]string{"git", "-C", "x", "status"}, "git status"},
		{[]string{"git", "--no-pager", "--git-dir=/tmp/repo.git", "--work-tree", "/tmp/repo", "--namespace", "tenant", "status"}, "git status"},
		{[]string{"git", "--unknown", "value", "status"}, "git"},
		{[]string{"gh", "-R", "cli/cli", "pr", "checks", "12"}, "gh pr checks"},
		{[]string{"gh", "api", "repos/cli/cli"}, "gh api"},
		{[]string{"gh", "search", "prs", "--repo", "cli/cli"}, "gh search prs"},
		{[]string{"gh", "workflow", "list", "-R", "cli/cli"}, "gh workflow list"},
		{[]string{"gh", "project", "item-list", "1", "--owner", "cli"}, "gh project item-list"},
		{[]string{"kubectl", "--context", "dev", "--request-timeout", "5s", "-n", "ns", "get", "pods"}, "kubectl get"},
		{[]string{"kubectl", "--warnings-as-errors", "exec", "pod/x", "--", "go", "test"}, "kubectl exec"},
		{[]string{"kubectl", "--unknown", "value", "get"}, "kubectl"},
		{[]string{"docker", "-c", "desktop-linux", "ps", "-a"}, "docker ps"},
		{[]string{"docker", "compose", "-f", "compose.yml", "ps"}, "docker compose ps"},
		{[]string{"docker", "network", "ls"}, "docker network ls"},
		{[]string{"docker", "--unknown", "value", "ps"}, "docker"},
		{[]string{"grep", "-rn", "pattern"}, "grep"}, // arbitrary args stay out of the key
		{[]string{"ls"}, "ls"},
		{nil, ""},
	}
	for _, tt := range tests {
		if got := CommandKey(tt.argv); got != tt.want {
			t.Errorf("CommandKey(%v) = %q, want %q", tt.argv, got, tt.want)
		}
	}
}
