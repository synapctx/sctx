package redact

import (
	"bytes"
	"strings"
	"testing"
)

func TestRulesPositiveNearMissPlaceholder(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantKind string // "" means no redaction expected
	}{
		// aws-access-key
		{"aws positive", "key=AKIAABCDEFGHIJKLMNOP", "aws-access-key"},
		{"aws near-miss short", "key=AKIAABCDEFG", ""},
		{"aws near-miss lowercase", "key=akiaabcdefghijklmnop", ""},

		// github-token
		{"github ghp positive", "token: ghp_" + strings.Repeat("a", 36), "github-token"},
		{"github pat positive", "token: github_pat_" + strings.Repeat("a", 22), "github-token"},
		{"github near-miss short", "token: ghp_short", ""},

		// slack-token
		{"slack positive", "xoxb-1234567890-abc", "slack-token"},
		{"slack near-miss short", "xoxb-123", ""},

		// stripe-key
		{"stripe live secret positive", "sk_live_" + strings.Repeat("a", 20), "stripe-key"},
		{"stripe restricted positive", "rk_live_" + strings.Repeat("b", 20), "stripe-key"},
		{"stripe test key not matched", "sk_test_" + strings.Repeat("a", 20), ""},

		// google-api-key
		{"google positive", "AIza" + strings.Repeat("A", 35), "google-api-key"},
		{"google near-miss short", "AIza" + strings.Repeat("A", 10), ""},

		// sendgrid-key
		{"sendgrid positive", "SG." + strings.Repeat("a", 22) + "." + strings.Repeat("b", 43), "sendgrid-key"},
		{"sendgrid near-miss short", "SG.short.short", ""},

		// npm-token
		{"npm positive", "_authToken=abcd1234efgh5678", "npm-token"},
		{"npm near-miss no value", "_authToken=", ""},

		// private-key
		{"private key positive", "-----BEGIN RSA PRIVATE KEY-----\nMIIBogIBAAKCAQ==\n-----END RSA PRIVATE KEY-----", "private-key"},
		{"private key near-miss no end", "-----BEGIN RSA PRIVATE KEY-----\nMIIBogIBAAKCAQ==", ""},

		// jwt
		{"jwt positive", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", "jwt"},
		{"jwt near-miss two segments", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0", ""},

		// bearer
		{"bearer positive", "Authorization: Bearer sometoken12345", "bearer"},
		{"bearer near-miss missing token", "Authorization: Bearer", ""},

		// sctx-key
		{"sctx positive", "sctx_live_abcdefgh12345", "sctx-key"},
		{"sctx near-miss short", "sctx_live_ab", ""},

		// generic-secret
		{"generic positive", `api_key="abcdEFGH1234"`, "generic-secret"},
		{"generic placeholder changeme", `api_key=changeme`, ""},
		{"generic placeholder redacted marker", `api_key=<redacted>`, ""},
		{"generic placeholder xxxxxxxx", `token=xxxxxxxxxxxx`, ""},
		{"generic placeholder example", `token=example`, ""},
		{"generic placeholder your-prefix", `password=your-password-here`, ""},
		{"generic low entropy", `password=aaaaaaaaaaaa`, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, rep := Apply([]byte(tc.input))
			if tc.wantKind == "" {
				if rep.Count != 0 {
					t.Fatalf("Apply(%q) = %q, rep=%+v; want no redaction", tc.input, out, rep)
				}
				return
			}
			want := "[REDACTED:" + tc.wantKind + "]"
			if !bytes.Contains(out, []byte(want)) {
				t.Fatalf("Apply(%q) = %q; want marker %q", tc.input, out, want)
			}
			if rep.ByKind[tc.wantKind] != 1 {
				t.Fatalf("Apply(%q) rep=%+v; want ByKind[%s]=1", tc.input, rep, tc.wantKind)
			}
		})
	}
}

func TestEntropyBoundary(t *testing.T) {
	// "aaaaaaaaaaaa" has zero entropy - must not be redacted.
	if e := shannonEntropy("aaaaaaaaaaaa"); e >= 3.5 {
		t.Fatalf("shannonEntropy(low) = %v, want < 3.5", e)
	}
	// A long, mixed-case+digit string should clear the 3.5 bits/char bar.
	high := "aB3$kL9!qZ7@mN2#"
	if e := shannonEntropy(high); e < 3.5 {
		t.Fatalf("shannonEntropy(high) = %v, want >= 3.5", e)
	}

	low := `api_key=aaaaaaaaaaaa`
	if _, rep := Apply([]byte(low)); rep.Count != 0 {
		t.Fatalf("low entropy value should not be redacted, got rep=%+v", rep)
	}
	highInput := `api_key=` + high
	if _, rep := Apply([]byte(highInput)); rep.Count != 1 {
		t.Fatalf("high entropy value should be redacted, got rep=%+v", rep)
	}
}

