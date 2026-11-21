package grep

import (
	"fmt"
	"path"
	"strings"
)

// minFileListLines is the smallest `-l` listing worth regrouping.
const minFileListLines = 4

// listsFiles reports whether argv asks grep/rg for a bare file list (-l,
// --files-with-matches, -L, --files-without-match, rg --files), whose output is one path
// per line rather than match lines.
func listsFiles(argv []string) bool {
	for i, a := range argv {
		if i == 0 {
			continue
		}
		switch {
		case a == "--":
			return false
		case a == "--files" || a == "--files-with-matches" || a == "--files-without-match":
			return true
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if strings.ContainsAny(a[1:], "lL") {
				return true
			}
		}
	}
	return false
}

// renderFileList folds a one-path-per-line listing into "dir/: a, b, c"
// groups so each directory prefix is printed once. Directories keep their
// first-seen order and files their encounter order; every path survives. It
// reports ok=false when the lines do not look like a plain file list or the
// regrouping would not be smaller.
func renderFileList(raw []byte) (body []byte, n int, ok bool) {
	lines := splitLines(raw)
	if len(lines) < minFileListLines {
		return nil, 0, false
	}
	var dirs []string
	byDir := map[string][]string{}
	for _, l := range lines {
		if l == "" || strings.HasSuffix(l, "/") {
			return nil, 0, false
		}
		d, f := path.Split(l)
		if f == "" {
			return nil, 0, false
		}
		if _, seen := byDir[d]; !seen {
			dirs = append(dirs, d)
		}
		byDir[d] = append(byDir[d], f)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d files in %d dirs", len(lines), len(dirs))
	for _, d := range dirs {
		fs := byDir[d]
		if d == "" {
			fmt.Fprintf(&b, "\n%s", strings.Join(fs, ", "))
			continue
		}
		fmt.Fprintf(&b, "\n%s: %s", d, strings.Join(fs, ", "))
	}
	if b.Len() >= len(raw) {
		return nil, 0, false
	}
	return []byte(b.String()), len(lines), true
}
