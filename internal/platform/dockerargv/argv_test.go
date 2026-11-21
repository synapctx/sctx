package dockerargv

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		argv []string
		want string
		ok   bool
	}{
		{[]string{"docker", "ps"}, "ps", true},
		{[]string{"docker", "-c", "desktop-linux", "ps"}, "ps", true},
		{[]string{"docker", "-cdesktop-linux", "images"}, "images", true},
		{[]string{"docker", "--tlscert", "/tmp/cert", "inspect", "x"}, "inspect", true},
		{[]string{"docker", "--tlsverify", "compose", "ps"}, "compose", true},
		{[]string{"docker", "--unknown", "value", "ps"}, "", false},
		{[]string{"docker", "--host"}, "", false},
	}
	for _, tt := range tests {
		got, ok := Parse(tt.argv)
		if ok != tt.ok || got.Command != tt.want {
			t.Errorf("Parse(%v) = (%q, %v), want (%q, %v)", tt.argv, got.Command, ok, tt.want, tt.ok)
		}
	}
}

func TestParseCompose(t *testing.T) {
	tests := []struct {
		argv []string
		want string
		ok   bool
	}{
		{[]string{"docker", "compose", "ps"}, "ps", true},
		{[]string{"docker", "compose", "-f", "compose.yml", "-p", "demo", "logs", "api"}, "logs", true},
		{[]string{"docker", "--context=desktop-linux", "compose", "--progress", "plain", "build"}, "build", true},
		{[]string{"docker", "compose", "--unknown", "x", "ps"}, "", false},
	}
	for _, tt := range tests {
		top, topOK := Parse(tt.argv)
		got, ok := ParseCompose(top)
		if !topOK || ok != tt.ok || got.Command != tt.want {
			t.Errorf("ParseCompose(%v) = (%q, %v), want (%q, %v)", tt.argv, got.Command, ok, tt.want, tt.ok)
		}
	}
}

func TestOptionHelpers(t *testing.T) {
	args := []string{"-it", "--format={{.ID}}", "--", "sh", "-c", "echo"}
	if !HasFlag(args, "-i") || !HasFlag(args, "-t") {
		t.Fatal("short cluster not recognized")
	}
	if got, ok := OptionValue(args, "-f", "--format"); !ok || got != "{{.ID}}" {
		t.Fatalf("OptionValue = (%q, %v)", got, ok)
	}
	if HasFlag([]string{"-w", "/fixturego"}, "-i") {
		t.Fatal("path value was mistaken for a short flag cluster")
	}
}

func TestExecCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    bool
	}{
		{"docker direct", "exec", []string{"-e", "A=B", "container", "go", "test", "./..."}, true},
		{"docker attached option", "exec", []string{"-w/work", "container", "go", "test"}, true},
		{"docker interactive", "exec", []string{"-it", "container", "go", "test"}, false},
		{"docker shell", "exec", []string{"container", "sh", "-c", "go test"}, false},
		{"compose explicitly finite", "compose exec", []string{"-T", "--interactive=false", "api", "git", "status"}, true},
		{"compose exec --no-tty", "compose exec", []string{"--no-tty", "--interactive=false", "api", "git", "status"}, true},
		{"compose exec --no-TTY", "compose exec", []string{"--no-TTY", "--interactive=false", "api", "git", "status"}, true},
		{"compose default tty", "compose exec", []string{"api", "git", "status"}, false},
		{"compose -T with default stdin wraps", "compose exec", []string{"-T", "api", "git", "status"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := ExecCommand(tt.command, tt.args)
			if ok != tt.want {
				t.Fatalf("ExecCommand(%q, %v) ok = %v, want %v", tt.command, tt.args, ok, tt.want)
			}
		})
	}
}

func TestRunCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    string
	}{
		{"plain", "run", []string{"--rm", "golang:1.23", "go", "test", "./..."}, "go test ./..."},
		{"value flags", "run", []string{"--rm", "-v", "/a:/w", "-w", "/w", "-e", "A=B", "--name=x", "golang:1.23", "go", "vet"}, "go vet"},
		{"attached shorthand with i/t/d letters", "run", []string{"-v/work/it:/w", "-w/tmp/dt", "golang:1.23", "go", "test"}, "go test"},
		{"entrypoint", "run", []string{"--entrypoint=go", "golang:1.23", "test", "./..."}, "go test ./..."},
		{"double dash before image", "run", []string{"--rm", "--", "golang:1.23", "go", "test"}, "go test"},
		{"interactive false", "run", []string{"--interactive=false", "golang:1.23", "go", "test"}, "go test"},
		{"compose -T and stdin off", "compose run", []string{"--rm", "-T", "--interactive=false", "svc", "go", "test"}, "go test"},
		{"compose --no-tty and stdin off", "compose run", []string{"--no-tty", "--interactive=false", "svc", "go", "test"}, "go test"},
		{"compose --no-TTY and stdin off", "compose run", []string{"--no-TTY", "--interactive=false", "svc", "go", "test"}, "go test"},
		{"compose -T with stdin attached wraps", "compose run", []string{"--rm", "-T", "svc", "go", "test"}, "go test"},
		{"compose stdin off but tty", "compose run", []string{"--interactive=false", "svc", "go", "test"}, ""},
		{"cluster -it", "run", []string{"-it", "golang:1.23", "go", "test"}, ""},
		{"detach", "run", []string{"-d", "golang:1.23", "go", "test"}, ""},
		{"tty long", "run", []string{"--tty", "golang:1.23", "go", "test"}, ""},
		{"unknown flag", "run", []string{"--wat", "golang:1.23", "go", "test"}, ""},
		{"no command", "run", []string{"--rm", "golang:1.23"}, ""},
		{"image only after --", "run", []string{"--", "golang:1.23"}, ""},
		{"shell", "run", []string{"golang:1.23", "bash", "-c", "x"}, ""},
		{"sctx nested", "run", []string{"golang:1.23", "sctx", "go", "test"}, ""},
		{"compose without -T", "compose run", []string{"svc", "go", "test"}, ""},
		{"compose -T=false", "compose run", []string{"--no-tty=false", "svc", "go", "test"}, ""},
		{"empty entrypoint", "run", []string{"--entrypoint", "", "golang:1.23", "go"}, ""},
		{"other command", "exec", []string{"c", "go"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := RunCommand(tt.command, tt.args)
			if (tt.want != "") != ok || strings.Join(got, " ") != tt.want {
				t.Fatalf("RunCommand(%q, %v) = (%q, %v), want %q", tt.command, tt.args, got, ok, tt.want)
			}
		})
	}
}
