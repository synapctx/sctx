package du

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func input(stdout string, exitCode int) format.Input {
	return format.Input{
		Command:  "du",
		Stdout:   strings.NewReader(stdout),
		ExitCode: exitCode,
	}
}

func TestFormatter_Descriptor(t *testing.T) {
	f := New()
	if got := f.Descriptor().Command; got != "du" {
		t.Fatalf("Descriptor().Command = %q, want %q", got, "du")
	}
}

func TestFormatter_Aggressive(t *testing.T) {
	tests := []struct {
		name      string
		stdout    string
		wantErr   error
		wantOrder []string // expected paths in order of appearance in Body
		wantExtra bool
		wantTotal string
		wantKeep  []string // lines expected preserved verbatim
	}{
		{
			name: "recursive du -h sorted and capped, mixed units",
			stdout: func() string {
				var b strings.Builder
				b.WriteString("2.0G\t./root\n")
				b.WriteString("50K\t./small\n")
				b.WriteString("1.2G\t./huge\n")
				b.WriteString("900M\t./big\n")
				for i := range 45 {
					b.WriteString("4.0K\t./filler" + strings.Repeat("x", i%3) + "\n")
				}
				return b.String()
			}(),
			wantOrder: []string{"./root", "./huge", "./big", "./small"},
		},
		{
			name: "raw 1K-block output",
			stdout: "4096\t./foo\n" +
				"8192\t./bar\n" +
				"1024\t./baz\n" +
				"16384\t./qux\n",
			wantOrder: []string{"./qux", "./bar", "./foo", "./baz"},
		},
		{
			name:    "single du -sh line is trivial",
			stdout:  "1.2G\t.\n",
			wantErr: format.ErrTierInapplicable,
		},
		{
			name: "permission error line preserved",
			stdout: "4.0K\t./ok1\n" +
				"du: cannot access './secret': Permission denied\n" +
				"8.0K\t./ok2\n" +
				"12.0K\t./ok3\n",
			wantKeep: []string{"du: cannot access './secret': Permission denied"},
		},
		{
			name:    "non-du blob is inapplicable",
			stdout:  "hello world\nthis is not du output at all\njust some prose\n",
			wantErr: format.ErrTierInapplicable,
		},
		{
			name: "grand total line preserved at end, outside sort/cap",
			stdout: "100K\t./a\n" +
				"5.0G\t./b\n" +
				"2.0K\t./c\n" +
				"5.1G\ttotal\n",
			wantOrder: []string{"./b", "./a", "./c"},
			wantTotal: "5.1G\ttotal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := New()
			rendered, err := f.Aggressive(context.Background(), input(tt.stdout, 0))
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			body := string(rendered.Body)
			for _, k := range tt.wantKeep {
				if !strings.Contains(body, k) {
					t.Errorf("body missing preserved line %q; body=%q", k, body)
				}
			}
			if tt.wantTotal != "" && !strings.Contains(body, tt.wantTotal) {
				t.Errorf("body missing total line %q; body=%q", tt.wantTotal, body)
			}
			if len(tt.wantOrder) > 0 {
				lastIdx := -1
				for _, p := range tt.wantOrder {
					if p == "2.0G-marker-skip" {
						continue
					}
					idx := strings.Index(body, p)
					if idx == -1 {
						t.Fatalf("body missing expected path %q; body=%q", p, body)
					}
					if idx < lastIdx {
						t.Errorf("path %q appears out of order (idx=%d, prev=%d)", p, idx, lastIdx)
					}
					lastIdx = idx
				}
			}
		})
	}
}

func TestFormatter_Aggressive_UnitSortOrder(t *testing.T) {
	stdout := "50K\t./small\n" +
		"900M\t./mid\n" +
		"1.2G\t./biggest\n" +
		"4.0K\t./filler1\n" +
		"8.0K\t./filler2\n"
	f := New()
	rendered, err := f.Aggressive(context.Background(), input(stdout, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := string(rendered.Body)

	idxBiggest := strings.Index(body, "./biggest")
	idxMid := strings.Index(body, "./mid")
	idxSmall := strings.Index(body, "./small")
	if idxBiggest == -1 || idxMid == -1 || idxSmall == -1 {
		t.Fatalf("expected all paths present; body=%q", body)
	}
	if !(idxBiggest < idxMid && idxMid < idxSmall) {
		t.Errorf("expected order biggest(1.2G) < mid(900M) < small(50K); body=%q", body)
	}
}

func TestFormatter_Aggressive_CapsWithMarker(t *testing.T) {
	var b strings.Builder
	for i := range 60 {
		b.WriteString("4.0K\t./file")
		b.WriteString(strings.Repeat("a", i%5+1))
		b.WriteByte('\n')
	}
	f := New()
	rendered, err := f.Aggressive(context.Background(), input(b.String(), 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(rendered.Body), "…+") {
		t.Errorf("expected elision marker in capped output; body=%q", rendered.Body)
	}
}

func TestFormatter_Relaxed(t *testing.T) {
	f := New()

	t.Run("dedupes and drops blanks", func(t *testing.T) {
		stdout := "4.0K\t./a\n\n4.0K\t./a\n8.0K\t./b\n"
		rendered, err := f.Relaxed(context.Background(), input(stdout, 0))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		body := string(rendered.Body)
		if strings.Count(body, "./a") != 1 {
			t.Errorf("expected dedupe of ./a, got body=%q", body)
		}
	})

	t.Run("no gain returns inapplicable", func(t *testing.T) {
		stdout := "4.0K\t./a\n8.0K\t./b\n"
		_, err := f.Relaxed(context.Background(), input(stdout, 0))
		if err != format.ErrTierInapplicable {
			t.Fatalf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
