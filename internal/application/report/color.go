package report

import "fmt"

// palette renders the `sctx gain` report with ANSI color when enabled, and
// as plain text otherwise. Every method is a no-op passthrough when on is
// false, so the JSON path and non-TTY writers (tests, pipes) stay byte-for-
// byte plain — color is purely a TTY affordance, never part of the data.
type palette struct{ on bool }

// ANSI select-graphic-rendition codes. Kept deliberately small: a brand
// green, a cyan for structure, warn/err ramp for percentages, and dim for
// chrome (rules, labels, empty meter cells).
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "1"
	ansiDim    = "2"
	ansiRed    = "31"
	ansiGreen  = "32"
	ansiYellow = "33"
	ansiCyan   = "36"
)

// wrap applies one or more SGR codes to s, or returns s untouched when color
// is disabled.
func (p palette) wrap(s string, codes ...string) string {
	if !p.on || len(codes) == 0 {
		return s
	}
	seq := codes[0]
	for _, c := range codes[1:] {
		seq += ";" + c
	}
	return "\x1b[" + seq + "m" + s + ansiReset
}

func (p palette) bold(s string) string   { return p.wrap(s, ansiBold) }
func (p palette) dim(s string) string    { return p.wrap(s, ansiDim) }
func (p palette) green(s string) string  { return p.wrap(s, ansiGreen) }
func (p palette) cyan(s string) string   { return p.wrap(s, ansiCyan) }
func (p palette) yellow(s string) string { return p.wrap(s, ansiYellow) }

func (p palette) boldGreen(s string) string { return p.wrap(s, ansiBold, ansiGreen) }
func (p palette) boldCyan(s string) string  { return p.wrap(s, ansiBold, ansiCyan) }

// pct colors a percentage string on a green→yellow→red ramp so the eye lands
// on the low-savings rows. Thresholds are on
// the savings percentage, not the raw magnitude.
func (p palette) pct(s string, v float64) string {
	switch {
	case v >= 50:
		return p.wrap(s, ansiGreen)
	case v >= 25:
		return p.wrap(s, ansiYellow)
	default:
		return p.wrap(s, ansiRed)
	}
}

// tier colors a padded degradation-log tier cell: verbatim (nothing saved)
// reads as an error, relaxed as a warning, anything else stays plain. raw is
// the unpadded tier used to decide; cell is the padded string that gets
// colored (so ANSI bytes don't skew column alignment).
func (p palette) tier(raw, cell string) string {
	switch raw {
	case "verbatim":
		return p.wrap(cell, ansiRed)
	case "relaxed":
		return p.wrap(cell, ansiYellow)
	default:
		return cell
	}
}

// wrapAnomaly colors a padded anomaly cell red when there is a real anomaly,
// and leaves the "-" placeholder dim. raw is the unpadded anomaly text used
// to decide; cell is the padded string that gets colored.
func (p palette) wrapAnomaly(raw, cell string) string {
	if raw == "" || raw == "-" {
		return p.dim(cell)
	}
	return p.wrap(cell, ansiRed)
}

// rule returns a dim horizontal rule of the given width using r.
func (p palette) rule(r string, width int) string {
	line := ""
	for i := 0; i < width; i++ {
		line += r
	}
	return p.dim(line)
}

// meterBar renders a fixed-width progress bar: filled cells in green, empty
// cells dim. Used for both the efficiency meter and the per-command impact
// bar (with different runes/widths).
func (p palette) meterBar(filledRune, emptyRune string, filled, width int) string {
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	fill, empty := "", ""
	for i := 0; i < filled; i++ {
		fill += filledRune
	}
	for i := 0; i < width-filled; i++ {
		empty += emptyRune
	}
	return p.green(fill) + p.dim(empty)
}

// pctString formats a percentage the way the report does everywhere (one
// decimal, trailing %), so colorers and plain callers agree on the token.
func pctString(v float64) string { return fmt.Sprintf("%.1f%%", v) }
