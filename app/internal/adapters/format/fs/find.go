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
	findNamesPerDirCap = 10
	findDirsCap        = 60
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

	var body, note string
	if entries, ok := parseFindLsLines(lines); ok {
		body, note = renderFindLs(entries)
	} else {
		body, note = renderFindPaths(lines)
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
func renderFindPaths(paths []string) (body string, note string) {
	byDir := make(map[string][]string)
	for _, p := range paths {
		dir := path.Dir(p)
		byDir[dir] = append(byDir[dir], path.Base(p))
	}
	return renderFindGroups(byDir, len(paths))
}

// renderFindGroups renders a dir -> labels grouping shared by the plain and
// -ls rendering paths, applying findDirsCap / findNamesPerDirCap.
func renderFindGroups(byDir map[string][]string, total int) (body string, note string) {
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var out []string
	for i, d := range dirs {
		if i >= findDirsCap {
			remainingDirs := len(dirs) - findDirsCap
			remainingPaths := 0
			for _, dd := range dirs[findDirsCap:] {
				remainingPaths += len(byDir[dd])
			}
			out = append(out, fmt.Sprintf("…+%d more dirs (%d paths)", remainingDirs, remainingPaths))
			break
		}

		names := byDir[d]
		shown := names
		extra := 0
		if len(names) > findNamesPerDirCap {
			extra = len(names) - findNamesPerDirCap
			shown = names[:findNamesPerDirCap]
		}
		line := fmt.Sprintf("%s/ (%d): %s", d, len(names), strings.Join(shown, ", "))
		if extra > 0 {
			line += fmt.Sprintf(", …+%d more files in %s", extra, d)
		}
		out = append(out, line)
	}

	return strings.Join(out, "\n"), fmt.Sprintf("%d paths", total)
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
func renderFindLs(entries []findLsEntry) (body string, note string) {
	byDir := make(map[string][]string)
	for _, e := range entries {
		dir := path.Dir(e.path)
		label := fmt.Sprintf("%s (%s)", path.Base(e.path), e.size)
		byDir[dir] = append(byDir[dir], label)
	}
	return renderFindGroups(byDir, len(entries))
}

func (f *findFormatter) Relaxed(ctx context.Context, in format.Input) (format.Rendered, error) {
	return relaxedRender(ctx, in)
}
