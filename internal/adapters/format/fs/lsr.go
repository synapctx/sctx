package fs

import (
	"fmt"
	"path"
	"strings"
)

// lsRecursive reports whether argv asks `ls` to recurse (-R, --recursive, or
// a bundled short-option group containing R such as -laR).
func lsRecursive(argv []string) bool {
	for _, a := range argv[min(1, len(argv)):] {
		if a == "--" {
			return false
		}
		if a == "--recursive" {
			return true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "R") {
			return true
		}
	}
	return false
}

type lsSection struct {
	header string
	lines  []string
}

// parseLsSections splits `ls -R` output into its blank-line-separated
// sections. A section's header is its first line ending in ":"; the very
// first section of a single-argument listing has none. ok is false when a
// later section does not start with a header, i.e. the shape is not ls -R.
func parseLsSections(lines []string) (secs []lsSection, ok bool) {
	var cur []string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		s := lsSection{lines: cur}
		if strings.HasSuffix(cur[0], ":") {
			s.header, s.lines = strings.TrimSuffix(cur[0], ":"), cur[1:]
		} else if len(secs) > 0 {
			ok = false
		}
		secs = append(secs, s)
		cur = nil
	}
	ok = true
	for _, l := range lines {
		if l == "" {
			flush()
			continue
		}
		cur = append(cur, l)
	}
	flush()
	return secs, ok
}

const (
	// lsrSectionsCap bounds how many directories are listed; the rest are
	// counted in one marker line.
	lsrSectionsCap = 60
	// lsrNamesCap bounds the names printed for one directory.
	lsrNamesCap = 30
)

// renderLsRecursive renders `ls -R` output one line per directory
// ("dir/: a, b, c"), collapsing sections inside noise directories into one
// counted line each. ok is false when the output is not a section listing.
func renderLsRecursive(lines []string, roots []string) (body, note string, ok bool) {
	secs, ok := parseLsSections(lines)
	if !ok || len(secs) < 2 {
		return "", "", false
	}
	var out []string
	noise := map[string]*noiseGroup{}
	var noiseOrder []string
	entries := 0
	listed, hiddenDirs, hiddenEntries := 0, 0, 0
	for _, s := range secs {
		names := make([]string, 0, len(s.lines))
		for _, l := range s.lines {
			if strings.HasPrefix(l, "total ") {
				continue
			}
			if e, parsed := parseLsLongLine(l); parsed {
				n := e.name
				if e.isDir && !strings.HasSuffix(n, "/") {
					n += "/"
				}
				names = append(names, n+" "+e.size)
				continue
			}
			names = append(names, l)
		}
		entries += len(names)
		if k := noiseKey(path.Join(s.header, "x"), roots); s.header != "" && k != "" {
			g := noise[k]
			if g == nil {
				g = &noiseGroup{dir: k, dirs: map[string]struct{}{}}
				noise[k] = g
				noiseOrder = append(noiseOrder, k)
			}
			g.paths += len(names)
			g.dirs[s.header] = struct{}{}
			continue
		}
		if listed >= lsrSectionsCap {
			hiddenDirs++
			hiddenEntries += len(names)
			continue
		}
		listed++
		label := s.header
		if label == "" {
			label = "."
		}
		label = strings.TrimPrefix(label, "./")
		if len(names) == 0 {
			out = append(out, label+"/: (empty)")
			continue
		}
		extra := 0
		if len(names) > lsrNamesCap {
			extra = len(names) - lsrNamesCap
			names = names[:lsrNamesCap]
		}
		sep := " "
		for _, n := range names {
			if strings.ContainsAny(n, " \t") {
				sep = ", " // a name with spaces needs an unambiguous separator
				break
			}
		}
		line := label + "/: " + strings.Join(names, sep)
		if extra > 0 {
			line += fmt.Sprintf("%s…+%d", sep, extra)
		}
		out = append(out, line)
	}
	if hiddenDirs > 0 {
		out = append(out, fmt.Sprintf("…+%d more dirs (%d entries) not listed", hiddenDirs, hiddenEntries))
	}
	groups := make([]*noiseGroup, 0, len(noiseOrder))
	for _, k := range noiseOrder {
		groups = append(groups, noise[k])
	}
	for _, g := range groups {
		out = append(out, fmt.Sprintf("%s/ …+%d entries in %d dirs (noise dir collapsed)", strings.TrimPrefix(g.dir, "./"), g.paths, len(g.dirs)))
	}
	return strings.Join(out, "\n"), fmt.Sprintf("%d entries in %d dirs", entries, len(secs)), true
}
