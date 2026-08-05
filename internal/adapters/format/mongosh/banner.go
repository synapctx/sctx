package mongosh

import "strings"

// bannerPrefixes are exact known-prefix lines from the mongosh connection
// preamble (printed when --quiet is not used). Only these literal prefixes
// are stripped; anything else is treated as real output.
var bannerPrefixes = []string{
	"Current Mongosh Log ID:",
	"Connecting to:",
	"Using MongoDB:",
	"Using Mongosh:",
	"For mongosh info see:",
}

// stripBanner drops the leading mongosh connection preamble from raw output:
// known-prefix banner lines, the version line ("mongosh <version>"), the
// telemetry/analytics notice paragraph, blank lines separating them, and any
// "------"-delimited block (server startup warnings / deprecation notices)
// that appears among them. It stops at the first line that doesn't match one
// of these known shapes, so real data is never touched.
func stripBanner(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	lines := strings.Split(string(raw), "\n")
	i := 0
bannerLoop:
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		switch {
		case trimmed == "":
			i++
		case hasBannerPrefix(trimmed):
			i++
		case isVersionBanner(trimmed):
			i++
		case strings.Contains(trimmed, "anonymous usage data is collected"):
			i++
		case isDashLine(trimmed):
			closed := findDashClose(lines, i)
			if closed == -1 {
				break bannerLoop
			}
			i = closed + 1
		default:
			break bannerLoop
		}
	}
	return []byte(strings.Join(lines[i:], "\n"))
}

// hasBannerPrefix reports whether line starts with one of the known,
// literal banner prefixes.
func hasBannerPrefix(line string) bool {
	for _, p := range bannerPrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// isVersionBanner matches the standalone "mongosh <version>" banner line
// (e.g. "mongosh 2.1.1"), never a data line, since real output never starts
// with the literal word "mongosh" followed by a digit.
func isVersionBanner(line string) bool {
	const prefix = "mongosh "
	if !strings.HasPrefix(line, prefix) {
		return false
	}
	rest := strings.TrimPrefix(line, prefix)
	return rest != "" && rest[0] >= '0' && rest[0] <= '9'
}

// isDashLine reports whether s is a "------"-style delimiter line.
func isDashLine(s string) bool {
	if len(s) < 4 {
		return false
	}
	for _, r := range s {
		if r != '-' {
			return false
		}
	}
	return true
}

// findDashClose looks ahead (within a bounded window) for the closing dash
// line of a block opened at lines[start], returning its index or -1 if none
// is found — in which case the opening line is not treated as a banner
// delimiter (conservative: don't eat data that merely starts with dashes).
func findDashClose(lines []string, start int) int {
	const maxWindow = 80
	for j := start + 1; j < len(lines) && j < start+maxWindow; j++ {
		if isDashLine(strings.TrimSpace(lines[j])) {
			return j
		}
	}
	return -1
}
