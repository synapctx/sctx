package fs

import (
	"context"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// wcFormatter strips the column-alignment padding from multi-file `wc`
// output. It is lossless: every count and every name survives; only runs of
// spaces between columns collapse to one.
type wcFormatter struct{}

func (f *wcFormatter) Descriptor() format.Match {
	return format.Match{Command: "wc"}
}

func (f *wcFormatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	raw, err := readStdout(in)
	if err != nil {
		return format.Rendered{}, err
	}
	lines := splitLines(raw)
	if len(lines) < 6 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	out := make([]string, 0, len(lines))
	type row struct{ counts, name string }
	var rows []row
	for _, l := range lines {
		fields := strings.Fields(l)
		if len(fields) == 0 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		// Counts are right-aligned numbers; the file name (which may hold
		// spaces) starts after them and is kept byte-for-byte.
		i, off := 0, 0
		for i < len(fields) && isDigits(fields[i]) {
			off = strings.Index(l[off:], fields[i]) + off + len(fields[i])
			i++
		}
		if i == 0 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		rows = append(rows, row{counts: strings.Join(fields[:i], " "), name: strings.TrimLeft(l[off:], " ")})
	}
	// Factor out the directory every named file shares, stating it once.
	var names []string
	for _, r := range rows {
		if r.name != "" && r.name != "total" {
			names = append(names, r.name)
		}
	}
	prefix := commonDirPrefix(names)
	if prefix != "" {
		out = append(out, "(paths relative to "+prefix+")")
	}
	for _, r := range rows {
		name := r.name
		if prefix != "" && name != "total" {
			name = strings.TrimPrefix(name, prefix)
		}
		if name != "" {
			out = append(out, r.counts+" "+name)
		} else {
			out = append(out, r.counts)
		}
	}
	body := strings.Join(out, "\n")
	if !shrunk(raw, body) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body)}, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (f *wcFormatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}

// commonDirPrefix returns the longest shared directory prefix (ending in "/")
// of names when it is long enough to be worth stating separately.
func commonDirPrefix(names []string) string {
	if len(names) < 4 {
		return ""
	}
	p := names[0]
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[:i+1]
	} else {
		return ""
	}
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, p) {
			i := strings.LastIndex(strings.TrimSuffix(p, "/"), "/")
			if i < 0 {
				return ""
			}
			p = p[:i+1]
		}
	}
	if len(p) < 8 {
		return ""
	}
	return p
}
