package gotest

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestAggressive_FailingVerboseDropsPassingDetail(t *testing.T) {
	f := New()

	t.Run("real fixture keeps failures and counts the passing tests it drops", func(t *testing.T) {
		raw, err := os.ReadFile("testdata/failing_verbose.txt")
		if err != nil {
			t.Fatal(err)
		}
		in := newInput([]string{"go", "test", "-v", "./..."}, "go test", string(raw), "", 1, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		for _, want := range []string{
			"--- FAIL: TestFail0",
			"--- FAIL: TestFail1",
			"fx_test.go:33: Add(0,1) = 1, want 99",
			"fx_test.go:34: Add(1,1) = 2, want 99",
			"FAIL\tfx\t",
			"…+330 passing tests",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("Body missing %q; got:\n%s", want, body)
			}
		}
		for _, unwanted := range []string{"--- PASS", "=== RUN"} {
			if strings.Contains(body, unwanted) {
				t.Errorf("Body should drop %q; got:\n%s", unwanted, body)
			}
		}
		if len(body) >= len(raw)/10 {
			t.Errorf("render %d B not far below raw %d B", len(body), len(raw))
		}
	})

	t.Run("failing subtest and its output survive passing siblings", func(t *testing.T) {
		stdout := "=== RUN   TestP\n=== RUN   TestP/a\n=== RUN   TestP/b\n" +
			"    p_test.go:9: boom\n" +
			"--- FAIL: TestP (0.00s)\n" +
			"    --- PASS: TestP/a (0.00s)\n" +
			"    --- FAIL: TestP/b (0.00s)\n" +
			"FAIL\nFAIL\tex.com/p\t0.1s\n"
		in := newInput([]string{"go", "test", "-v"}, "go test", stdout, "", 1, 0)
		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		body := string(rendered.Body)
		for _, want := range []string{"--- FAIL: TestP (", "    --- FAIL: TestP/b", "p_test.go:9: boom", "…+1 passing tests", "progress lines ×3"} {
			if !strings.Contains(body, want) {
				t.Errorf("Body missing %q; got:\n%s", want, body)
			}
		}
		if strings.Contains(body, "TestP/a") {
			t.Errorf("passing subtest line kept:\n%s", body)
		}
	})
}

func TestAggressive_PassingCount(t *testing.T) {
	f := New()
	t.Run("plain run has no knowable count and invents none", func(t *testing.T) {
		in := newInput([]string{"go", "test", "./..."}, "go test", "ok  \texample.com/a\t0.1s\nok  \texample.com/b\t(cached)\n", "", 0, 0)
		rendered, _ := f.Aggressive(context.Background(), in)
		if strings.Contains(string(rendered.Body), "tests passed") {
			t.Errorf("invented a count: %q", rendered.Body)
		}
	})
	t.Run("verbose run reports the count it can see", func(t *testing.T) {
		in := newInput([]string{"go", "test", "-v"}, "go test", verboseStdout, "", 0, 0)
		rendered, _ := f.Aggressive(context.Background(), in)
		if !strings.Contains(string(rendered.Body), "…+2 passing tests; progress lines ×5") {
			t.Errorf("Body = %q", rendered.Body)
		}
	})
	t.Run("non-verbose argv but verbose-shaped output still counts", func(t *testing.T) {
		in := newInput([]string{"go", "test"}, "go test", verboseStdout, "", 0, 0)
		rendered, _ := f.Aggressive(context.Background(), in)
		if !strings.Contains(string(rendered.Body), "2 tests passed") {
			t.Errorf("Body = %q", rendered.Body)
		}
	})
}
