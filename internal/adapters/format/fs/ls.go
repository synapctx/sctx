package fs

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

type lsFormatter struct{}

func (f *lsFormatter) Descriptor() format.Match {
	return format.Match{Command: "ls"}
}

// permRe matches the 10-column permission string ls -l prints, e.g.
// "drwxr-xr-x" or "-rw-r--r--@" (macOS xattr marker).
var permRe = regexp.MustCompile(`^[bcdlpsD-]([r-][w-][xsS-]){3}[+@]?$`)

type lsLongEntry struct {
	name     string
	size     string
	modified string
	isDir    bool
}

// parseLsLongLine parses one ls -l data line (not a "total N" header) into
// its name, size and modified-time columns. It returns ok=false for lines
// that don't look like ls -l output.
func parseLsLongLine(line string) (lsLongEntry, bool) {
	fields := strings.Fields(line)
	if len(fields) < 9 {
		return lsLongEntry{}, false
	}
	if !permRe.MatchString(fields[0]) {
		return lsLongEntry{}, false
	}
	return lsLongEntry{
		name:     strings.Join(fields[8:], " "),
		size:     fields[4],
		modified: fields[5] + " " + fields[6] + " " + fields[7],
		isDir:    fields[0][0] == 'd',
	}, true
}

func (f *lsFormatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	raw, err := readStdout(in)
	if err != nil {
		return format.Rendered{}, err
	}
	content := nonEmptyLines(splitLines(raw))
	if len(content) <= 3 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var dataLines []string
	for _, l := range content {
		if strings.HasPrefix(l, "total ") {
			continue
		}
		dataLines = append(dataLines, l)
	}
	if len(dataLines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	if body, note, ok := renderLsLong(dataLines); ok {
		if !shrunk(raw, body) {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		return format.Rendered{Body: []byte(body), Note: note}, nil
	}

	body, note := renderLsPlain(dataLines)
	if !shrunk(raw, body) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: note}, nil
}

// renderLsLong compresses ls -l output to "name  size  modified" per entry,
// marking directories with a trailing slash. ok is false if any data line
// fails to parse as ls -l output.
func renderLsLong(lines []string) (body string, note string, ok bool) {
	entries := make([]lsLongEntry, 0, len(lines))
	for _, l := range lines {
		e, parsed := parseLsLongLine(l)
		if !parsed {
			return "", "", false
		}
		if e.name == "." || e.name == ".." {
			continue
		}
		entries = append(entries, e)
	}

	var b strings.Builder
	for i, e := range entries {
		name := e.name
		if e.isDir && !strings.HasSuffix(name, "/") {
			name += "/"
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s  %s  %s", name, e.size, e.modified)
	}
	return b.String(), fmt.Sprintf("%d entries", len(entries)), true
}

const lsWrapWidth = 100

// renderLsPlain re-flows a plain (multi-column or one-per-line) ls listing
// into comma-separated names, directories first (as indicated by a trailing
// "/" from ls -p/-F), wrapped at lsWrapWidth columns.
func renderLsPlain(lines []string) (body string, note string) {
	var names []string
	for _, l := range lines {
		names = append(names, strings.Fields(l)...)
	}

	sort.SliceStable(names, func(i, j int) bool {
		iDir := strings.HasSuffix(names[i], "/")
		jDir := strings.HasSuffix(names[j], "/")
		return iDir && !jDir
	})

	var out []string
	var line strings.Builder
	for _, n := range names {
		sep := n + ", "
		if line.Len() > 0 && line.Len()+len(sep) > lsWrapWidth {
			out = append(out, strings.TrimSuffix(line.String(), ", "))
			line.Reset()
		}
		line.WriteString(sep)
	}
	if line.Len() > 0 {
		out = append(out, strings.TrimSuffix(line.String(), ", "))
	}

	return strings.Join(out, "\n"), fmt.Sprintf("%d entries", len(names))
}

func (f *lsFormatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	return relaxedRender(ctx, in)
}
