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
	if repoIdx < 0 || tagIdx < 0 || colIndex(names, "IMAGE ID") < 0 || sizeIdx < 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	var out []string
	total := 0.0
	haveTotal := true
	dangling := 0
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := splitColumns(line, starts)
		repo, tag, size := cols[repoIdx], cols[tagIdx], cols[sizeIdx]
		if repo == "<none>" {
			dangling++
			continue
		}
		if v, ok := parseSize(size); ok {
			total += v
		} else {
			haveTotal = false
		}
		out = append(out, fmt.Sprintf("%s:%s %s", repo, tag, size))
	}

	count := len(out) + dangling
	if count == 0 {
		return format.Rendered{Body: []byte("0 images")}, nil
	}

	firstLine := fmt.Sprintf("%d images", count)
	if haveTotal && total > 0 {
		firstLine += fmt.Sprintf(", total ≈%s", formatSize(total))
	}

	var b strings.Builder
	b.WriteString(firstLine)
	for _, l := range out {
		b.WriteString("\n")
		b.WriteString(l)
	}
	if dangling > 0 {
		fmt.Fprintf(&b, "\n+%d dangling", dangling)
	}
	return format.Rendered{Body: []byte(b.String())}, nil
}
