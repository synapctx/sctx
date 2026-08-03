package gotest

import (
	"context"
	"strings"
	"testing"
)

const testJSONPassingFixture = `{"Time":"2026-07-09T10:00:00Z","Action":"run","Package":"example.com/mod","Test":"TestAdd"}
{"Time":"2026-07-09T10:00:00Z","Action":"output","Package":"example.com/mod","Test":"TestAdd","Output":"=== RUN   TestAdd\n"}
{"Time":"2026-07-09T10:00:00Z","Action":"pass","Package":"example.com/mod","Test":"TestAdd","Elapsed":0}
{"Time":"2026-07-09T10:00:00Z","Action":"pass","Package":"example.com/mod","Elapsed":0.01}
`

const testJSONFailingFixture = `{"Time":"2026-07-09T10:00:00Z","Action":"run","Package":"example.com/mod","Test":"TestDiv"}
{"Time":"2026-07-09T10:00:00Z","Action":"output","Package":"example.com/mod","Test":"TestDiv","Output":"--- FAIL: TestDiv (0.00s)\n"}
{"Time":"2026-07-09T10:00:00Z","Action":"output","Package":"example.com/mod","Test":"TestDiv","Output":"    f_test.go:8: Div(10,2) = 5, want 6\n"}
{"Time":"2026-07-09T10:00:00Z","Action":"fail","Package":"example.com/mod","Test":"TestDiv","Elapsed":0}
{"Time":"2026-07-09T10:00:00Z","Action":"fail","Package":"example.com/mod","Elapsed":0.01}
`

func TestAggressive_TestJSON(t *testing.T) {
	f := New()

	t.Run("passing json run summarizes counts", func(t *testing.T) {
		in := newInput([]string{"go", "test", "-json", "./..."}, "go test", testJSONPassingFixture, "", 0, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "1 passed, 0 failed") {
			t.Errorf("Body = %q, want a pass/fail summary", body)
		}
		if len(rendered.Body) >= len(testJSONPassingFixture) {
			t.Fatalf("Body (%d bytes) should compress raw input (%d bytes)", len(rendered.Body), len(testJSONPassingFixture))
		}
	})

	t.Run("failing json run keeps failed test name and output excerpt", func(t *testing.T) {
		in := newInput([]string{"go", "test", "-json", "./..."}, "go test", testJSONFailingFixture, "", 1, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "0 passed, 1 failed") {
			t.Errorf("Body = %q, want a pass/fail summary", body)
		}
		if !strings.Contains(body, "FAIL example.com/mod.TestDiv") {
			t.Errorf("Body = %q, want the failed test name preserved", body)
		}
		if !strings.Contains(body, "Div(10,2) = 5, want 6") {
			t.Errorf("Body = %q, want the failure output excerpt preserved", body)
		}
	})

	t.Run("non-json go test output is left to the text tier", func(t *testing.T) {
		in := newInput([]string{"go", "test", "./..."}, "go test", passingTestStdout, "", 0, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if strings.Contains(string(rendered.Body), "go test -json:") {
			t.Errorf("Body = %q, should not use the json summary for plain text output", string(rendered.Body))
		}
	})
}
