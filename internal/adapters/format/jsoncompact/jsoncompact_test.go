package jsoncompact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func input(stdout string) format.Input {
	return format.Input{
		Command: "(json)",
		Stdout:  strings.NewReader(stdout),
	}
}

func TestFormatter_Descriptor(t *testing.T) {
	f := New()
	got := f.Descriptor()
	if got.Command != "(json)" {
		t.Fatalf("Descriptor().Command = %q, want %q", got.Command, "(json)")
	}
}

func TestFormatter_Relaxed(t *testing.T) {
	t.Run("pretty-printed nested JSON compacts", func(t *testing.T) {
		raw := `{
  "user": {
    "id": 1,
    "name": "alice",
    "tags": ["a", "b", "c"]
  }
}`
		f := New()
		out, err := f.Relaxed(context.Background(), input(raw))
		if err != nil {
			t.Fatalf("Relaxed() error = %v", err)
		}
		if !json.Valid(out.Body) {
			t.Fatalf("Relaxed() body is not valid JSON: %s", out.Body)
		}
		if len(out.Body) >= len(raw) {
			t.Fatalf("Relaxed() body (%d bytes) not smaller than raw (%d bytes)", len(out.Body), len(raw))
		}
		if strings.Contains(string(out.Body), "\n") {
			t.Fatalf("Relaxed() body still contains whitespace: %s", out.Body)
		}
		if out.Elided {
			t.Fatal("lossless whitespace compaction reported content elision")
		}
	})

	t.Run("already-compact JSON is inapplicable", func(t *testing.T) {
		raw := `{"a":1,"b":2,"c":[1,2,3]}`
		f := New()
		_, err := f.Relaxed(context.Background(), input(raw))
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("Relaxed() error = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("invalid JSON is inapplicable", func(t *testing.T) {
		f := New()
		_, err := f.Relaxed(context.Background(), input("not json at all"))
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("Relaxed() error = %v, want ErrTierInapplicable", err)
		}
	})
}

func TestFormatter_Aggressive(t *testing.T) {
	t.Run("invalid JSON is inapplicable", func(t *testing.T) {
		f := New()
		_, err := f.Aggressive(context.Background(), input("{not json"))
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("long string is truncated with a marker", func(t *testing.T) {
		longStr := strings.Repeat("x", 500)
		raw := fmt.Sprintf(`{"value":"%s"}`, longStr)
		f := New()
		out, err := f.Aggressive(context.Background(), input(raw))
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !json.Valid(out.Body) {
			t.Fatalf("Aggressive() body is not valid JSON: %s", out.Body)
		}
		var doc map[string]string
		if err := json.Unmarshal(out.Body, &doc); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if !strings.Contains(doc["value"], "…(+300 chars)") {
			t.Fatalf("value = %q, want truncation marker with +300 chars", doc["value"])
		}
		if len(out.Body) >= len(raw) {
			t.Fatalf("Aggressive() body (%d bytes) not smaller than raw (%d bytes)", len(out.Body), len(raw))
		}
		if !out.Elided {
			t.Fatal("long-string truncation did not report content elision")
		}
	})

	t.Run("aggressive whitespace-only reduction is lossless", func(t *testing.T) {
		raw := "{\n  \"alpha\": 1,\n  \"beta\": 2\n}\n"
		out, err := New().Aggressive(context.Background(), input(raw))
		if err != nil {
			t.Fatal(err)
		}
		if out.Elided {
			t.Fatal("unchanged JSON values reported content elision")
		}
	})

	t.Run("scalar array is elided with a generic marker", func(t *testing.T) {
		nums := make([]string, 50)
		for i := range nums {
			nums[i] = strconv.Itoa(i)
		}
		raw := fmt.Sprintf(`{"nums":[%s]}`, strings.Join(nums, ","))
		f := New()
		out, err := f.Aggressive(context.Background(), input(raw))
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !json.Valid(out.Body) {
			t.Fatalf("Aggressive() body is not valid JSON: %s", out.Body)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(out.Body, &doc); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		var arr []any
		if err := json.Unmarshal(doc["nums"], &arr); err != nil {
			t.Fatalf("unmarshal nums: %v", err)
		}
		if len(arr) != maxArrayLen+1 {
			t.Fatalf("len(arr) = %d, want %d", len(arr), maxArrayLen+1)
		}
		last, ok := arr[len(arr)-1].(string)
		if !ok || !strings.Contains(last, "+40 more") {
			t.Fatalf("last element = %v, want '+40 more' marker", arr[len(arr)-1])
		}
	})

	t.Run("homogeneous object array keeps 3 plus same-shape marker", func(t *testing.T) {
		var items []string
		for i := range 500 {
			items = append(items, fmt.Sprintf(`{"id":%d,"status":"ok"}`, i))
		}
		raw := fmt.Sprintf(`{"results":[%s]}`, strings.Join(items, ","))
		f := New()
		out, err := f.Aggressive(context.Background(), input(raw))
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !json.Valid(out.Body) {
			t.Fatalf("Aggressive() body is not valid JSON: %s", out.Body)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(out.Body, &doc); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		var arr []any
		if err := json.Unmarshal(doc["results"], &arr); err != nil {
			t.Fatalf("unmarshal results: %v", err)
		}
		if len(arr) != keepHomogeneous+1 {
			t.Fatalf("len(arr) = %d, want %d", len(arr), keepHomogeneous+1)
		}
		last, ok := arr[len(arr)-1].(string)
		if !ok || !strings.Contains(last, "+497 more items (same shape)") {
			t.Fatalf("last element = %v, want same-shape marker with +497", arr[len(arr)-1])
		}
		if len(out.Body) >= len(raw) {
			t.Fatalf("Aggressive() body (%d bytes) not smaller than raw (%d bytes)", len(out.Body), len(raw))
		}
		if out.Note == "" {
			t.Fatalf("Note is empty, want a size-reduction annotation")
		}
	})
}
