package git

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// commitHeaderRe: "[main 850a312] subject" / "[main (root-commit) 850a312] subject".
	commitHeaderRe = regexp.MustCompile(`^\[(\S+)(?: \(root-commit\))? ([0-9a-f]{7,40})\] ?(.*)$`)
	statSummaryRe  = regexp.MustCompile(`^ ?\d+ files? changed`)
	modeLineRe     = regexp.MustCompile(`^ (create|delete) mode \d+ `)
	otherModeRe    = regexp.MustCompile(`^ (rename|copy|mode change) `)
	updatingRe     = regexp.MustCompile(`^Updating ([0-9a-f]+\.\.\.?[0-9a-f]+)$`)
)

// hasMessageFlag reports whether the commit message was supplied on the command
// line (-m, -am, --message, -F, -C), in which case the caller already knows it.
func hasMessageFlag(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--":
			return false
		case strings.HasPrefix(a, "--message"), strings.HasPrefix(a, "--file"), strings.HasPrefix(a, "--reuse-message"), strings.HasPrefix(a, "--reedit-message"):
			return true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a[1:], "mFCc"):
			return true
		}
	}
	return false
}

// confirmWrite collapses a SUCCESSFUL write command's already noise-filtered
// lines into a short "ok ..." confirmation when they match a fully-understood
// shape. It returns nil for any line it does not recognise, so unexpected
// output (hook output, warnings, advice) is never summarised away.
func confirmWrite(sub string, args []string, lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	switch sub {
	case "commit":
		return confirmCommit(args, lines)
	case "pull", "merge":
		return confirmIntegrate(lines)
	}
	return nil
}

func confirmCommit(args []string, lines []string) []string {
	m := commitHeaderRe.FindStringSubmatch(lines[0])
	if m == nil {
		return nil
	}
	stat := ""
	created, deleted := 0, 0
	for _, l := range lines[1:] {
		switch {
		case statSummaryRe.MatchString(l) && stat == "":
			stat = strings.TrimSpace(l)
		case modeLineRe.MatchString(l):
			if strings.HasPrefix(l, " create") {
				created++
			} else {
				deleted++
			}
		case otherModeRe.MatchString(l):
			// Renames/copies/mode changes are rare enough to leave verbatim.
			return nil
		default:
			return nil
		}
	}
	out := fmt.Sprintf("ok %s %s", m[1], m[2])
	if !hasMessageFlag(args) && m[3] != "" {
		out += " " + m[3]
	}
	var extra []string
	if stat != "" {
		extra = append(extra, stat)
	}
	if created > 0 {
		extra = append(extra, fmt.Sprintf("%d created", created))
	}
	if deleted > 0 {
		extra = append(extra, fmt.Sprintf("%d deleted", deleted))
	}
	if len(extra) > 0 {
		out += ": " + strings.Join(extra, "; ")
	}
	return []string{out}
}

// confirmIntegrate handles pull/merge that were a no-op or a fast-forward.
func confirmIntegrate(lines []string) []string {
	if len(lines) == 1 && lines[0] == "Already up to date." {
		return []string{"ok up-to-date"}
	}
	ff := -1
	rng := ""
	for i, l := range lines {
		if m := updatingRe.FindStringSubmatch(l); m != nil {
			rng = m[1]
		}
		if l == "Fast-forward" {
			ff = i
			break
		}
	}
	if ff < 0 || rng == "" {
		return nil
	}
	// Everything before "Updating" must be fetch chatter ("From <url>" and
	// ref-update lines), everything after "Fast-forward" the diffstat.
	for _, l := range lines[:ff] {
		if strings.HasPrefix(l, "From ") || strings.HasPrefix(l, "Updating ") || strings.Contains(l, " -> ") {
			continue
		}
		return nil
	}
	out := []string{"ok fast-forward " + rng}
	for _, l := range lines[ff+1:] {
		if modeLineRe.MatchString(l) {
			out = append(out, strings.TrimSpace(l))
			continue
		}
		if strings.Contains(l, " | ") || statSummaryRe.MatchString(l) {
			out = append(out, strings.TrimSpace(l))
			continue
		}
		return nil
	}
	return out
}