func TestLeftmostLongestBearerJWT(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	input := "Authorization: Bearer " + jwt
	out, rep := Apply([]byte(input))
	if rep.Count != 1 {
		t.Fatalf("rep.Count = %d, want 1 (single marker for overlapping bearer+jwt); rep=%+v out=%q", rep.Count, rep, out)
	}
	if rep.ByKind["bearer"] != 1 || rep.ByKind["jwt"] != 0 {
		t.Fatalf("expected the leftmost (bearer) match to win, got %+v", rep.ByKind)
	}
	want := "[REDACTED:bearer]"
	if string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestMarkerTextExact(t *testing.T) {
	out, _ := Apply([]byte("AKIAABCDEFGHIJKLMNOP"))
	if string(out) != "[REDACTED:aws-access-key]" {
		t.Fatalf("out = %q", out)
	}
}

func TestExitCodeAndCountsAreNeverRedacted(t *testing.T) {
	rendered := "FAIL ×3\n…+12 more\nAKIAABCDEFGHIJKLMNOP\nexit code: 1\n"
	out, rep := Apply([]byte(rendered))
	if rep.Count != 1 {
		t.Fatalf("rep=%+v, want exactly 1 redaction", rep)
	}
	want := "FAIL ×3\n…+12 more\n[REDACTED:aws-access-key]\nexit code: 1\n"
	if string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestWriterSplitToken(t *testing.T) {
	key := "AKIAABCDEFGHIJKLMNOP"
	first, second := key[:10], key[10:]

	var buf bytes.Buffer
	w := NewWriter(&buf)
	if _, err := w.Write([]byte(first)); err != nil {
		t.Fatalf("Write first half: %v", err)
	}
	if _, err := w.Write([]byte(second)); err != nil {
		t.Fatalf("Write second half: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	rep := w.Report()
	if rep.Count != 1 {
		t.Fatalf("rep=%+v, want 1 redaction of the reassembled key", rep)
	}
	if !bytes.Contains(buf.Bytes(), []byte("[REDACTED:aws-access-key]")) {
		t.Fatalf("output = %q, want redaction marker", buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte(key)) {
		t.Fatalf("output = %q, leaked the raw key", buf.String())
	}
}

func TestWriterCloseIsIdempotentAndClosesUnderlying(t *testing.T) {
	var buf closeTrackingBuffer
	w := NewWriter(&buf)
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !buf.closed {
		t.Fatal("underlying io.Closer was not closed")
	}
	if buf.String() != "hello" {
		t.Fatalf("buf = %q, want %q", buf.String(), "hello")
	}
}

type closeTrackingBuffer struct {
	bytes.Buffer
	closed bool
}

func (c *closeTrackingBuffer) Close() error {
	c.closed = true
	return nil
}

func TestMaxScanBehaviour(t *testing.T) {
	before := bytes.Repeat([]byte("x"), maxScan-32)
	secretBefore := "AKIAABCDEFGHIJKLMNOP"
	secretAfter := "sctx_live_shouldnotberedacted12345"

	input := append(append([]byte{}, before...), []byte(secretBefore)...)
	input = append(input, []byte(strings.Repeat("y", 64))...)
	input = append(input, []byte(secretAfter)...)

	out, rep := Apply(input)

	if rep.Unscanned <= 0 {
		t.Fatalf("rep.Unscanned = %d, want > 0", rep.Unscanned)
	}
	if !bytes.Contains(out, []byte("[REDACTED:aws-access-key]")) {
		t.Fatal("secret within the scanned window must still be redacted")
	}
	if !bytes.Contains(out, []byte(secretAfter)) {
		t.Fatal("secret beyond maxScan must be passed through untouched, not silently dropped")
	}
	if len(out) != len(input)-len(secretBefore)+len("[REDACTED:aws-access-key]") {
		t.Fatalf("unexpected output length: got %d", len(out))
	}
}

func FuzzApply(f *testing.F) {
	seeds := []string{
		"",
		"plain text with no secrets",
		"AKIAABCDEFGHIJKLMNOP",
		"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"-----BEGIN RSA PRIVATE KEY-----\nMII\n-----END RSA PRIVATE KEY-----",
		"api_key=changeme secret: token=abc123ABC456!!",
		"_authToken=deadbeefcafebabe1234",
		"sk_live_" + strings.Repeat("a", 20),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out, rep := Apply([]byte(s))
		if len(out) > 2*len(s)+4096 {
			t.Fatalf("output length %d unbounded relative to input length %d", len(out), len(s))
		}
		if rep.Count < 0 {
			t.Fatalf("negative report count: %d", rep.Count)
		}
	})
}
