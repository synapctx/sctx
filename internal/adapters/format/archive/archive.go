// Package archive formats the per-file chatter of tar, zip and unzip: verbose
// create/extract (`tar -cvf`/`-xvf`, `zip -r`, `unzip`) and long listings
// (`tar -tf`/`-tvf`, `unzip -l`/`-v`).
//
// A create/extract log says little beyond the path, so its member lines
// collapse into one counted line per parent directory ("dir/ ×N, S B").
// A listing is the answer to a question about names, so it is grouped only from
// minListing members up; a smaller verbose listing keeps every name and size
// and drops just the permission/owner/date columns, and a smaller bare listing
// is left alone. Headers, trailers, warnings, errors and anything that is not a
// member line stay verbatim and in place. Directory entries are not listed
// individually when grouping; their number is reported in a marker. Output that
// carries the archive itself (`tar -f -`, `unzip -p`) is never touched.
package archive

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

const (
	// minEntries is the smallest member count worth grouping for a
	// create/extract log, where the lines only confirm progress.
	minEntries = 20
	// minListing is the smallest member count worth grouping for a listing,
	// where the names ARE the answer: below it the names are kept and only the
	// permission/owner/date columns are dropped.
	minListing = 150
)

var (
	// tar -tv, GNU: "drwxr-xr-x root/root 0 2026-11-21 02:25 proj/"
	tarGNURE = regexp.MustCompile(`^[-dlhcbps][rwxsStT-]{9}\S*\s+\S+\s+(\d+) \d{4}-\d\d-\d\d \d\d:\d\d(?::\d\d)? (.+)$`)
	// tar -tv, bsdtar: "drwxr-xr-x  0 user staff 0 21 Nov 02:25 proj/"
	tarBSDRE = regexp.MustCompile(`^[-dlhcbps][rwxsStT-]{9}\S*\s+\d+\s+\S+\s+\S+\s+(\d+) +\d+ \w{3} +[\d:]+ (.+)$`)
	// bsdtar verbose create/extract: "a path" / "x path"
	tarBSDVerbRE = regexp.MustCompile(`^[ax] (.+)$`)
	// zip: "  adding: path (deflated 72%)"
	zipAddRE = regexp.MustCompile(`^\s*(?:adding|updating|freshening): (.+) \((?:stored|deflated) \d+%\)$`)
	// unzip -l: "     6174  2026-11-21 02:25   path" (also MM-DD-YYYY)
	unzipLRE = regexp.MustCompile(`^\s*(\d+)\s+[\d-]{10}\s+\d\d:\d\d\s+(.+)$`)
	// unzip -v: "  6174  Defl:N  1234  80% 2026-11-21 02:25 aabbccdd  path"
	unzipVRE = regexp.MustCompile(`^\s*(\d+)\s+\S+\s+\d+\s+-?\d+%\s+[\d-]{10} \d\d:\d\d [0-9a-f]{8}\s+(.+)$`)
	// unzip extraction: "  inflating: path  "
	unzipXRE = regexp.MustCompile(`^\s*(?:creating|inflating|extracting|linking):\s+(.+?)\s*$`)
)

// Formatter renders one archiver's output.
type Formatter struct{ command string }

// All returns one formatter per supported program; the registry holds one
// entry per program.
func All() []format.Formatter {
	names := []string{"tar", "bsdtar", "gtar", "zip", "unzip"}
	out := make([]format.Formatter, len(names))
	for i, n := range names {
		out[i] = &Formatter{command: n}
	}
	return out
}

// Descriptor claims every invocation of the program.
func (f *Formatter) Descriptor() format.Match { return format.Match{Command: f.command} }

// Aggressive groups member lines by directory.
func (f *Formatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(f.command, in)
}

// Relaxed behaves like Aggressive: the member grammars are fixed.
func (f *Formatter) Relaxed(_ context.Context, in format.Input) (format.Rendered, error) {
	return render(f.command, in)
}

// carriesArchive reports whether stdout is the archive/file payload rather than
// a log.
func carriesArchive(cmd string, argv []string) bool {
	for _, a := range argv[min(1, len(argv)):] {
		switch {
		case a == "-" || a == "--file=-" || a == "-O" || a == "--to-stdout" || a == "-p" && cmd == "unzip" || a == "-c" && cmd == "unzip":
			return true
		case cmd == "zip" && (a == "-q" || a == "-qr" || a == "-rq"):
			return true
		}
	}
	return false
}

type entry struct {
	path  string
	dir   string
	size  int64
	sized bool
	isDir bool
}

