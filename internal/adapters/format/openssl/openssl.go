// Package openssl formats the two very verbose, finite openssl shapes agents
// run to inspect certificates:
//
//   - `x509|req|crl -text`: the hex dumps (modulus, signature, SCT
//     signatures, key bytes) are runs of colon-separated byte rows that carry
//     no readable information; each run collapses to one counted marker while
//     Issuer, Subject, Validity, key type, SANs and every extension stay.
//   - `s_client`: the PEM certificate the server sent is dropped (the chain
//     summary above it and the verify result are kept) with a counted marker.
//
// It declines when the user asked for the bytes themselves (-pubkey, -modulus,
// s_client -showcerts) and for every other subcommand. `s_client` blocks on
// stdin unless the caller redirects it (</dev/null); that is the caller's
// shape, wrapping does not change it. `s_server` must never be wrapped.
package openssl

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// hexRowRE matches a row of colon-separated bytes, with or without a trailing
// colon (openssl's x509 text dump style).
var hexRowRE = regexp.MustCompile(`^\s*([0-9a-fA-F]{2}:)+([0-9a-fA-F]{2})?$`)

// minHexRun is the shortest run of hex rows worth collapsing; a 20-byte key
// identifier fits on one row and stays readable.
const minHexRun = 3

// Formatter renders openssl output.
type Formatter struct{}

// New constructs an openssl Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims every openssl invocation; dispatch is internal.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "openssl"} }

// Aggressive renders x509/req/crl -text and s_client.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in)
}

// Relaxed behaves like Aggressive: both grammars are fixed.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(in)
}

// subcommand returns the openssl command (first non-flag argument).
func subcommand(argv []string) string {
	for _, a := range argv[min(1, len(argv)):] {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

func has(argv []string, flags ...string) bool {
	for _, a := range argv {
		for _, f := range flags {
			if a == f {
				return true
			}
		}
	}
	return false
}

func render(in format.Input) (format.Rendered, error) {
	var fn func([]string) []string
	switch subcommand(in.Argv) {
	case "x509", "req", "crl":
		if !has(in.Argv, "-text") || has(in.Argv, "-pubkey", "-modulus", "-pubout") {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		fn = collapseHex
	case "s_client":
		if has(in.Argv, "-showcerts", "-prexit") {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		fn = dropPEM
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, rawErr := readAll(in.Stdout), readAll(in.Stderr)
	lines := append(splitLines(rawOut), splitLines(rawErr)...)
	folded := fn(lines)
	body := strings.Join(folded, "\n")
	if len(folded) == len(lines) || len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

// collapseHex replaces each run of hex rows with one marker line carrying the
// run's indentation, row count and byte count.
func collapseHex(lines []string) []string {
	var out []string
	for i := 0; i < len(lines); {
		if !hexRowRE.MatchString(lines[i]) {
			out = append(out, lines[i])
			i++
			continue
		}
		j, bytesN := i, 0
		for j < len(lines) && hexRowRE.MatchString(lines[j]) {
			bytesN += (len(strings.TrimSpace(lines[j])) + 1) / 3
			j++
		}
		if j-i < minHexRun {
			out = append(out, lines[i:j]...)
		} else {
			indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
			out = append(out, fmt.Sprintf("%s…+%d hex rows (%d bytes)", indent, j-i, bytesN))
		}
		i = j
	}
	return out
}

// dropPEM replaces each PEM block with a one-line counted marker.
func dropPEM(lines []string) []string {
	var out []string
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "-----BEGIN ") {
			out = append(out, lines[i])
			continue
		}
		j := i
		for j < len(lines) && !strings.HasPrefix(lines[j], "-----END ") {
			j++
		}
		if j == len(lines) { // unterminated: keep as-is
			out = append(out, lines[i:]...)
			break
		}
		out = append(out, fmt.Sprintf("…+%d lines (%s)", j-i+1, strings.Trim(strings.TrimPrefix(lines[i], "-----BEGIN "), "- ")))
		i = j
	}
	return out
}

func readAll(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	b, _ := io.ReadAll(r)
	return b
}

func splitLines(raw []byte) []string {
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}
