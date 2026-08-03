package gotest

import (
	"context"
	"strings"
	"testing"
)

const dataRaceStdout = "=== RUN   TestConcurrentWrite\n" +
	"=== PAUSE TestConcurrentWrite\n" +
	"=== CONT  TestConcurrentWrite\n" +
	"==================\n" +
	"WARNING: DATA RACE\n" +
	"Write at 0x00c0000a4010 by goroutine 8:\n" +
	"  example.com/mod.increment()\n" +
	"      /src/mod/counter.go:12 +0x44\n" +
	"\n" +
	"Previous write at 0x00c0000a4010 by goroutine 7:\n" +
	"  example.com/mod.increment()\n" +
	"      /src/mod/counter.go:12 +0x44\n" +
	"==================\n" +
	"--- FAIL: TestConcurrentWrite (0.00s)\n" +
	"    testing.go:1398: race detected during execution of test\n" +
	"FAIL\n" +
	"FAIL\texample.com/mod\t0.015s\n" +
	"FAIL\n"

func TestAggressive_Race(t *testing.T) {
	f := New()

	t.Run("data race report kept verbatim, RUN/PAUSE/CONT noise dropped", func(t *testing.T) {
		in := newInput([]string{"go", "test", "-race", "./..."}, "go test", dataRaceStdout, "", 1, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		for _, want := range []string{
			"WARNING: DATA RACE",
			"Write at 0x00c0000a4010 by goroutine 8:",
			"example.com/mod.increment()",
			"/src/mod/counter.go:12 +0x44",
			"Previous write at 0x00c0000a4010 by goroutine 7:",
			"--- FAIL: TestConcurrentWrite",
			"race detected during execution of test",
			"FAIL\texample.com/mod",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("Body missing required race signal %q; got %q", want, body)
			}
		}
		for _, unwanted := range []string{"=== RUN", "=== PAUSE", "=== CONT"} {
			if strings.Contains(body, unwanted) {
				t.Errorf("Body = %q, should drop verbose progress noise %q", body, unwanted)
			}
		}
		if len(rendered.Body) >= len(dataRaceStdout) {
			t.Fatalf("Body (%d bytes) should still compress raw input (%d bytes) via noise removal", len(rendered.Body), len(dataRaceStdout))
		}
	})
}
