package gotest

import (
	"context"
	"strings"
	"testing"
)

const benchStdout = "goos: darwin\n" +
	"goarch: arm64\n" +
	"pkg: example.com/mod\n" +
	"cpu: Apple M2\n" +
	"BenchmarkAdd-8         1000000000\t         0.3125 ns/op\t       0 B/op\t       0 allocs/op\n" +
	"BenchmarkConcat-8        5000000\t       238.1 ns/op\t      16 B/op\t       1 allocs/op\n" +
	"PASS\n" +
	"ok  \texample.com/mod\t1.842s\n"

func TestAggressive_Bench(t *testing.T) {
	f := New()

	t.Run("keeps benchmark results and final PASS/ok, drops the rest", func(t *testing.T) {
		in := newInput([]string{"go", "test", "-bench=.", "./..."}, "go test", benchStdout, "", 0, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		for _, want := range []string{
			"BenchmarkAdd-8", "0.3125 ns/op", "0 B/op", "0 allocs/op",
			"BenchmarkConcat-8", "238.1 ns/op", "16 B/op", "1 allocs/op",
			"PASS",
			"ok  \texample.com/mod\t1.842s",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("Body missing %q; got %q", want, body)
			}
		}
		for _, unwanted := range []string{"goos:", "goarch:", "cpu:"} {
			if strings.Contains(body, unwanted) {
				t.Errorf("Body = %q, should drop header noise %q", body, unwanted)
			}
		}
	})
}
