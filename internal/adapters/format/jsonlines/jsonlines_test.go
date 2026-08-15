package jsonlines

import (
	"fmt"
	"strings"
	"testing"
)

func TestRenderValidStreamWithExactCount(t *testing.T) {
	var records []string
	for i := 0; i < 12; i++ {
		records = append(records, fmt.Sprintf(`{ "seq": %d, "message": "event" }`, i))
	}
	raw := []byte(strings.Join(records, "\n") + "\n")
	out, ok := Render(raw)
	if !ok {
		t.Fatal("Render() declined valid JSONL")
	}
	if got := string(out.Body); !strings.Contains(got, "…+7 more JSON records") || strings.Contains(got, `{ "seq"`) {
		t.Fatalf("Render() = %q", got)
	}
	if out.Note != "jsonl: kept 5 of 12 records" {
		t.Fatalf("Note = %q", out.Note)
	}
}

func TestMixedAndInvalidStreamsDecline(t *testing.T) {
	mixed := []byte("{\"n\":1}\n{\"n\":2}\n{\"n\":3}\nnot-json\n{\"n\":5}\n{\"n\":6}\n{\"n\":7}\n{\"n\":8}\n")
	if got := Classify(mixed); got != MixedJSONLines {
		t.Fatalf("Classify(mixed) = %v", got)
	}
	if _, ok := Render(mixed); ok {
		t.Fatal("Render() accepted mixed stream")
	}
	invalid := []byte("one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\n")
	if got := Classify(invalid); got != NotJSONLines {
		t.Fatalf("Classify(invalid) = %v", got)
	}
}

func TestShortJSONSequenceIsNotClassifiedAsStream(t *testing.T) {
	if got := Classify([]byte("{\"a\":1}\n{\"a\":2}\n")); got != NotJSONLines {
		t.Fatalf("Classify(short) = %v", got)
	}
}

func TestScalarLinesAreNotMistakenForJSONLines(t *testing.T) {
	raw := []byte("1\n2\n3\n4\n5\n6\n7\n8\n9\n")
	if got := Classify(raw); got != NotJSONLines {
		t.Fatalf("Classify(scalar lines) = %v, want NotJSONLines", got)
	}
	if _, ok := Render(raw); ok {
		t.Fatal("Render() accepted ordinary numeric lines")
	}
}
