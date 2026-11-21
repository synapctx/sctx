package fs

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

type findFormatter struct{}

func (f *findFormatter) Descriptor() format.Match {
	return format.Match{Command: "find"}
}

const (
	// findNamesPerDirCap is the largest directory whose file names are listed.
	findNamesPerDirCap = 4
	// findNamesBudget is the total number of file names listed across the
	// whole output; beyond it directories are count-only.
	findNamesBudget = 40
	// findDirsCap bounds the directory lines for pathological trees.
	findDirsCap = 400
	// findMinLines guards against compressing trivially small output: below
	// this many lines, grouping by directory adds more noise than it saves.
	findMinLines = 4
)

func (f *findFormatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	raw, err := readStdout(in)
	if err != nil {
		return format.Rendered{}, err
	}
	lines := nonEmptyLines(splitLines(raw))
	if len(lines) < findMinLines {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	roots := findRoots(in.Argv)
	var body, note string
	if entries, ok := parseFindLsLines(lines); ok {
		body, note = renderFindLs(entries, roots)
	} else {
		body, note = renderFindPaths(lines, roots)
	}

	if !shrunk(raw, body) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: note}, nil
}

// renderFindPaths groups bare `find` paths (one per line) by parent
// directory, capping both the number of directories shown and the number of
// names listed per directory, with explicit "…+N" markers for anything
// elided.
func renderFindPaths(paths []string, roots []string) (body string, note string) {
	noise, absorbed := splitNoise(paths, roots)
	byDir := make(map[string][]string)
	for i, p := range paths {
		if absorbed[i] {
			continue
		}
		dir := path.Dir(p)
		byDir[dir] = append(byDir[dir], path.Base(p))
	}
	return renderFindGroups(byDir, len(paths), noise)
}

// renderFindGroups renders a dir -> labels grouping shared by the plain and
// -ls rendering paths. Every directory that holds a match appears with its
// count, with directories sharing a parent folded under one "parent/:" header
// so common prefixes are printed once. File names are listed only for small
// directories (<= findNamesPerDirCap) and only until findNamesBudget names have
// been spent across the whole listing; everything else is count-only and the
// omitted names are counted in a trailing "…+N" line. At most findDirsCap
// directories are shown; the remainder is counted, never dropped silently.
func renderFindGroups(byDir map[string][]string, total int, noise []*noiseGroup) (body string, note string) {
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var out []string
	budget := findNamesBudget
	omitted := 0
	curParent := ""
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, curParent+": "+strings.Join(cur, ", "))
			cur = nil
		}
	}
	for i, d := range dirs {
		if i >= findDirsCap {
			flush()
			remainingPaths := 0
			for _, dd := range dirs[findDirsCap:] {
				remainingPaths += len(byDir[dd])
			}
			out = append(out, fmt.Sprintf("…+%d more dirs (%d paths)", len(dirs)-findDirsCap, remainingPaths))
			break
		}
		names := byDir[d]
		parent, leaf := path.Dir(d), path.Base(d)
		if d == "." {
			parent, leaf = ".", "."
		}
		if pp := parent + "/"; pp != curParent {
			flush()
			curParent = pp
		}
		entry := fmt.Sprintf("%s %d", leaf, len(names))
		if len(names) <= findNamesPerDirCap && len(names) <= budget {
			budget -= len(names)
			entry += " [" + strings.Join(names, ", ") + "]"
		} else {
			omitted += len(names)
		}
		cur = append(cur, entry)
	}
	flush()
	if omitted > 0 {
		out = append(out, fmt.Sprintf("…+%d file names not listed (counts shown per dir)", omitted))
	}
	out = append(out, noiseLines(noise)...)
	header := fmt.Sprintf("%d paths in %d dirs", total, len(dirs))
	if len(noise) > 0 {
		header = fmt.Sprintf("%d paths in %d dirs (+%d noise dirs collapsed)", total, len(dirs), len(noise))
	}
	return header + "\n" + strings.Join(out, "\n"), fmt.Sprintf("%d paths", total)
}

// findLsEntry is one parsed `find -ls` line.
type findLsEntry struct {
	path string
	size string
}

// parseFindLsLines parses every line as `find -ls` long-listing output
// (inode, blocks, permissions, links, owner, group, size, month, day, time,
// path). ok is false if any non-blank line fails to match the shape, so the
// caller can fall back to plain path grouping.
func parseFindLsLines(lines []string) (entries []findLsEntry, ok bool) {
	entries = make([]findLsEntry, 0, len(lines))
	for _, l := range lines {
		e, parsed := parseFindLsLine(l)
		if !parsed {
			return nil, false
		}
		entries = append(entries, e)
	}
	return entries, true
}

// parseFindLsLine parses one `find -ls` data line. It reuses ls.go's
// permRe to identify the permissions column, which sits at field index 2
// (after inode and block count).
func parseFindLsLine(line string) (findLsEntry, bool) {
	fields := strings.Fields(line)
	if len(fields) < 11 {
		return findLsEntry{}, false
	}
	if !permRe.MatchString(fields[2]) {
		return findLsEntry{}, false
	}
	return findLsEntry{
		path: strings.Join(fields[10:], " "),
		size: fields[6],
	}, true
}

// renderFindLs compacts `find -ls` output to a grouped "path (size)"
// listing per directory, dropping the inode/blocks/links/owner/group/date
// noise that -ls carries on every line.
func renderFindLs(entries []findLsEntry, roots []string) (body string, note string) {
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.path
	}
	noise, absorbed := splitNoise(paths, roots)
	byDir := make(map[string][]string)
	for i, e := range entries {
		if absorbed[i] {
			continue
		}
		dir := path.Dir(e.path)
		label := fmt.Sprintf("%s (%s)", path.Base(e.path), e.size)
		byDir[dir] = append(byDir[dir], label)
	}
	return renderFindGroups(byDir, len(entries), noise)
}

func (f *findFormatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	return relaxedRender(ctx, in)
}
