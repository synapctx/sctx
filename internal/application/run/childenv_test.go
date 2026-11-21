package run

import (
	"slices"
	"testing"
)

func TestChildEnvDefaultsForEveryChild(t *testing.T) {
	base := []string{"PATH=/bin", "HOME=/h"}
	count := func(env []string, name string) (n int) {
		for _, kv := range env {
			if len(kv) > len(name) && kv[:len(name)+1] == name+"=" {
				n++
			}
		}
		return n
	}
	tests := []struct {
		name    string
		environ []string
		want    []string // entries that must be present
		absent  []string // entries that must not be present
		nilEnv  bool
	}{
		{"unset: both defaults", base, []string{"PLAYWRIGHT_HTML_OPEN=never", "TF_INPUT=0", "PATH=/bin"}, nil, false},
		{"empty values count as unset", append([]string{"PLAYWRIGHT_HTML_OPEN=", "TF_INPUT="}, base...),
			[]string{"PLAYWRIGHT_HTML_OPEN=never", "TF_INPUT=0"}, []string{"PLAYWRIGHT_HTML_OPEN=", "TF_INPUT="}, false},
		{"user html open kept", append([]string{"PLAYWRIGHT_HTML_OPEN=always"}, base...),
			[]string{"PLAYWRIGHT_HTML_OPEN=always", "TF_INPUT=0"}, []string{"PLAYWRIGHT_HTML_OPEN=never"}, false},
		{"user TF_INPUT kept", append([]string{"TF_INPUT=1"}, base...),
			[]string{"TF_INPUT=1", "PLAYWRIGHT_HTML_OPEN=never"}, []string{"TF_INPUT=0"}, false},
		{"CI is never set", append([]string{"CI=true"}, base...), []string{"CI=true"}, nil, false},
		{"both chosen: inherit", []string{"PLAYWRIGHT_HTML_OPEN=list", "TF_INPUT=1"}, nil, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := slices.Clone(tt.environ)
			got := childEnv([]string{"make", "e2e"}, tt.environ)
			if !slices.Equal(before, tt.environ) {
				t.Fatalf("input environment mutated: %v", tt.environ)
			}
			if tt.nilEnv {
				if got != nil {
					t.Fatalf("childEnv = %v, want nil (inherit)", got)
				}
				return
			}
			for _, w := range tt.want {
				if !slices.Contains(got, w) {
					t.Errorf("childEnv = %v, missing %q", got, w)
				}
			}
			for _, a := range tt.absent {
				if slices.Contains(got, a) {
					t.Errorf("childEnv = %v, must not contain %q", got, a)
				}
			}
			for _, n := range []string{"PLAYWRIGHT_HTML_OPEN", "TF_INPUT"} {
				if c := count(got, n); c != 1 {
					t.Errorf("%s appears %d times in %v, want 1", n, c, got)
				}
			}
			if slices.ContainsFunc(got, func(kv string) bool { return len(kv) >= 3 && kv[:3] == "CI=" }) != slices.Contains(tt.environ, "CI=true") {
				t.Errorf("CI changed by childEnv: %v", got)
			}
		})
	}
}

// TestChildEnvLeavesEnvironmentInspectionAlone is the review finding: with the
// overlay applied unconditionally, `printenv TF_INPUT` printed 0 and exited 0
// where unwrapped it prints nothing and exits 1.
func TestChildEnvLeavesEnvironmentInspectionAlone(t *testing.T) {
	environ := []string{"PATH=/usr/bin"}
	for _, argv := range [][]string{
		{"printenv"},
		{"printenv", "TF_INPUT"},
		{"/usr/bin/printenv", "-0"},
		{"env"},
		{"env", "-0"},
		{"env", "FOO=1"},
	} {
		if got := childEnv(argv, environ); got != nil {
			t.Errorf("childEnv(%q) = %q, want nil (no overlay for environment inspection)", argv, got)
		}
	}
	for _, argv := range [][]string{
		{"env", "FOO=1", "make", "plan"},
		{"npm", "test"},
		{"printenvx"},
	} {
		if got := childEnv(argv, environ); got == nil {
			t.Errorf("childEnv(%q) = nil, want the overlay", argv)
		}
	}
}
