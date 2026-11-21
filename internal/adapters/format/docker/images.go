package docker

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var sizeRe = regexp.MustCompile(`^([\d.]+)\s*([a-zA-Z]+)$`)

var sizeUnits = map[string]float64{
	"B":  1,
	"kB": 1e3,
	"MB": 1e6,
	"GB": 1e9,
	"TB": 1e12,
}

// parseSize converts a docker SIZE field (e.g. "123MB") to bytes.
func parseSize(s string) (float64, bool) {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	mult, ok := sizeUnits[m[2]]
	if !ok {
		return 0, false
	}
	return val * mult, true
}

// formatSize renders a byte count back into the coarsest unit that keeps it
// >= 1, matching docker's SI-decimal size formatting.
func formatSize(bytes float64) string {
	units := []struct {
		suffix string
		size   float64
	}{
		{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3},
	}
	for _, u := range units {
		if bytes >= u.size {
			v := bytes / u.size
			if v < 10 {
				return fmt.Sprintf("%.1f%s", v, u.suffix)
			}
			return fmt.Sprintf("%.0f%s", v, u.suffix)
		}
	}
	return fmt.Sprintf("%.0fB", bytes)
}

// aggressiveImages parses the default `docker images` table into one
// compact line per image: "repo:tag size", collapsing <none> repos into a
// dangling count.
func aggressiveImages(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	lines := splitLines(raw)
	if len(lines) == 0 {
		return format.Rendered{Body: []byte("0 images")}, nil
	}

	names, starts := parseHeader(lines[0])
	repoIdx, tagIdx, sizeIdx := colIndex(names, "REPOSITORY"), colIndex(names, "TAG"), colIndex(names, "SIZE")
	imageIdx, diskIdx := colIndex(names, "IMAGE"), colIndex(names, "DISK USAGE")
	legacy := repoIdx >= 0 && tagIdx >= 0 && sizeIdx >= 0
	current := imageIdx >= 0 && diskIdx >= 0
	idPresent := colIndex(names, "IMAGE ID") >= 0 || colIndex(names, "ID") >= 0
	if !idPresent || (!legacy && !current) {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var out []imageEntry
	dangling := 0
	shortened := false
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := splitColumns(line, starts)
		ref, size := "", ""
		if legacy {
			ref, size = cols[repoIdx]+":"+cols[tagIdx], cols[sizeIdx]
		} else {
			ref, size = cols[imageIdx], cols[diskIdx]
		}
		if ref == "<none>" || strings.HasPrefix(ref, "<none>:") {
			dangling++
			continue
		}
		ref, cut := shortDigest(ref)
		shortened = shortened || cut
		out = append(out, imageEntry{ref: ref, size: size})
	}

	count := len(out) + dangling
	if count == 0 {
		return format.Rendered{Body: []byte("0 images")}, nil
	}

	firstLine := fmt.Sprintf("%d images", count)
	var b strings.Builder
	b.WriteString(firstLine)
	for _, l := range groupImages(out) {
		b.WriteString("\n")
		b.WriteString(l)
	}
	if dangling > 0 {
		fmt.Fprintf(&b, "\n+%d dangling", dangling)
	}
	if shortened {
		b.WriteString("\n(digests shortened to 12 hex chars = IMAGE ID)")
	}
	return format.Rendered{Body: []byte(b.String())}, nil
}

// imageEntry is one named image: its reference and displayed size.
type imageEntry struct{ ref, size string }

// digestRe matches a repo@sha256:<64 hex> reference.
var digestRe = regexp.MustCompile(`@sha256:([0-9a-f]{64})$`)

// shortDigest shortens a full sha256 digest reference to its first 12 hex
// characters, which is exactly the IMAGE ID docker accepts for rm/inspect.
// The second result reports whether anything was cut.
func shortDigest(ref string) (string, bool) {
	m := digestRe.FindStringSubmatchIndex(ref)
	if m == nil {
		return ref, false
	}
	return ref[:m[2]+12], true
}

// splitRef splits "repo:tag" at the tag colon (one after the last slash). A
// digest reference or a tagless one has no tag and is never grouped.
func splitRef(ref string) (repo, tag string, ok bool) {
	if strings.Contains(ref, "@") {
		return "", "", false
	}
	i := strings.LastIndex(ref, ":")
	if i < 0 || i < strings.LastIndex(ref, "/") {
		return "", "", false
	}
	return ref[:i], ref[i+1:], true
}

// groupImages folds consecutive-or-not images that share a repository into
// one "repo: tag size, tag size" line, in first-seen order. A repository with
// a single tag, and digest references, stay "ref size" lines. Every tag and
// size is kept; only the repeated repository prefix is folded.
func groupImages(entries []imageEntry) []string {
	type group struct {
		repo string
		tags []string
		one  string
	}
	var groups []*group
	byRepo := map[string]*group{}
	for _, e := range entries {
		repo, tag, ok := splitRef(e.ref)
		if !ok {
			groups = append(groups, &group{one: e.ref + " " + e.size})
			continue
		}
		g := byRepo[repo]
		if g == nil {
			g = &group{repo: repo}
			byRepo[repo] = g
			groups = append(groups, g)
		}
		g.tags = append(g.tags, tag+" "+e.size)
	}
	lines := make([]string, 0, len(groups))
	for _, g := range groups {
		switch {
		case g.one != "":
			lines = append(lines, g.one)
		case len(g.tags) == 1:
			lines = append(lines, g.repo+":"+g.tags[0])
		default:
			lines = append(lines, g.repo+": "+strings.Join(g.tags, ", "))
		}
	}
	return lines
}
