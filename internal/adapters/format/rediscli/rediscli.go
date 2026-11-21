// Package rediscli compacts the output of `redis-cli` run with a command on
// the command line (`redis-cli KEYS 'user:*'`, `redis-cli INFO`). Replies are
// the answer the agent asked for: INFO loses only its CRLF line endings and
// the blank line between sections, and one-item-per-line replies (KEYS, SCAN,
// SMEMBERS, LRANGE, ...) are untouched up to lineCap lines. Above lineCap the
// middle is replaced by an explicit "…+N redis lines" marker. redis-cli exits
// 0 on a Redis error reply (the error is just a line on stdout), so nothing
// here ever rewrites a line that is not a plain list item. The "Using a
// password with '-a'" notice on stderr is the only stderr noise removed.
//
// MONITOR, SUBSCRIBE, --stat, --latency and friends never terminate; the hook
// must not wrap them (see Interactive).
package rediscli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// Above lineCap lines the first lineHead and last lineTail are kept.
const (
	lineCap  = 200
	lineHead = 150
	lineTail = 50
)

const passwordNotice = "Warning: Using a password with '-a' or '-u' option on the command line interface may not be safe."

// listCommands reply with one item per line in redis-cli's non-tty raw mode.
var listCommands = map[string]bool{
	"KEYS": true, "SMEMBERS": true, "LRANGE": true, "HKEYS": true, "HVALS": true,
	"HGETALL": true, "ZRANGE": true, "ZREVRANGE": true, "ZRANGEBYSCORE": true,
	"SCAN": true, "SSCAN": true, "HSCAN": true, "ZSCAN": true, "SUNION": true,
	"SINTER": true, "SDIFF": true, "MGET": true, "XRANGE": true, "XREVRANGE": true,
	"PUBSUB": true, "COMMAND": true, "ACL": true,
}

// valueFlags are the redis-cli options that consume the next argument.
var valueFlags = map[string]bool{
	"-h": true, "-p": true, "-s": true, "-a": true, "-u": true, "-n": true,
	"-r": true, "-i": true, "-d": true, "--user": true, "--pass": true, "--sni": true,
	"--cacert": true, "--cacertdir": true, "--cert": true, "--key": true,
	"--tls-ciphers": true, "--tls-ciphersuites": true, "--pattern": true,
	"--count": true, "--type": true, "--memkeys-samples": true, "--eval": true,
	"--rdb": true, "--functions-rdb": true, "--lru-test": true,
}

// Formatter renders `redis-cli` output.
type Formatter struct{}

// New constructs a redis-cli Formatter.
func New() *Formatter { return &Formatter{} }

// Descriptor claims the `redis-cli` program; the command is not a subcommand
// (it is data the server interprets), so none is declared.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: "redis-cli"} }

// Command returns the upper-cased Redis command in argv (the first operand
// after redis-cli's own options), or "" when there is none.
func Command(argv []string) string {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if valueFlags[a] {
				i++
			}
			continue
		}
		return strings.ToUpper(a)
	}
	return ""
}

// clusterPrompts are the `--cluster` verbs that stop at a confirmation unless
// `--cluster-yes` answers it.
var clusterPrompts = map[string]bool{"create": true, "fix": true, "reshard": true, "rebalance": true}

// Interactive reports whether argv starts something that never terminates by
// itself, or opens the REPL: no command, MONITOR/SUBSCRIBE family, --stat,
// --latency*, --replica, --ldb, an endless -r, or an -i repeat loop.
func Interactive(argv []string) bool {
	cmd := Command(argv)
	switch cmd {
	case "MONITOR", "SUBSCRIBE", "PSUBSCRIBE", "SSUBSCRIBE", "SYNC", "PSYNC":
		return true
	}
	hasOp := cmd != ""
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--stat", a == "--latency", a == "--latency-history", a == "--latency-dist",
			a == "--replica", a == "--ldb", a == "--ldb-sync-mode", a == "--intrinsic-latency",
			a == "--lru-test":
			return true // --lru-test N is a cache simulation that never ends
		case a == "--cluster" && i+1 < len(argv) && clusterPrompts[argv[i+1]] && !hasFlag(argv, "--cluster-yes"):
			return true // asks "Can I set the above configuration? (type 'yes')"
		case a == "-r" && i+1 < len(argv) && strings.HasPrefix(argv[i+1], "-"):
			return true // negative repeat count: run forever
		case a == "--pipe", a == "--scan", a == "--bigkeys", a == "--memkeys", a == "--keystats",
			a == "--rdb", a == "--functions-rdb", a == "--cluster", a == "--eval", a == "--version", a == "--help", a == "-v":
			hasOp = true
		}
	}
	return !hasOp
}

// Aggressive compacts a successful run's stdout.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if in.ExitCode != 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	stdout, stderr, err := readBoth(in)
	if err != nil {
		return format.Rendered{}, err
	}
	var (
		body    string
		omitted int
		ok      bool
	)
	cmd := Command(in.Argv)
	switch {
	case cmd == "INFO":
		body, ok = compactInfo(string(stdout))
	case listCommands[cmd] || hasFlag(in.Argv, "--scan"):
		body, omitted, ok = capLines(string(stdout))
	}
	if !ok {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if kept := stripNotice(stderr); kept != "" {
		body += kept
	}
	if len(body) >= len(stdout)+len(stderr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body:       []byte(body),
		Note:       fmt.Sprintf("redis-cli: omitted %d lines", omitted),
		FoldStderr: true,
		Elided:     omitted > 0,
	}, nil
}

// Relaxed only drops the password notice from stderr and re-emits the rest.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	stdout, stderr, err := readBoth(in)
	if err != nil {
		return format.Rendered{}, err
	}
	kept := stripNotice(stderr)
	if len(kept) >= len(stderr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := string(stdout)
	if body != "" && !strings.HasSuffix(body, "\n") && kept != "" {
		body += "\n"
	}
	body += kept
	if body == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: true}, nil
}

// compactInfo removes CRLF endings and the blank separator between sections.
// Every key:value and "# Section" line is kept.
func compactInfo(s string) (string, bool) {
	if !strings.HasPrefix(s, "# ") {
		return "", false
	}
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n") + "\n", true
}

// capLines bounds a one-item-per-line reply. At or below lineCap it is already
// lossless, so it declines.
func capLines(s string) (string, int, bool) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= lineCap {
		return "", 0, false
	}
	omitted := len(lines) - lineHead - lineTail
	out := append([]string(nil), lines[:lineHead]...)
	out = append(out, fmt.Sprintf("…+%d redis lines", omitted))
	out = append(out, lines[len(lines)-lineTail:]...)
	return strings.Join(out, "\n") + "\n", omitted, true
}

func hasFlag(argv []string, flag string) bool {
	for _, a := range argv[min(1, len(argv)):] {
		if a == flag {
			return true
		}
	}
	return false
}

func stripNotice(stderr []byte) string {
	if len(stderr) == 0 {
		return ""
	}
	var kept []string
	for _, l := range strings.Split(strings.TrimRight(string(stderr), "\r\n"), "\n") {
		if strings.TrimSpace(l) == passwordNotice {
			continue
		}
		kept = append(kept, l)
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n"
}

func readBoth(in format.Input) ([]byte, []byte, error) {
	stdout, err := readAll(in.Stdout)
	if err != nil {
		return nil, nil, fmt.Errorf("redis-cli: reading stdout: %w", err)
	}
	stderr, err := readAll(in.Stderr)
	if err != nil {
		return nil, nil, fmt.Errorf("redis-cli: reading stderr: %w", err)
	}
	return stdout, stderr, nil
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}
