package run

import (
	"context"
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

func TestCommandKey(t *testing.T) {
	tests := []struct {
		argv []string
		want string
	}{
		{[]string{"go", "test", "./..."}, "go test"},
		{[]string{"git", "-C", "x", "status"}, "git status"},
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
