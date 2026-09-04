// Package gitrepo detects the "org/repo" identity of the git repository
// containing a directory, purely by parsing on-disk git metadata (no
// shelling out to git) so telemetry can attribute events per repository
// without adding startup latency.
package gitrepo

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Detect walks up from dir looking for a .git entry, resolves it (handling
// the worktree ".git file" indirection), and returns the "org/repo" form of
// the "origin" remote URL. It returns "" on any failure — no git, no
// origin, unparseable URL — and never errors.
func Detect(dir string) string {
	_, name, _ := RootAndName(dir)
	return name
}

// RootAndName is Detect plus the working-tree ROOT the repository was found at.
//
// The root exists because a repository-relative path is the only stable way to
// name a file across machines: an absolute path is one developer's checkout, and
// a path relative to the agent's working directory stops matching the moment the
// agent changes directory mid-session. Everything server-side indexes files by
// their repository-relative path, so this is what makes a local edit joinable to
// what the organization knows.
func RootAndName(dir string) (root, name string, ok bool) {
	current := dir
	for {
		if _, gitErr := os.Stat(filepath.Join(current, ".git")); gitErr == nil {
			_, commonDir, found := GitDirs(current)
			if !found {
				return "", "", false
			}
			// Read from the COMMON dir: a linked worktree's own git directory
			// holds HEAD and little else — `config`, and therefore the origin
			// remote, lives once in the directory shared by every worktree.
			url := parseOriginURL(filepath.Join(commonDir, "config"))
			if url == "" {
				return "", "", false
			}
			return current, normalizeURL(url), true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", "", false
		}
		current = parent
	}
}

// GitDirs resolves the two directories a caller needs to read git state for a
// working-tree root: the git directory FOR THIS TREE (which holds HEAD) and the
// COMMON directory shared with every linked worktree (which holds config, refs
// and packed-refs).
//
// They are the same path in the ordinary case and differ in exactly the two
// layouts where `.git` is a FILE rather than a directory:
//
//   - a linked worktree, where `.git` names `<main>/.git/worktrees/<name>` and a
//     `commondir` file beside HEAD points back at the shared directory;
//   - a submodule, where `.git` names `<super>/.git/modules/<name>`, which is a
//     complete git directory with no `commondir` at all.
//
// Exported because a caller that hard-codes `<root>/.git` silently reads nothing
// in both layouts — and a worktree is precisely the checkout whose HEAD is least
// likely to match what the index already holds.
func GitDirs(root string) (gitDir, commonDir string, ok bool) {
	gitPath := filepath.Join(root, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", "", false
	}
	gitDir = gitPath
	if !info.IsDir() {
		resolved, found := resolveGitFile(gitPath)
		if !found {
			return "", "", false
		}
		gitDir = resolved
	}
	return gitDir, resolveCommonDir(gitDir), true
}

// resolveCommonDir reads the `commondir` file beside HEAD. Its contents are
// usually the relative "../.." back to the shared git directory; an absolute
// path is also legal. No file means this IS the common directory.
func resolveCommonDir(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	target := strings.TrimSpace(string(data))
	if target == "" {
		return gitDir
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(gitDir, target)
	}
	return filepath.Clean(target)
}

// resolveGitFile reads a worktree ".git" file (contents: "gitdir: <path>")
// and returns the absolute directory it points to.
func resolveGitFile(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	content := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(content, prefix) {
		return "", false
	}
	target := strings.TrimSpace(content[len(prefix):])
	if target == "" {
		return "", false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return target, true
}

// parseOriginURL scans a git config file for the "url" key inside the
// [remote "origin"] section.
func parseOriginURL(configPath string) string {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	inOrigin := false
	for line := range strings.SplitSeq(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inOrigin = isOriginHeader(trimmed)
			continue
		}
		if !inOrigin {
			continue
		}
		key, value, ok := splitKV(trimmed)
		if ok && key == "url" {
			return value
		}
	}
	return ""
}

// isOriginHeader reports whether line is the `[remote "origin"]` section
// header.
func isOriginHeader(line string) bool {
	inner := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
	parts := strings.SplitN(inner, " ", 2)
	if len(parts) != 2 {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(parts[0]), "remote") {
		return false
	}
	name := strings.Trim(strings.TrimSpace(parts[1]), `"`)
	return name == "origin"
}

// splitKV splits a "key = value" config line.
func splitKV(line string) (string, string, bool) {
	before, after, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(before), strings.TrimSpace(after), true
}

// ChildRepo is one repository found immediately under a workspace root by
// ChildRepos.
type ChildRepo struct {
	// Name is the "org/repo" form of its origin remote.
	Name string
	// Root is the child directory itself.
	Root string
	// IndexModTime is the mtime of `.git/index` (or the linked worktree's own
	// index, via GitDirs) — the freshest available signal for "worked on
	// recently" without shelling out to git or reading history.
	IndexModTime time.Time
}

// ChildRepos scans cwd's IMMEDIATE child directories for one containing
// `.git`, for the one case RootAndName cannot answer: a workspace root that
// is not itself inside a repository, but holds several checkouts side by
// side (proactive guidance v2, plan item B5). It never walks deeper than one
// level and never walks UP — RootAndName already owns that direction.
//
// Two bounds keep this safe to call from a session's startup path on an
// arbitrarily large directory (a home directory, say): at most maxEntries
// directory entries are inspected, and the scan stops the instant it has run
// for wallBudget, in either case simply returning what it already found
// rather than erroring — a partial answer here is exactly as good as a full
// one, since the caller only wants "the busiest few". Sorted busiest first
// (index mtime, descending) so a caller taking the top N gets the checkouts
// most likely to be the ones actually in use.
func ChildRepos(cwd string, maxEntries int, wallBudget time.Duration) []ChildRepo {
	start := time.Now()
	entries, err := os.ReadDir(cwd)
	if err != nil {
		return nil
	}

	var out []ChildRepo
	for i, e := range entries {
		if i >= maxEntries || time.Since(start) > wallBudget {
			break
		}
		if !e.IsDir() {
			continue
		}
		child := filepath.Join(cwd, e.Name())
		if _, err := os.Stat(filepath.Join(child, ".git")); err != nil {
			continue
		}
		gitDir, commonDir, ok := GitDirs(child)
		if !ok {
			continue
		}
		name := normalizeURL(parseOriginURL(filepath.Join(commonDir, "config")))
		if name == "" {
			continue
		}
		var mtime time.Time
		if info, err := os.Stat(filepath.Join(gitDir, "index")); err == nil {
			mtime = info.ModTime()
		}
		out = append(out, ChildRepo{Name: name, Root: child, IndexModTime: mtime})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].IndexModTime.After(out[j].IndexModTime) })
	return out
}

// normalizeURL reduces a git remote URL (https, ssh scp-like, or ssh://) to
// its "org/repo" form.
func normalizeURL(raw string) string {
	url := strings.TrimSpace(raw)
	url = strings.TrimSuffix(url, "/")
	url = strings.TrimSuffix(url, ".git")

	var path string
	switch {
	case strings.Contains(url, "://"):
		rest := url[strings.Index(url, "://")+3:]
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return ""
		}
		path = rest[slash+1:]
	case strings.Contains(url, "@") && strings.Contains(url, ":"):
		path = url[strings.Index(url, ":")+1:]
	default:
		return ""
	}

	path = strings.Trim(path, "/")
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return ""
	}
	org, repo := parts[len(parts)-2], parts[len(parts)-1]
	if org == "" || repo == "" {
		return ""
	}
	return org + "/" + repo
}
