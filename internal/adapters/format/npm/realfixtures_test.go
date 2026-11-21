package npm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func formatterFor(t *testing.T, name string) format.Formatter {
	t.Helper()
	for _, f := range All() {
		if f.Descriptor().Command == name {
			return f
		}
	}
	t.Fatalf("no formatter %q", name)
	return nil
}

// TestRealPnpmYarnOutput drives output captured from pnpm 12.21 and yarn
// 1.22 against the install tier, including failures: an error's explanation
// (the failing package and the registry status) must survive.
func TestRealPnpmYarnOutput(t *testing.T) {
	tests := []struct {
		file   string
		argv   []string
		exit   int
		want   []string
		absent []string
	}{
		{"pnpm-install.txt", []string{"pnpm", "install"}, 0,
			[]string{"Packages: +71", "Done in 1.2s using pnpm v12.21.0", "[WARN] deprecated left-pad@1.3.0"},
			[]string{"Progress:", "+ express", "Virtual store"}},
		{"pnpm-install-again.txt", []string{"pnpm", "install"}, 0, []string{"Done in 5ms"}, nil},
		{"pnpm-add.txt", []string{"pnpm", "add", "zod"}, 0, []string{"Packages: +1", "Done in 780ms"}, []string{"+ zod"}},
		{"pnpm-add-fail.txt", []string{"pnpm", "add", "nonexistent-zzzz-pkg-12345"}, 1,
			[]string{"ERR_PNPM_PACKAGE_MANAGER_ADD_RESOLVE_LATEST", "adding a new package", "HTTP status client error (404 Not Found)", "nonexistent-"}, nil},
		{"yarn-install.txt", []string{"yarn", "install"}, 0,
			[]string{"success Saved lockfile.", "Done in 3.69s.", "warning left-pad@1.3.0: use String.prototype.padStart()"},
			[]string{"No license field", "Fetching packages"}},
		{"yarn-add.txt", []string{"yarn", "add", "zod"}, 0, []string{"success Saved 1 new dependency.", "Done in 1.01s."}, []string{"No license field"}},
		{"yarn-add-fail.txt", []string{"yarn", "add", "nonexistent-zzzz-pkg-12345"}, 1,
			[]string{"error Error: https://registry.yarnpkg.com/nonexistent-zzzz-pkg-12345: Not found", "…+10 stack frames"},
			[]string{"node:events", "Visit https"}},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			f := formatterFor(t, tc.argv[0])
			got, err := f.Aggressive(context.Background(), format.Input{
				Argv: tc.argv, Stdout: bytes.NewReader(raw), Stderr: bytes.NewReader(nil), ExitCode: tc.exit,
			})
			if err != nil {
				t.Fatal(err)
			}
			body := string(got.Body)
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("missing %q in:\n%s", w, body)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(body, a) {
					t.Errorf("unexpected %q in:\n%s", a, body)
				}
			}
			if len(got.Body) >= len(raw) && len(raw) > 60 {
				t.Errorf("not smaller: %d >= %d", len(got.Body), len(raw))
			}
		})
	}
}

// TestRealNpmAudit drives npm 11 `npm audit` output (vulnerable project, and a
// registry that refuses the request) through the audit tier. A failed audit
// must never lose lines without a marker.
func TestRealNpmAudit(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	argv := []string{"npm", "audit"}
	f := formatterFor(t, "npm")

	t.Run("vulnerable project", func(t *testing.T) {
		got, err := f.Aggressive(context.Background(), format.Input{
			Argv: argv, Stdout: bytes.NewReader(read("audit-vulnerable.txt")), Stderr: bytes.NewReader(nil), ExitCode: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		body := string(got.Body)
		if !strings.HasPrefix(body, "2 critical severity vulnerabilities") {
			t.Errorf("summary must be the count line, not an advisory title:\n%s", body)
		}
		for _, w := range []string{"lodash  <=4.17.23", "minimist  <=0.2.3", "…+2 fix-hint blocks"} {
			if !strings.Contains(body, w) {
				t.Errorf("body lacks %q:\n%s", w, body)
			}
		}
		if !got.Elided {
			t.Errorf("Elided not set despite a marker")
		}
	})

	t.Run("registry refused keeps every error and marks nothing silently", func(t *testing.T) {
		got, err := f.Aggressive(context.Background(), format.Input{
			Argv: argv, Stdout: bytes.NewReader(read("audit-offline.stdout.txt")),
			Stderr: bytes.NewReader(read("audit-offline.stderr.txt")), ExitCode: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !got.FoldStderr {
			t.Errorf("stderr lines are in Body; FoldStderr must be set")
		}
		for _, w := range []string{"undefined", "npm warn audit request", "npm error audit endpoint returned an error", "npm error A complete log"} {
			if !strings.Contains(string(got.Body), w) {
				t.Errorf("body lacks %q:\n%s", w, got.Body)
			}
		}
	})

	t.Run("errors survive past the plain-line cap, drops are counted", func(t *testing.T) {
		var out strings.Builder
		for i := 0; i < 30; i++ {
			out.WriteString("noise line\n")
		}
		got, err := f.Aggressive(context.Background(), format.Input{
			Argv: argv, Stdout: bytes.NewReader([]byte(out.String())),
			Stderr: bytes.NewReader([]byte("npm error late failure\n")), ExitCode: 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		body := string(got.Body)
		if !strings.Contains(body, "npm error late failure") || !strings.Contains(body, "…+10 lines") || !got.Elided {
			t.Errorf("want late error kept and a …+10 lines marker:\n%s", body)
		}
	})

	t.Run("non-zero exit with unrecognised output is not silently emptied", func(t *testing.T) {
		got, err := f.Aggressive(context.Background(), format.Input{
			Argv: argv, Stdout: bytes.NewReader([]byte("something odd\n")), Stderr: bytes.NewReader(nil), ExitCode: 1,
		})
		if err != nil || !strings.Contains(string(got.Body), "something odd") {
			t.Fatalf("got %q, %v", got.Body, err)
		}
	})
}

// TestAuditFailureKeepsEveryStderrLine is the review finding: the plain-line
// cap was applied to stdout first, so with >= 20 stdout lines an unrecognised
// stderr line was dropped — and stderr is folded, so it was gone for good.
func TestAuditFailureKeepsEveryStderrLine(t *testing.T) {
	var out strings.Builder
	for i := range 32 {
		fmt.Fprintf(&out, "registry line %d\n", i)
	}
	got, err := formatterFor(t, "npm").Aggressive(context.Background(), format.Input{
		Argv:     []string{"npm", "audit"},
		Stdout:   strings.NewReader(out.String()),
		Stderr:   strings.NewReader("WARNING: audit endpoint returned an unexpected payload\n"),
		ExitCode: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(got.Body)
	if !strings.Contains(body, "WARNING: audit endpoint returned an unexpected payload") {
		t.Fatalf("stderr line lost from a failed audit render:\n%s", body)
	}
	if !strings.Contains(body, "…+12 lines") || !got.Elided {
		t.Fatalf("stdout cap must still apply and be marked (Elided=%v):\n%s", got.Elided, body)
	}
}
