package hook

import "testing"

// TestHeredocStartLineIsShell — a heredoc's START LINE is ordinary shell; only its BODY
// is opaque. Skipping from `<<` straight past the terminator made pipes and redirects on
// that line invisible, so `cat <<EOF | grep needle` wrapped cat and let grep filter
// sctx's compressed output. Found by the fuzzer after its invariant was strengthened.
func TestHeredocStartLineIsShell(t *testing.T) {
	// `| grep` downstream must make this DECLINE. If the pipe on the heredoc start
	// line is invisible, cat gets wrapped and grep filters compressed output.
	in := "cat <<EOF | grep needle\nbody\nEOF"
	if got, ok := rewrite(in); ok {
		t.Errorf("rewrite(%q) = %q, want declined — grep must not filter compressed output", in, got)
	}
}

func TestHeredocStartLineRedirectAndSeparator(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		// An OUTPUT redirect on the start line still disqualifies that segment, while a
		// later command is unaffected.
		{"redirect on the start line", "cat <<EOF > out.txt\nbody\nEOF\ngo vet ./...",
			"cat <<EOF > out.txt\nbody\nEOF\nsctx go vet ./..."},
		// A pager downstream of the start line is allowed, so the head still wraps.
		{"pager on the start line", "cat <<EOF | head -3\nbody\nEOF",
			"sctx cat <<EOF | head -3\nbody\nEOF"},
		// A `;` on the start line genuinely separates two commands.
		{"semicolon on the start line", "cat <<EOF ; git status\nbody\nEOF",
			"sctx cat <<EOF ; sctx git status\nbody\nEOF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := rewrite(tc.in)
			if got != tc.want {
				t.Errorf("rewrite(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestHeredocDelimiterWithOpenQuoteDeclines pins the bash-verified pathological case: an
// unterminated quote on a heredoc start line makes the delimiter word absorb the newline,
// so the body runs to end-of-input and the "commands" after it are text. Found by the
// fuzzer, ground-truthed against bash before choosing to decline.
func TestHeredocDelimiterWithOpenQuoteDeclines(t *testing.T) {
	for _, in := range []string{
		"<<'F'\"\n\"\nF\ngo test",
		"cat <<'F'\"\n\"\nF\ngo test",
		"cat <<EOF'\nbody\nEOF\ngo vet ./...",
	} {
		if got, ok := rewrite(in); ok {
			t.Errorf("rewrite(%q) = (%q, true), want a decline — the trailing text is heredoc body, not a command", in, got)
		}
	}
}

// TestQuotedValueIsNotAnAssignmentPlusProgram — a covered program name inside a quoted
// assignment VALUE must not be treated as the segment's program. The tokenizer used to
// split on whitespace regardless of quoting, so `A=" go test "` read as the assignment
// `A="` plus the program `go`, and sctx landed inside the string. Pre-existing bug, found
// by the fuzzer only after it started checking WHERE insertions land.
func TestQuotedValueIsNotAnAssignmentPlusProgram(t *testing.T) {
	for _, in := range []string{
		`A=" go test "`,
		`MSG='go test ./...'`,
		`A="go test" B="git status"`,
		`echo " go test "`,
	} {
		if got, ok := rewrite(in); ok {
			t.Errorf("rewrite(%q) = (%q, true), want a decline — the program name is inside a string", in, got)
		}
	}
	// The genuine env-prefix form still wraps: the assignment is a real token, and the
	// program after it is real shell.
	for _, tc := range [][2]string{
		{`CGO_ENABLED=0 go test ./...`, `CGO_ENABLED=0 sctx go test ./...`},
		{`GOFLAGS="-mod=mod" go build ./...`, `GOFLAGS="-mod=mod" sctx go build ./...`},
	} {
		if got, ok := rewrite(tc[0]); !ok || got != tc[1] {
			t.Errorf("rewrite(%q) = (%q, %v), want (%q, true)", tc[0], got, ok, tc[1])
		}
	}
}

// TestConcatenatedHeredocDelimiterDeclines — bash assembles a heredoc delimiter from
// concatenated quoted parts, so `<<'F”<<0'` means the single delimiter `F<<0`. Reading
// only the first chunk ended the body at the line `F` and wrapped the body text after it
// as a command. Fuzzer-found; declining is correct because these forms are not real usage.
func TestConcatenatedHeredocDelimiterDeclines(t *testing.T) {
	for _, in := range []string{
		"<<'F''<<0'\nF\ngo test",
		"cat <<'A'\"B\"\nAB\ngo test",
		"cat <<E'O'F\nEOF\ngo vet ./...",
		// An unquoted word carrying a quote character: bash's quote spans the newline.
		"<<F'\nF'\ngo test",
		"cat <<E\"OF\nEOF\ngo test",
		"cat <<\\EOF\nbody\nEOF\ngo test",
	} {
		if got, ok := rewrite(in); ok {
			t.Errorf("rewrite(%q) = (%q, true), want a decline — the delimiter is a concatenation we do not model", in, got)
		}
	}
	// The four forms people actually write must keep working.
	for _, tc := range [][2]string{
		{"cat <<EOF\nbody\nEOF\ngo test", "sctx cat <<EOF\nbody\nEOF\nsctx go test"},
		{"cat <<'EOF'\nbody\nEOF\ngo test", "sctx cat <<'EOF'\nbody\nEOF\nsctx go test"},
		{"cat <<\"EOF\"\nbody\nEOF\ngo test", "sctx cat <<\"EOF\"\nbody\nEOF\nsctx go test"},
		{"cat <<-EOF\n\tbody\n\tEOF\ngo test", "sctx cat <<-EOF\n\tbody\n\tEOF\nsctx go test"},
	} {
		if got, ok := rewrite(tc[0]); !ok || got != tc[1] {
			t.Errorf("rewrite(%q) = (%q, %v), want (%q, true)", tc[0], got, ok, tc[1])
		}
	}
}