func parse(line string, tar, bsdVerbose bool) (entry, bool) {
	var p string
	var size int64
	var sized bool
	switch {
	case tarGNURE.MatchString(line):
		m := tarGNURE.FindStringSubmatch(line)
		size, _ = strconv.ParseInt(m[1], 10, 64)
		sized, p = true, m[2]
	case tarBSDRE.MatchString(line):
		m := tarBSDRE.FindStringSubmatch(line)
		size, _ = strconv.ParseInt(m[1], 10, 64)
		sized, p = true, m[2]
	case unzipVRE.MatchString(line):
		m := unzipVRE.FindStringSubmatch(line)
		size, _ = strconv.ParseInt(m[1], 10, 64)
		sized, p = true, m[2]
	case unzipLRE.MatchString(line):
		m := unzipLRE.FindStringSubmatch(line)
		size, _ = strconv.ParseInt(m[1], 10, 64)
		sized, p = true, m[2]
	case zipAddRE.MatchString(line):
		p = zipAddRE.FindStringSubmatch(line)[1]
	case unzipXRE.MatchString(line):
		p = unzipXRE.FindStringSubmatch(line)[1]
	case bsdVerbose && tarBSDVerbRE.MatchString(line):
		p = tarBSDVerbRE.FindStringSubmatch(line)[1]
	case !tar || bsdVerbose:
		return entry{}, false
	default: // GNU tar verbose/list: the line is the bare member path
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "tar: ") || strings.Contains(line, ": ") && !strings.Contains(line, "/") {
			return entry{}, false
		}
		p = line
	}
	if i := strings.Index(p, " -> "); i >= 0 { // symlink target
		p = p[:i]
	}
	isDir := strings.HasSuffix(p, "/")
	full := strings.TrimPrefix(p, "./")
	p = strings.TrimSuffix(full, "/")
	return entry{path: full, dir: path.Dir(p), size: size, sized: sized, isDir: isDir}, true
}

func render(cmd string, in format.Input) (format.Rendered, error) {
	if carriesArchive(cmd, in.Argv) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	rawOut, rawErr := readAll(in.Stdout), readAll(in.Stderr)
	lines := append(splitLines(rawOut), splitLines(rawErr)...)

	// bsdtar prints "a path"/"x path" for every member; only treat that prefix
	// as syntax when every candidate line has it, so a GNU member named "a b"
	// is never rewritten.
	tarish := cmd != "zip" && cmd != "unzip"
	bsd := tarish
	if bsd {
		cands := 0
		for _, l := range lines {
			if strings.HasPrefix(l, "tar: ") || strings.TrimSpace(l) == "" || tarGNURE.MatchString(l) || tarBSDRE.MatchString(l) {
				continue
			}
			cands++
			if !tarBSDVerbRE.MatchString(l) {
				bsd = false
				break
			}
		}
		bsd = bsd && cands > 0
	}

	type group struct {
		dir   string
		n     int
		size  int64
		sized bool
	}
	type parsed struct {
		line string
		e    entry
		ok   bool
	}
	ps := make([]parsed, len(lines))
	members, dirs, sizedN := 0, 0, 0
	for i, l := range lines {
		e, ok := parse(l, tarish, bsd)
		ps[i] = parsed{l, e, ok}
		if ok {
			members++
			if e.isDir {
				dirs++
			}
			if e.sized {
				sizedN++
			}
		}
	}
	listing := sizedN > 0 || tarish && tarList(in.Argv)

	var out []string
	switch {
	case members >= minEntries && (!listing || members >= minListing):
		var order []*group
		byDir := map[string]*group{}
		slot := -1
		for _, p := range ps {
			if !p.ok {
				out = append(out, p.line)
				continue
			}
			if slot < 0 {
				slot = len(out)
				out = append(out, "")
			}
			if p.e.isDir {
				continue
			}
			g := byDir[p.e.dir]
			if g == nil {
				g = &group{dir: p.e.dir}
				byDir[p.e.dir] = g
				order = append(order, g)
			}
			g.n++
			g.size += p.e.size
			g.sized = g.sized || p.e.sized
		}
		var block []string
		for _, g := range order {
			d := g.dir + "/"
			if g.dir == "." {
				d = "./"
			}
			s := fmt.Sprintf("%s ×%d", d, g.n)
			if g.sized {
				s += fmt.Sprintf(", %d B", g.size)
			}
			block = append(block, s)
		}
		if dirs > 0 {
			block = append(block, fmt.Sprintf("…+%d directory entries", dirs))
		}
		out[slot] = strings.Join(block, "\n")
	case listing && sizedN > 0 && members >= minEntries:
		// Keep every name and size; drop permission/owner/date columns.
		for _, p := range ps {
			if !p.ok {
				out = append(out, p.line)
				continue
			}
			out = append(out, fmt.Sprintf("%s %d", p.e.path, p.e.size))
		}
		out = append(out, "…perms/owner/date columns omitted")
	default:
		return format.Rendered{}, format.ErrTierInapplicable
	}
	body := strings.Join(out, "\n")
	if len(body) >= len(rawOut)+len(rawErr) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), FoldStderr: len(rawErr) > 0, Elided: true}, nil
}

// tarList reports whether the tar invocation lists an archive (-t/--list).
func tarList(argv []string) bool {
	for _, a := range argv[min(1, len(argv)):] {
		if a == "--list" {
			return true
		}
	}
	for i, a := range argv[min(1, len(argv)):] {
		bundle := ""
		switch {
		case strings.HasPrefix(a, "--"):
			continue
		case strings.HasPrefix(a, "-"):
			bundle = a[1:]
		case i == 0:
			bundle = a // old style: tar tvf x.tar
		default:
			continue
		}
		if strings.Trim(bundle, "abcdefghijklmnopqrstuvwxyz") == "" && strings.Contains(bundle, "t") && !strings.ContainsAny(bundle, "cxru") {
			return true
		}
	}
	return false
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
