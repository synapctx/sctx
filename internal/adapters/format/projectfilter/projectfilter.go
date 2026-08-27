// Package projectfilter implements declarative, explicitly trusted filters for
// project-specific commands. Rules never execute code: they only match argv and
// remove declared line noise or collapse provably repeated runs.
package projectfilter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/collapse"
	"github.com/synapctx/sctx/internal/domain/format"
)

const (
	RelativePath = ".sctx/filters.json"
	maxFileBytes = 1 << 20
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

type Config struct {
	Version int    `json:"version"`
	Filters []Rule `json:"filters"`
}

type Rule struct {
	ID               string    `json:"id"`
	Command          string    `json:"command"`
	ArgsPrefix       []string  `json:"args_prefix,omitempty"`
	Finite           bool      `json:"finite"`
	OverrideBuiltin  bool      `json:"override_builtin,omitzero"`
	DropExactLines   []string  `json:"drop_exact_lines,omitempty"`
	DropLinePrefixes []string  `json:"drop_line_prefixes,omitempty"`
	CollapseRepeats  bool      `json:"collapse_repeats,omitzero"`
	Fixtures         []Fixture `json:"fixtures"`
}

type Fixture struct {
	Name           string   `json:"name"`
	Argv           []string `json:"argv,omitempty"`
	Stdout         string   `json:"stdout"`
	Stderr         string   `json:"stderr,omitempty"`
	ExitCode       int      `json:"exit_code,omitzero"`
	Applied        bool     `json:"applied"`
	ExpectedStdout string   `json:"expected_stdout"`
}

type Loaded struct {
	Root   string
	Path   string
	Digest string
	Config Config
}

// Matcher is the minimal trusted command description consumed by shell hooks.
type Matcher struct {
	Command    string
	ArgsPrefix []string
}

func (m Matcher) Matches(root string, argv []string) bool {
	return matches(root, Rule{Command: m.Command, ArgsPrefix: m.ArgsPrefix}, argv)
}

type Formatter struct {
	root string
	rule Rule
}

func (f *Formatter) Descriptor() format.Match {
	return format.Match{Command: filepath.Base(f.rule.Command), Subcommands: f.rule.ArgsPrefix}
}

func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	if in.ExitCode != 0 || !matches(f.root, f.rule, in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw, err := readAll(in.Stdout)
	if err != nil {
		return format.Rendered{}, fmt.Errorf("project filter %s: reading stdout: %w", f.rule.ID, err)
	}
	return apply(f.rule, raw)
}

func (f *Formatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}

// Formatters constructs trusted formatter instances without executing anything.
func (l Loaded) Formatters() []*Formatter {
	out := make([]*Formatter, 0, len(l.Config.Filters))
	for _, rule := range l.Config.Filters {
		out = append(out, &Formatter{root: l.Root, rule: rule})
	}
	return out
}

func (l Loaded) Matchers() []Matcher {
	out := make([]Matcher, 0, len(l.Config.Filters))
	for _, rule := range l.Config.Filters {
		out = append(out, Matcher{Command: rule.Command, ArgsPrefix: append([]string(nil), rule.ArgsPrefix...)})
	}
	return out
}

func (f *Formatter) OverrideBuiltin() bool { return f.rule.OverrideBuiltin }
func (f *Formatter) MatchesArgv(argv []string) bool {
	return matches(f.root, f.rule, argv)
}
func (f *Formatter) MatchSpecificity() int { return len(f.rule.ArgsPrefix) }

// Discover searches upward for a repository root and its fixed filter path.
func Discover(start string) (root, path string, found bool, err error) {
	root, err = filepath.Abs(start)
	if err != nil {
		return "", "", false, err
	}
	if canonical, evalErr := filepath.EvalSymlinks(root); evalErr == nil {
		root = canonical
	}
	for {
		if _, statErr := os.Lstat(filepath.Join(root, ".git")); statErr == nil {
			path = filepath.Join(root, filepath.FromSlash(RelativePath))
			if _, filterErr := os.Lstat(path); filterErr == nil {
				return root, path, true, nil
			} else if !os.IsNotExist(filterErr) {
				return "", "", false, filterErr
			}
			return root, path, false, nil
		} else if !os.IsNotExist(statErr) {
			return "", "", false, statErr
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", "", false, nil
		}
		root = parent
	}
}

// Load validates the strict JSON schema and every inline fixture.
func Load(root, path string) (Loaded, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Loaded{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Loaded{}, errors.New("project filter file must be a regular non-symlink file")
	}
	f, err := os.Open(path)
	if err != nil {
		return Loaded{}, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return Loaded{}, err
	}
	if len(raw) > maxFileBytes {
		return Loaded{}, fmt.Errorf("project filter file exceeds %d bytes", maxFileBytes)
	}
	var cfg Config
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	if err := json.UnmarshalDecode(dec, &cfg, json.RejectUnknownMembers(true)); err != nil {
		return Loaded{}, fmt.Errorf("decoding project filters: %w", err)
	}
	if json.UnmarshalDecode(dec, &struct{}{}) != io.EOF {
		return Loaded{}, errors.New("project filter file contains trailing JSON")
	}
	loaded := Loaded{Root: filepath.Clean(root), Path: path, Digest: digest(raw), Config: cfg}
	if err := loaded.Verify(); err != nil {
		return Loaded{}, err
	}
	return loaded, nil
}

// LoadFrom discovers and validates the current project's filter file.
func LoadFrom(start string) (Loaded, bool, error) {
	root, path, found, err := Discover(start)
	if err != nil || !found {
		return Loaded{}, found, err
	}
	loaded, err := Load(root, path)
	return loaded, true, err
}

func (l Loaded) Verify() error {
	if l.Config.Version != 1 {
		return fmt.Errorf("project filters: version %d is unsupported", l.Config.Version)
	}
	if len(l.Config.Filters) == 0 {
		return errors.New("project filters: at least one filter is required")
	}
	ids := make(map[string]bool, len(l.Config.Filters))
	matches := make(map[string]bool, len(l.Config.Filters))
	for _, rule := range l.Config.Filters {
		if err := validateRule(rule); err != nil {
			return err
		}
		if ids[rule.ID] {
			return fmt.Errorf("project filters: duplicate id %q", rule.ID)
		}
		ids[rule.ID] = true
		matchKey := rule.Command + "\x00" + strings.Join(rule.ArgsPrefix, "\x00")
		if matches[matchKey] {
			return fmt.Errorf("project filters: duplicate command/args match for %q", rule.ID)
		}
		matches[matchKey] = true
		formatter := &Formatter{root: l.Root, rule: rule}
		for _, fixture := range rule.Fixtures {
			argv := fixture.Argv
			if len(argv) == 0 {
				argv = append([]string{rule.Command}, rule.ArgsPrefix...)
			}
			out, err := formatter.Aggressive(context.Background(), format.Input{
				Argv: argv, Stdout: strings.NewReader(fixture.Stdout),
				Stderr: strings.NewReader(fixture.Stderr), ExitCode: fixture.ExitCode,
			})
			applied := err == nil
			if err != nil && !errors.Is(err, format.ErrTierInapplicable) {
				return fmt.Errorf("project filter %s fixture %s: %w", rule.ID, fixture.Name, err)
			}
			body := fixture.Stdout
			if applied {
				body = string(out.Body)
			}
			if applied != fixture.Applied || body != fixture.ExpectedStdout {
				return fmt.Errorf("project filter %s fixture %s: got applied=%t stdout=%q, want applied=%t stdout=%q",
					rule.ID, fixture.Name, applied, body, fixture.Applied, fixture.ExpectedStdout)
			}
		}
	}
	return nil
}

func validateRule(rule Rule) error {
	if !idPattern.MatchString(rule.ID) {
		return fmt.Errorf("project filters: invalid id %q", rule.ID)
	}
	if err := validateCommand(rule.Command); err != nil {
		return fmt.Errorf("project filter %s: %w", rule.ID, err)
	}
	if !rule.Finite {
		return fmt.Errorf("project filter %s: finite must be true before sctx may buffer the command", rule.ID)
	}
	if len(rule.ArgsPrefix) > 16 {
		return fmt.Errorf("project filter %s: args_prefix is too long", rule.ID)
	}
	for _, arg := range rule.ArgsPrefix {
		if arg == "" || strings.ContainsAny(arg, "\x00\r\n") {
			return fmt.Errorf("project filter %s: invalid args_prefix value", rule.ID)
		}
	}
	if len(rule.DropExactLines)+len(rule.DropLinePrefixes) == 0 && !rule.CollapseRepeats {
		return fmt.Errorf("project filter %s: no transformations declared", rule.ID)
	}
	for _, pattern := range append(append([]string{}, rule.DropExactLines...), rule.DropLinePrefixes...) {
		if pattern == "" || strings.ContainsAny(pattern, "\r\n") {
			return fmt.Errorf("project filter %s: line patterns must be non-empty single lines", rule.ID)
		}
	}
	if len(rule.Fixtures) == 0 {
		return fmt.Errorf("project filter %s: at least one inline fixture is required", rule.ID)
	}
	hasAppliedFixture := false
	for _, fixture := range rule.Fixtures {
		if fixture.Name == "" || strings.ContainsAny(fixture.Name, "\r\n") {
			return fmt.Errorf("project filter %s: every fixture needs a valid name", rule.ID)
		}
		hasAppliedFixture = hasAppliedFixture || fixture.Applied
	}
	if !hasAppliedFixture {
		return fmt.Errorf("project filter %s: at least one fixture must exercise the filter", rule.ID)
	}
	return nil
}

func validateCommand(command string) error {
	if command == "" || filepath.IsAbs(command) || strings.ContainsAny(command, "\x00\r\n \t") {
		return errors.New("command must be a non-empty relative token")
	}
	clean := filepath.Clean(command)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("command must stay within the project")
	}
	return nil
}

