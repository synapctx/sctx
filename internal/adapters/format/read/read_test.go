package read

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAll(t *testing.T) {
	fs := All()
	if len(fs) != 3 {
		t.Fatalf("All() returned %d formatters, want 3", len(fs))
	}
	want := map[string]bool{"cat": false, "head": false, "tail": false}
	for _, f := range fs {
		want[f.Descriptor().Command] = true
	}
	for cmd, seen := range want {
		if !seen {
			t.Errorf("All() missing formatter for %q", cmd)
		}
	}
}

const jsonFixture = `{"items":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20],"name":"widget"}`

func TestAggressive(t *testing.T) {
	f := New()

	t.Run("JSON stdout delegates to jsoncompact", func(t *testing.T) {
		in := format.Input{Argv: []string{"cat", "file.json"}, Stdout: strings.NewReader(jsonFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !json.Valid(out.Body) {
			t.Fatalf("body is not valid JSON: %s", out.Body)
		}
		if len(out.Body) >= len(jsonFixture) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(jsonFixture))
		}
		if !strings.Contains(out.Note, "json") {
			t.Errorf("Note = %q, want mention of json", out.Note)
		}
	})

	t.Run("plain prose is inapplicable", func(t *testing.T) {
		in := format.Input{Argv: []string{"cat", "README.md"}, Stdout: strings.NewReader("just some prose\nmore prose\n")}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("non-zero exit degrades", func(t *testing.T) {
		in := format.Input{
			Argv:     []string{"cat", "missing"},
			Stdout:   strings.NewReader(""),
			Stderr:   strings.NewReader("cat: missing: No such file or directory\n"),
			ExitCode: 1,
		}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("JSONL log keeps first lines and elides the rest with a marker", func(t *testing.T) {
		var lines []string
		for i := 0; i < 12; i++ {
			lines = append(lines, fmt.Sprintf(`{"seq":%d,"level":"info","msg":"event %d"}`, i, i))
		}
		raw := strings.Join(lines, "\n") + "\n"
		in := format.Input{Argv: []string{"cat", "events.jsonl"}, Stdout: strings.NewReader(raw)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "…+7 more JSON lines") {
			t.Errorf("body missing JSONL marker, got: %q", body)
		}
		kept := strings.Split(strings.TrimSuffix(body, "\n…+7 more JSON lines"), "\n")
		if len(kept) != keepJSONLLines {
			t.Fatalf("kept %d lines, want %d: %q", len(kept), keepJSONLLines, body)
		}
		for i, line := range kept {
			if !json.Valid([]byte(line)) {
				t.Errorf("kept line %d not valid JSON: %q", i, line)
			}
		}
		if len(out.Body) >= len(raw) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(raw))
		}
	})

	t.Run("plain Go source is inapplicable, not compressed", func(t *testing.T) {
		src := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
		in := format.Input{Argv: []string{"cat", "main.go"}, Stdout: strings.NewReader(src)}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable (source must pass through verbatim)", err)
		}
	})

	t.Run("few scattered JSON-looking lines are not mistaken for JSONL", func(t *testing.T) {
		src := "func foo() {\n{\n}\nfmt.Println(1)\n}\n"
		in := format.Input{Argv: []string{"cat", "snippet.go"}, Stdout: strings.NewReader(src)}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}

func TestRelaxed(t *testing.T) {
	f := New()

	t.Run("collapses blank-line runs", func(t *testing.T) {
		in := format.Input{Stdout: strings.NewReader("a\n\n\n\n\nb\n")}
		out, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "…+3 blank") {
			t.Errorf("body missing blank marker, got: %q", body)
		}
		if !strings.Contains(body, "a") || !strings.Contains(body, "b") {
			t.Errorf("body dropped content lines: %q", body)
		}
	})

	t.Run("collapses duplicate-line runs", func(t *testing.T) {
		in := format.Input{Stdout: strings.NewReader("start\nsame\nsame\nsame\nsame\nend\n")}
		out, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "same ×4") {
			t.Errorf("body missing dupe marker, got: %q", body)
		}
	})

	t.Run("collapses timestamped repeat lines", func(t *testing.T) {
		in := format.Input{Stdout: strings.NewReader(
			"2024-01-02T10:00:00Z ERROR connection refused\n" +
				"2024-01-02T10:00:01Z ERROR connection refused\n" +
				"2024-01-02T10:00:02Z ERROR connection refused\n" +
				"2024-01-02T10:00:03Z INFO recovered\n",
		)}
		out, err := f.Relaxed(context.Background(), in)
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		body := string(out.Body)
		if !strings.Contains(body, "ERROR connection refused ×3") {
			t.Errorf("body missing timestamped-repeat marker, got: %q", body)
		}
		if !strings.Contains(body, "2024-01-02T10:00:00Z") {
			t.Errorf("body lost representative timestamp, got: %q", body)
		}
		if !strings.Contains(body, "INFO recovered") {
			t.Errorf("body dropped distinct trailing line, got: %q", body)
		}
	})

	t.Run("distinct timestamped lines are never merged", func(t *testing.T) {
		in := format.Input{Stdout: strings.NewReader(
			"2024-01-02T10:00:00Z INFO starting up\n" +
				"2024-01-02T10:00:01Z INFO loaded config\n" +
				"2024-01-02T10:00:02Z INFO listening on :8080\n",
		)}
		if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable (distinct content must not collapse)", err)
		}
	})

	t.Run("plain passthrough with no runs is inapplicable", func(t *testing.T) {
		in := format.Input{Stdout: strings.NewReader("line one\nline two\nline three\n")}
		if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("empty stdout is inapplicable", func(t *testing.T) {
		in := format.Input{Stdout: strings.NewReader("")}
		if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
