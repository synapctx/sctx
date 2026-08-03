package grep

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestAll(t *testing.T) {
	fs := All()
	if len(fs) != 2 {
		t.Fatalf("All() returned %d formatters, want 2", len(fs))
	}
	commands := map[string]bool{}
	for _, f := range fs {
		commands[f.Descriptor().Command] = true
	}
	if !commands["grep"] || !commands["rg"] {
		t.Fatalf("All() commands = %v, want grep and rg", commands)
	}
	if New().Descriptor().Command != "grep" {
		t.Fatalf("New().Descriptor().Command = %q, want grep", New().Descriptor().Command)
	}
}

func input(stdout string, argv []string) format.Input {
	if argv == nil {
		argv = []string{"grep"}
	}
	return format.Input{
		Argv:   argv,
		Stdout: strings.NewReader(stdout),
		Stderr: strings.NewReader(""),
	}
}

func TestAggressive(t *testing.T) {
	f := New()

	t.Run("grep -rn many files with duplicates", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 12; i++ {
			b.WriteString("pkg/a/a.go:1:duplicate line here\n")
		}
		b.WriteString("pkg/a/a.go:50:unique other line\n")
		for i := 0; i < 3; i++ {
			b.WriteString("pkg/b/b.go:7:some other match\n")
		}
		raw := b.String()

		r, err := f.Aggressive(context.Background(), input(raw, nil))
		if err != nil {
			t.Fatalf("Aggressive returned error: %v", err)
		}
		body := string(r.Body)
		if !strings.Contains(body, "pkg/a/a.go (13)") {
			t.Errorf("body missing file header with count: %s", body)
		}
		if !strings.Contains(body, "×12") {
			t.Errorf("body missing collapsed duplicate marker: %s", body)
		}
		if !strings.Contains(body, "pkg/b/b.go (3)") {
			t.Errorf("body missing second file header: %s", body)
		}
		if r.Note != "16 matches in 2 files" {
			t.Errorf("note = %q, want %q", r.Note, "16 matches in 2 files")
		}
		if len(r.Body) >= len(raw) {
			t.Errorf("body (%d bytes) not smaller than raw (%d bytes)", len(r.Body), len(raw))
		}
	})

	t.Run("rg grouped output", func(t *testing.T) {
		raw := "pkg/a/a.go\n12:first match\n40:second match\n\n" +
			"pkg/b/b.go\n5:another match\n"

		r, err := f.Aggressive(context.Background(), input(raw, nil))
		if err != nil {
			t.Fatalf("Aggressive returned error: %v", err)
		}
		body := string(r.Body)
		if !strings.Contains(body, "pkg/a/a.go (2)") {
			t.Errorf("body missing grouped file header: %s", body)
		}
		if !strings.Contains(body, "12: first match") {
			t.Errorf("body missing trimmed match line: %s", body)
		}
		if !strings.Contains(body, "pkg/b/b.go (1)") {
			t.Errorf("body missing second grouped file: %s", body)
		}
		if r.Note != "3 matches in 2 files" {
			t.Errorf("note = %q, want %q", r.Note, "3 matches in 2 files")
		}
	})

	t.Run("grep -l file list is inapplicable", func(t *testing.T) {
		raw := "pkg/a/a.go\npkg/b/b.go\npkg/c/c.go\n"
		_, err := f.Aggressive(context.Background(), input(raw, nil))
		if err != format.ErrTierInapplicable {
			t.Fatalf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("grep -c counts is inapplicable", func(t *testing.T) {
		raw := "pkg/a/a.go:5\npkg/b/b.go:2\n"
		_, err := f.Aggressive(context.Background(), input(raw, nil))
		if err != format.ErrTierInapplicable {
			t.Fatalf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("huge match set exercises caps", func(t *testing.T) {
		var b strings.Builder
		const nFiles = 50
		const nMatchesPerFile = 20
		for i := 0; i < nFiles; i++ {
			for j := 0; j < nMatchesPerFile; j++ {
				b.WriteString("pkg/f")
				b.WriteString(itoa(i))
				b.WriteString(".go:")
				b.WriteString(itoa(j + 1))
				b.WriteString(":unique content ")
				b.WriteString(itoa(j))
				b.WriteByte('\n')
			}
		}
		raw := b.String()

		r, err := f.Aggressive(context.Background(), input(raw, nil))
		if err != nil {
			t.Fatalf("Aggressive returned error: %v", err)
		}
		body := string(r.Body)
		if !strings.Contains(body, "…+12 more") {
			t.Errorf("body missing per-file more marker (want 12 more after 8 shown of 20): %s", firstN(body, 400))
		}
		if !strings.Contains(body, "…+10 more files") {
			t.Errorf("body missing file cap marker (50 files, cap 40 -> 10 more): %s", lastN(body, 200))
		}
		wantNote := itoa(nFiles*nMatchesPerFile) + " matches in " + itoa(nFiles) + " files"
		if r.Note != wantNote {
			t.Errorf("note = %q, want %q", r.Note, wantNote)
		}
		if len(r.Body) >= len(raw) {
			t.Errorf("body (%d bytes) not smaller than raw (%d bytes)", len(r.Body), len(raw))
		}
	})

	t.Run("no matches empty output is inapplicable", func(t *testing.T) {
		_, err := f.Aggressive(context.Background(), input("", nil))
		if err != format.ErrTierInapplicable {
			t.Fatalf("err = %v, want ErrTierInapplicable", err)
		}
	})
}

func TestRelaxed(t *testing.T) {
	f := New()

	t.Run("dedupes consecutive identical lines", func(t *testing.T) {
		raw := "same\nsame\nsame\ndifferent\n"
		r, err := f.Relaxed(context.Background(), input(raw, nil))
		if err != nil {
			t.Fatalf("Relaxed returned error: %v", err)
		}
		body := string(r.Body)
		if !strings.Contains(body, "same ×3") {
			t.Errorf("body missing dedupe marker: %q", body)
		}
		if !strings.Contains(body, "different") {
			t.Errorf("body missing distinct line: %q", body)
		}
	})

	t.Run("truncates long lines", func(t *testing.T) {
		raw := strings.Repeat("x", 500) + "\n"
		r, err := f.Relaxed(context.Background(), input(raw, nil))
		if err != nil {
			t.Fatalf("Relaxed returned error: %v", err)
		}
		body := string(r.Body)
		if !strings.Contains(body, "…") {
			t.Errorf("body missing truncation marker: %q", firstN(body, 50))
		}
		if len(r.Body) >= len(raw) {
			t.Errorf("body (%d bytes) not smaller than raw (%d bytes)", len(r.Body), len(raw))
		}
	})

	t.Run("caps total lines", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 400; i++ {
			b.WriteString("line ")
			b.WriteString(itoa(i))
			b.WriteByte('\n')
		}
		raw := b.String()
		r, err := f.Relaxed(context.Background(), input(raw, nil))
		if err != nil {
			t.Fatalf("Relaxed returned error: %v", err)
		}
		body := string(r.Body)
		if !strings.Contains(body, "…+100 more lines") {
			t.Errorf("body missing line cap marker: %s", lastN(body, 100))
		}
	})

	t.Run("empty raw is inapplicable", func(t *testing.T) {
		_, err := f.Relaxed(context.Background(), input("", nil))
		if err != format.ErrTierInapplicable {
			t.Fatalf("err = %v, want ErrTierInapplicable", err)
		}
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
