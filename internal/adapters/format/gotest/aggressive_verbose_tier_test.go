package gotest

import (
	"context"
	"strings"
	"testing"
)

const verboseStdout = "=== RUN   TestLeakDetected\n" +
	"    leak_test.go:31: before=0 after=1\n" +
	"--- PASS: TestLeakDetected (0.06s)\n" +
	"=== RUN   TestOther\n" +
	"=== PAUSE TestOther\n" +
	"=== CONT  TestOther\n" +
	"--- PASS: TestOther (0.00s)\n" +
	"PASS\n" +
	"ok  \texample.com/mod\t0.249s\n"

func TestAggressive_PassingVerbose(t *testing.T) {
	f := New()

	// The regression this tier exists for: a passing -v run used to collapse to
	// "ok: 1 packages" and take the log line with it.
	t.Run("keeps t.Log output and per-test results", func(t *testing.T) {
		in := newInput([]string{"go", "test", "-v", "./..."}, "go test", verboseStdout, "", 0, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		for _, want := range []string{
			"leak_test.go:31: before=0 after=1", // the whole point of -v
			"--- PASS: TestLeakDetected",
			"--- PASS: TestOther",
			"ok: 1 packages",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("Body missing %q; got:\n%s", want, body)
			}
		}
	})

	t.Run("drops progress markers and counts them", func(t *testing.T) {
		in := newInput([]string{"go", "test", "-v", "./..."}, "go test", verboseStdout, "", 0, 0)

		rendered, _ := f.Aggressive(context.Background(), in)
		body := string(rendered.Body)
		for _, unwanted := range []string{"=== RUN", "=== PAUSE", "=== CONT"} {
			if strings.Contains(body, unwanted) {
				t.Errorf("Body = %q, should drop progress marker %q", body, unwanted)
			}
		}
		// 2 RUN + 1 PAUSE + 1 CONT + 1 bare PASS = 5, and an elision is never silent.
		if !strings.Contains(body, "progress lines ×5") {
			t.Errorf("Body = %q, want a counted elision marker", body)
		}
	})

	// Without -v there is no per-test output to protect, so the terse summary
	// must still win — otherwise the fix would undo the compression everywhere.
	t.Run("non-verbose run still collapses", func(t *testing.T) {
		in := newInput([]string{"go", "test", "./..."}, "go test",
			"ok  \texample.com/mod\t0.249s\n", "", 0, 0)

		rendered, _ := f.Aggressive(context.Background(), in)
		body := string(rendered.Body)
		if !strings.HasPrefix(body, "ok: 1 packages") {
			t.Errorf("Body = %q, want the collapsed summary", body)
		}
	})
}

// -vet=off starts with "-v" and is NOT a request for verbose output. A prefix
// match here would silently switch every `go test -vet=off` run to the verbose
// tier and undo the compression on a very common invocation.
func TestVerboseMode_DoesNotMatchVetFlag(t *testing.T) {
	tests := []struct {
		argv []string
		want bool
	}{
		{[]string{"go", "test", "-v", "./..."}, true},
		{[]string{"go", "test", "-v=true", "./..."}, true},
		{[]string{"go", "test", "-test.v", "./..."}, true},
		{[]string{"go", "test", "-vet=off", "./..."}, false},
		{[]string{"go", "test", "-verbose", "./..."}, false},
		{[]string{"go", "test", "./..."}, false},
	}
	for _, tt := range tests {
		in := newInput(tt.argv, "go test", "", "", 0, 0)
		if got := verboseMode(in); got != tt.want {
			t.Errorf("verboseMode(%v) = %v, want %v", tt.argv, got, tt.want)
		}
	}
}
