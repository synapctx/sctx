package kubectlargv

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		argv    []string
		command string
		args    []string
		ok      bool
	}{
		{"plain", []string{"kubectl", "get", "pods"}, "get", []string{"pods"}, true},
		{"separate values", []string{"kubectl", "--context", "dev", "-n", "ns", "get", "pods"}, "get", []string{"pods"}, true},
		{"equals values", []string{"kubectl", "--request-timeout=5s", "--namespace=ns", "get"}, "get", nil, true},
		{"attached shorthand", []string{"kubectl", "-nns", "-v5", "get", "pods"}, "get", []string{"pods"}, true},
		{"boolean", []string{"kubectl", "--warnings-as-errors", "get", "pods"}, "get", []string{"pods"}, true},
		{"unknown global", []string{"kubectl", "--mystery", "value", "get"}, "", nil, false},
		{"missing value", []string{"kubectl", "--context"}, "", nil, false},
		{"no command", []string{"kubectl"}, "", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Parse(tc.argv)
			if ok != tc.ok || got.Command != tc.command || !equal(got.Args, tc.args) {
				t.Fatalf("Parse(%v) = %#v, %v; want command=%q args=%v ok=%v", tc.argv, got, ok, tc.command, tc.args, tc.ok)
			}
		})
	}
}

func TestOptionsStopAtCommandSeparator(t *testing.T) {
	args := []string{"pod/x", "-c", "app", "--", "tool", "-o", "json", "-it"}
	if value, ok := OptionValue(args, "-o", "--output"); ok {
		t.Fatalf("OptionValue found inner-command value %q", value)
	}
	if HasFlag(args, "-i", "--stdin", "-t", "--tty") {
		t.Fatal("HasFlag found inner-command flag")
	}
	if value, ok := OptionValue(args, "-c", "--container"); !ok || value != "app" {
		t.Fatalf("container = %q, %v", value, ok)
	}
}

func TestOptionValueAttachedShorthand(t *testing.T) {
	if value, ok := OptionValue([]string{"pods", "-ojson"}, "-o", "--output"); !ok || value != "json" {
		t.Fatalf("OptionValue = %q, %v", value, ok)
	}
}

func TestHasFlag(t *testing.T) {
	if !HasFlag([]string{"pod/x", "-it", "--", "sh"}, "-i", "--stdin") {
		t.Fatal("clustered -i not found")
	}
	if HasFlag([]string{"pods", "--watch=false"}, "-w", "--watch") {
		t.Fatal("explicit false treated as enabled")
	}
	if HasFlag([]string{"-c", "/fixture"}, "-i") {
		t.Fatal("path value was mistaken for a short flag cluster")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