func apply(rule Rule, raw []byte) (format.Rendered, error) {
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		if drops(rule, line) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}
	changed := dropped > 0
	if rule.CollapseRepeats {
		var collapsed bool
		kept, collapsed = collapse.Runs(kept)
		changed = changed || collapsed
	}
	if !changed {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	if dropped > 0 {
		kept = append(kept, fmt.Sprintf("…+%d lines filtered by project rule %s", dropped, rule.ID))
	}
	body := strings.Join(kept, "\n")
	if body == "" || len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: "project filter: " + rule.ID, Elided: true}, nil
}

// splitLines drops only the conventional final line terminator. Additional
// trailing blank records remain visible to the rule/collapser and therefore
// cannot disappear merely because another line was filtered.
func splitLines(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	text := string(raw)
	text = strings.TrimSuffix(text, "\n")
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

func drops(rule Rule, line string) bool {
	if slices.Contains(rule.DropExactLines, line) {
		return true
	}
	for _, prefix := range rule.DropLinePrefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func matches(root string, rule Rule, argv []string) bool {
	if len(argv) == 0 || len(argv)-1 < len(rule.ArgsPrefix) {
		return false
	}
	if !commandMatches(root, rule.Command, argv[0]) {
		return false
	}
	for i, want := range rule.ArgsPrefix {
		if argv[i+1] != want {
			return false
		}
	}
	return true
}

func commandMatches(root, configured, actual string) bool {
	if !strings.ContainsAny(configured, `/\`) {
		return filepath.Base(actual) == configured
	}
	actualPath := actual
	if !filepath.IsAbs(actualPath) {
		actualPath = filepath.Join(root, actualPath)
	}
	rel, err := filepath.Rel(root, filepath.Clean(actualPath))
	return err == nil && filepath.Clean(rel) == filepath.Clean(configured)
}

func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
