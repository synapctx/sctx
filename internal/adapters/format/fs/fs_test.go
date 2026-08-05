package fs

import "testing"

func TestAll(t *testing.T) {
	formatters := All()
	if len(formatters) != 3 {
		t.Fatalf("All() returned %d formatters, want 3", len(formatters))
	}

	want := map[string]bool{"ls": false, "find": false, "tree": false}
	for _, f := range formatters {
		cmd := f.Descriptor().Command
		if _, ok := want[cmd]; !ok {
			t.Errorf("unexpected formatter command %q", cmd)
			continue
		}
		want[cmd] = true
	}
	for cmd, seen := range want {
		if !seen {
			t.Errorf("missing formatter for %q", cmd)
		}
	}
}
