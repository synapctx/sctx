package jsonlines

import (
	"fmt"
	"strings"
	"testing"
)

func TestRenderValidStreamWithExactCount(t *testing.T) {
	var records []string
	for i := range 12 {
		records = append(records, fmt.Sprintf(`{ "seq": %d, "message": "event" }`, i))
	}
	raw := []byte(strings.Join(records, "\n") + "\n")
	out, ok := Render(raw)
	if !ok {
		t.Fatal("Render() declined valid JSONL")
	}
	got := string(out.Body)
	if !strings.Contains(got, "…+5 more JSON records") || strings.Contains(got, `{ "seq"`) {
		t.Fatalf("Render() = %q", got)
	}
	if out.Note != "jsonl: kept 7 of 12 records" {
		t.Fatalf("Note = %q", out.Note)
	}
	// The LAST records survive: an NDJSON stream is usually a log, and the end
	// is where the failure and the summary are.
	if !strings.HasSuffix(got, `{"seq":11,"message":"event"}`) {
		t.Errorf("the tail of the stream was dropped: %q", got)
	}
	if !strings.HasPrefix(got, `{"seq":0,"message":"event"}`) {
		t.Errorf("the head of the stream was dropped: %q", got)
	}
}

// The boundary: a stream small enough that head and tail would overlap keeps
// every record rather than printing a marker for nothing.
func TestAStreamThatFitsIsNotElided(t *testing.T) {
	var records []string
	for i := range MinRecords {
		records = append(records, fmt.Sprintf(`{ "seq": %d }`, i))
	}
	out, ok := Render([]byte(strings.Join(records, "\n") + "\n"))
	if !ok {
		t.Fatal("Render() declined valid JSONL")
	}
	body := string(out.Body)
	if strings.Contains(body, "more JSON records") {
		t.Errorf("a marker was printed for records that were not omitted: %q", body)
	}
	if out.Elided {
		t.Error("Elided is set although nothing was omitted")
	}
	if n := strings.Count(body, "\n") + 1; n != MinRecords {
		t.Errorf("kept %d records, want all %d", n, MinRecords)
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
