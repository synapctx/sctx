package progkey

import "testing"

// Found in our own spool the first time `sctx telemetry --preview` ran against
// real data: `./bin/sctx` recorded verbatim, under a disclosure promising we
// never send file paths. The program token is the one argument position nobody
// thinks of as an argument.
func TestAnInvokedPathIsRecordedByNameNotLocation(t *testing.T) {
	cases := map[string]string{
		"./bin/sctx":                        "sctx",
		"/usr/local/bin/terraform":          "terraform",
		"./scripts/deploy.sh":               "deploy.sh",
		"/opt/acme/internal/rotate-keys.sh": "rotate-keys.sh",
		`C:\Users\someone\tools\build.exe`:  "build.exe",
		"cargo":                             "cargo",
	}
	for in, want := range cases {
		if got := Key(in, ""); got != want {
			t.Errorf("Key(%q) = %q, want %q — the location must not reach telemetry", in, got, want)
		}
	}
}

// The subcommand join must still work once the path is stripped, or an absolute
// invocation silently loses its granularity: /usr/bin/git status -> "git status".
func TestASubcommandStillJoinsAfterTheDirectoryIsStripped(t *testing.T) {
	if got := Key("/usr/bin/git", "status"); got != "git status" {
		t.Errorf("Key = %q, want %q", got, "git status")
	}
}
