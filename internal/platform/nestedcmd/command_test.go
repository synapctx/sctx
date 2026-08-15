package nestedcmd

import (
	"strings"
	"testing"
)

func TestSharedSecurityGrammar(t *testing.T) {
	tests := []struct {
		name  string
		parts []string
		want  string
		ok    bool
	}{
		{"simple split", []string{"go", "test", "./..."}, "go test ./...", true},
		{"simple joined", []string{"docker ps -a"}, "docker ps -a", true},
		{"shell", []string{"sh -c 'go test'"}, "", false},
		{"nested wrapper", []string{"sctx go test"}, "", false},
		{"nested ssh", []string{"ssh other go test"}, "", false},
		{"pipeline", []string{"go test | head"}, "", false},
		{"redirect", []string{"go test > result"}, "", false},
		{"substitution", []string{"go test $(pwd)"}, "", false},
		{"newline", []string{"go test\nprintf x"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Remote(tt.parts)
			if ok != tt.ok || strings.Join(got, " ") != tt.want {
				t.Fatalf("Remote(%q) = %q, %v; want %q, %v", tt.parts, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestDirectArgumentsAreNotMistakenForShellSyntax(t *testing.T) {
	got, ok := Direct([]string{"grep", "a|b", "file.txt"})
	if !ok || strings.Join(got, " ") != "grep a|b file.txt" {
		t.Fatalf("Direct() = %v, %v", got, ok)
	}
	for _, argv := range [][]string{{"/bin/bash", "-c", "go test"}, {"sctx", "go", "test"}, nil} {
		if _, ok := Direct(argv); ok {
			t.Errorf("Direct(%v) accepted unsafe wrapper", argv)
		}
	}
}
