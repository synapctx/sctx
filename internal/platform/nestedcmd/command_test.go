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
		{"pipeline into narrowing tail", []string{"go test | head"}, "go test", true},
		{"pipeline into non-narrowing tail", []string{"go test | grep FAIL"}, "", false},
		{"pipeline into two narrowing tails", []string{"docker ps | head -20 | tail -3"}, "docker ps", true},
		{"pipeline with a redirect too", []string{"go test | head > out.txt"}, "", false},
		{"pipeline with a semicolon too", []string{"go test | head; ls"}, "", false},
		{"redirect", []string{"go test > result"}, "", false},
		{"substitution", []string{"go test $(pwd)"}, "", false},
		{"newline", []string{"go test\nprintf x"}, "", false},
		{"quoted argument stays one word", []string{`sbt "~ test"`}, "sbt ~ test", true},
		{"single quoted argument", []string{`go test -run 'A B'`}, "go test -run A B", true},
		{"unterminated quote", []string{`sbt "~ test`}, "", false},
		{"backslash", []string{`go test a\ b`}, "", false},
		{"expansion in double quotes", []string{`go test "$HOME"`}, "", false},
		{"trailing tail follow", []string{"ls | tail -f x"}, "", false},
		{"trailing tail follow cluster", []string{"ls | tail -fn 5 x"}, "", false},
		{"trailing tail follow long", []string{"ls | tail --follow=name x"}, "", false},
		{"trailing less follow", []string{"ls | less +F"}, "", false},
		{"trailing tail finite", []string{"ls | tail -n 5"}, "ls", true},
		{"trailing tail value containing f", []string{"ls | tail -nf5"}, "ls", true},
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

func TestShortClusterStopsAtValueLetters(t *testing.T) {
	tests := []struct {
		arg  string
		want bool
	}{
		{"-f", true}, {"-fu", true}, {"-fn", true}, {"-ufoo.service", false},
		{"-tfoo", false}, {"-gfail", false}, {"-n", false}, {"--follow", false},
	}
	for _, tt := range tests {
		if got := ShortCluster([]string{tt.arg}, "f", "utgpnSUDMobcFi"); got != tt.want {
			t.Errorf("ShortCluster(%q) = %v, want %v", tt.arg, got, tt.want)
		}
	}
}
