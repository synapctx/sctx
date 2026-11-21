package fs

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// noiseDirs are directory names whose contents are almost never what a
// listing is looking for (dependency caches, VCS metadata, build output,
// agent worktrees). A listing that reaches inside one collapses it into a
// single counted line instead of printing thousands of paths.
var noiseDirs = map[string]bool{
	"node_modules": true, ".git": true, "vendor": true, "target": true,
	".claude": true, "__pycache__": true, ".venv": true, ".mypy_cache": true,
	".pytest_cache": true, ".next": true, ".gradle": true, ".terraform": true,
	".idea": true, ".cache": true,
}

// noiseGroup is one collapsed noise directory.
type noiseGroup struct {
	dir   string // cleaned path of the noise directory itself
	paths int
	dirs  map[string]struct{}
}

// findRoots extracts the starting points named in a `find` argv: everything
// between the program (and its leading -H/-L/-P/-D/-O options) and the first
// expression token. It defaults to ".".
func findRoots(argv []string) []string {
	var roots []string
	i := 1
	for i < len(argv) && strings.HasPrefix(argv[i], "-") && len(argv[i]) == 2 {
		i++
	}
	for ; i < len(argv); i++ {
		a := argv[i]
		if strings.HasPrefix(a, "-") || a == "(" || a == "!" || a == ")" {
			break
		}
		roots = append(roots, path.Clean(a))
	}
	if len(roots) == 0 {
		roots = []string{"."}
	}
	return roots
}

// noiseKey returns the cleaned path of the noise directory p lies STRICTLY
// inside, or "" when p is not inside one. A noise directory the user named as
// a search root (or that lies under one) is never collapsed: asking `find
// node_modules -name x` is explicit intent. p itself being a noise-named entry
// is shown as a normal path.
func noiseKey(p string, roots []string) string {
	p = path.Clean(p)
	parts := strings.Split(p, "/")
	for i := 0; i < len(parts)-1; i++ {
		if !noiseDirs[parts[i]] {
			continue
		}
		key := strings.Join(parts[:i+1], "/")
		for _, r := range roots {
			if r == key || strings.HasPrefix(r, key+"/") {
				return ""
			}
		}
		return key
	}
	return ""
}

// splitNoise separates indexes of paths that live inside noise directories.
// It returns the groups sorted by directory, and for each input index whether
// it was absorbed.
func splitNoise(paths []string, roots []string) (groups []*noiseGroup, absorbed []bool) {
	absorbed = make([]bool, len(paths))
	byKey := map[string]*noiseGroup{}
	for i, p := range paths {
		k := noiseKey(p, roots)
		if k == "" {
			continue
		}
		g := byKey[k]
		if g == nil {
			g = &noiseGroup{dir: k, dirs: map[string]struct{}{}}
			byKey[k] = g
		}
		g.paths++
		g.dirs[path.Dir(path.Clean(p))] = struct{}{}
		absorbed[i] = true
	}
	for _, g := range byKey {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].dir < groups[j].dir })
	return groups, absorbed
}

// noiseLines renders one counted marker per collapsed noise directory.
func noiseLines(groups []*noiseGroup) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		out = append(out, fmt.Sprintf("%s/ …+%d paths in %d dirs (noise dir collapsed; run find inside it to list)", g.dir, g.paths, len(g.dirs)))
	}
	return out
}
