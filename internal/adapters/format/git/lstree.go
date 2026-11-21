package git

import (
	"regexp"

	"github.com/synapctx/sctx/internal/domain/format"
)

// lsTreeRe matches the default `git ls-tree` record:
// "<mode> SP <type> SP <object> TAB <path>".
var lsTreeRe = regexp.MustCompile(`^(\d{6}) (blob|tree|commit) [0-9a-f]{40,64}\t(.+)$`)

// aggressiveLsTree renders the default `git ls-tree` listing as paths grouped
// by directory. Object ids are dropped (stated in the legend); modes other
// than a plain file are kept as a suffix on the name: "*" executable, "@"
// symlink, "!" submodule.
func aggressiveLsTree(in format.Input, args []string) (format.Rendered, error) {
	if hasCustomLineFormat(args) || hasAnyArg(args, "-z", "-l", "--long", "--name-only", "--name-status", "--object-only") {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	raw := readAll(in.Stdout)
	lines := nonEmptyLines(splitLines(raw))
	if len(lines) == 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	paths := make([]string, 0, len(lines))
	for _, l := range lines {
		m := lsTreeRe.FindStringSubmatch(l)
		if m == nil {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		name := m[3]
		if m[2] == "tree" {
			// A non-recursive listing of directories is short and its "dir/"
			// entries would not survive per-directory grouping.
			return format.Rendered{}, format.ErrTierInapplicable
		}
		switch {
		case m[1] == "100755":
			name += "*"
		case m[1] == "120000":
			name += "@"
		case m[2] == "commit":
			name += "!"
		}
		paths = append(paths, name)
	}
	return renderPathsByDir(paths, raw, "(object ids and mode omitted; * executable, @ symlink, ! submodule)")
}
