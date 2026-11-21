package phpstan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// load reads a fixture captured from a real PHPStan 2.3 run.
func load(t *testing.T, name string) (string, int) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := os.ReadFile(filepath.Join("testdata", name+".exit"))
	if err != nil {
		t.Fatal(err)
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(e)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b), code
}

func input(argv []string, out string, code int) format.Input {
	return format.Input{Argv: argv, Stdout: strings.NewReader(out), ExitCode: code}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("testdata", name+".golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Errorf("%s mismatch\n--- got ---\n%s--- want ---\n%s", name, got, want)
	}
}

func TestAggressiveGoldens(t *testing.T) {
	tests := []struct {
		name string
		must []string
	}{
		{"multi", []string{
			"Calc.php (3)", "  7 Method App\\Calc::bad() should return int but returns string. [return.type]",
			"  8 Method App\\Calc::undefinedUse() has no return type specified. [missingType.return]",
			"  8 Undefined variable: $nope [variable.undefined]",
			"Other.php (3)", "  4 Method App\\Other::run() has parameter $x with no type specified. [missingType.parameter]",
			"  5 Method App\\Other::ret() should return string but returns int. [return.type]", "Found 6 errors",
		}},
		{"one", []string{"Calc.php (3)", "Found 3 errors"}},
		{"pass", []string{"No errors"}},
		{"progress", []string{"Calc.php (3)", "Found 3 errors"}},
		{"syntax", []string{"Syntax.php (1)", "Syntax error, unexpected EOF, expecting T_VARIABLE on line 2 [phpstan.parse (non-ignorable)]", "Found 1 error", "Result is incomplete because of severe errors"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, code := load(t, tt.name)
			r, err := New().Aggressive(context.Background(), input([]string{"phpstan", "analyse"}, raw, code))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Body) >= len(raw) {
				t.Errorf("render %d bytes is not smaller than raw %d", len(r.Body), len(raw))
			}
			for _, m := range tt.must {
				if !strings.Contains(string(r.Body), m) {
					t.Errorf("lost %q", m)
				}
			}
			golden(t, tt.name, r.Body)
		})
	}
}

func TestWrappedMessageAndTipAreKept(t *testing.T) {
	raw := " ------ ---------------------------------------------\n  Line   Foo.php                                     \n ------ ---------------------------------------------\n" +
		"  12     Call to an undefined method App\\Foo::bar()   \n         that is declared in a trait.                 \n         🪪  method.notFound                           \n         💡 Learn more at https://phpstan.org/x         \n" +
		" ------ ---------------------------------------------\n\n\n [ERROR] Found 1 error                                   \n\n"
	r, err := New().Aggressive(context.Background(), input([]string{"phpstan"}, raw, 1))
	if err != nil {
		t.Fatal(err)
	}
	want := "Foo.php (1)\n  12 Call to an undefined method App\\Foo::bar() that is declared in a trait. [method.notFound]\n    💡 Learn more at https://phpstan.org/x\nFound 1 error\n"
	if string(r.Body) != want {
		t.Errorf("body = %q, want %q", r.Body, want)
	}
}

func TestProjectLevelErrorsAreKept(t *testing.T) {
	raw := " -------------------------------------------------\n  Error                                           \n -------------------------------------------------\n" +
		"  Ignored error pattern #^x# was not matched.     \n -------------------------------------------------\n\n\n [ERROR] Found 1 error                          \n"
	r, err := New().Aggressive(context.Background(), input([]string{"phpstan"}, raw, 1))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.Body), "Ignored error pattern #^x# was not matched.") || !strings.Contains(string(r.Body), "Found 1 error") {
		t.Errorf("body = %q", r.Body)
	}
}

func TestDeclines(t *testing.T) {
	raw, _ := load(t, "json")
	multi, _ := load(t, "multi")
	tests := []struct {
		name string
		argv []string
		out  string
	}{
		{"json", []string{"phpstan", "analyse", "--error-format=json"}, raw},
		{"json separate arg", []string{"phpstan", "analyse", "--error-format", "json"}, raw},
		{"raw", []string{"phpstan", "analyse", "--error-format=raw"}, "a.php:1:msg\n"},
		{"baseline", []string{"phpstan", "analyse", "--generate-baseline"}, multi},
		{"no status line", []string{"phpstan"}, "hello\n"},
		{"summary counts errors we cannot find", []string{"phpstan"}, " [ERROR] Found 4 errors\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New().Aggressive(context.Background(), input(tt.argv, tt.out, 1)); !errors.Is(err, format.ErrTierInapplicable) {
				t.Fatalf("err = %v, want ErrTierInapplicable", err)
			}
			if tt.name != "no status line" && tt.name != "summary counts errors we cannot find" {
				if _, err := New().Relaxed(context.Background(), input(tt.argv, tt.out, 1)); !errors.Is(err, format.ErrTierInapplicable) {
					t.Fatalf("relaxed err = %v, want ErrTierInapplicable", err)
				}
			}
		})
	}
}

func TestRelaxedAndStderr(t *testing.T) {
	raw, code := load(t, "progress")
	in := input([]string{"phpstan"}, raw, code)
	in.Stderr = strings.NewReader("PHP Warning: x\n")
	r, err := New().Relaxed(context.Background(), in)
	if err != nil || !r.FoldStderr || strings.Contains(string(r.Body), "▓") || !strings.Contains(string(r.Body), "Found 3 errors") {
		t.Fatalf("relaxed: %v %q", err, r.Body)
	}
	if _, err := New().Relaxed(context.Background(), input([]string{"phpstan"}, "", 0)); !errors.Is(err, format.ErrTierInapplicable) {
		t.Errorf("empty err = %v", err)
	}
	in = input([]string{"phpstan"}, raw, code)
	in.Stderr = strings.NewReader("PHP Warning: x\n")
	agg, err := New().Aggressive(context.Background(), in)
	if err != nil || !strings.Contains(string(agg.Body), "PHP Warning: x") {
		t.Errorf("aggressive dropped stderr: %v %q", err, agg.Body)
	}
}

func TestDescriptor(t *testing.T) {
	if New().Descriptor().Command != "phpstan" {
		t.Fatal("wrong command")
	}
}
