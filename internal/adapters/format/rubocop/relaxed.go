package rubocop

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

var progressRowRE = regexp.MustCompile(`^[.CWEFA]+$`)

// relaxedRender drops blank lines and the progress row, and collapses runs of
// identical lines; every other line is kept.
func relaxedRender(stdout, stderr string) (format.Rendered, error) {
	if stdout == "" && stderr == "" {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	var b strings.Builder
	for _, text := range []string{stripANSI(stdout), stripANSI(stderr)} {
		lines := splitLines(text)
		for i := 0; i < len(lines); {
			l := strings.TrimRight(lines[i], " \t")
			if l == "" || progressRowRE.MatchString(l) {
				i++
				continue
			}
			j := i + 1
			for j < len(lines) && strings.TrimRight(lines[j], " \t") == l {
				j++
			}
			b.WriteString(l)
			if n := j - i; n > 1 {
				fmt.Fprintf(&b, " ×%d", n)
			}
			b.WriteByte('\n')
			i = j
		}
	}
	if b.Len() == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(b.String()), FoldStderr: stderr != ""}, nil
}
