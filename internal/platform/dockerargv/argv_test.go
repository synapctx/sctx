package dockerargv

import "testing"

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
		{"compose default tty", "compose exec", []string{"api", "git", "status"}, false},
		{"compose default stdin", "compose exec", []string{"-T", "api", "git", "status"}, false},
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
