package git

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// lsFilesNamesPerDirCap and lsFilesDirsCap bound how many file names per
// directory, and how many directories, `git ls-files` output is grouped
// into (mirrors the `find` formatter's grouping strategy).
const (
	lsFilesNamesPerDirCap = 10
	lsFilesDirsCap        = 60
)

// aggressiveLsFiles groups `git ls-files` output (one tracked path per
// line, repo-root relative) by directory, capping both the number of names
// shown per directory and the number of directories shown.
func aggressiveLsFiles(in format.Input) (format.Rendered, error) {
	raw := readAll(in.Stdout)
	paths := nonEmptyLines(splitLines(raw))
	if len(paths) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	byDir := make(map[string][]string)
	for _, p := range paths {
		dir := path.Dir(p)
		byDir[dir] = append(byDir[dir], path.Base(p))
	}

	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var out []string
	for i, d := range dirs {
		if i >= lsFilesDirsCap {
			remainingDirs := len(dirs) - lsFilesDirsCap
			remainingPaths := 0
			for _, dd := range dirs[lsFilesDirsCap:] {
				remainingPaths += len(byDir[dd])
			}
			out = append(out, fmt.Sprintf("…+%d more dirs (%d paths)", remainingDirs, remainingPaths))
			break
		}

		names := byDir[d]
		shown := names
		extra := 0
		if len(names) > lsFilesNamesPerDirCap {
			extra = len(names) - lsFilesNamesPerDirCap
			shown = names[:lsFilesNamesPerDirCap]
		}
		line := fmt.Sprintf("%s/ (%d): %s", d, len(names), strings.Join(shown, ", "))
		if extra > 0 {
			line += fmt.Sprintf(", …+%d", extra)
		}
		out = append(out, line)
	}

	body := strings.Join(out, "\n")
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(body), Note: fmt.Sprintf("%d files", len(paths))}, nil
}
