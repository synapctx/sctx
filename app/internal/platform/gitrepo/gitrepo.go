// Package gitrepo detects the "org/repo" identity of the git repository
// containing a directory, purely by parsing on-disk git metadata (no
// shelling out to git) so telemetry can attribute events per repository
// without adding startup latency.
package gitrepo

import (
	"os"
	"path/filepath"
	"strings"
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
		gitPath := filepath.Join(current, ".git")
		if info, err := os.Stat(gitPath); err == nil {
			gitDir := gitPath
			if !info.IsDir() {
				resolved, found := resolveGitFile(gitPath)
				if !found {
					return "", "", false
				}
				gitDir = resolved
			}
			url := parseOriginURL(filepath.Join(gitDir, "config"))
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
	for _, line := range strings.Split(string(data), "\n") {
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
	idx := strings.Index(line, "=")
	if idx < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
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
