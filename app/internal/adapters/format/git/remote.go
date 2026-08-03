package git

import (
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// remoteVerboseCollapseThreshold is the line count under which `git remote
// -v` output is already small enough that structured collapsing adds
// nothing.
const remoteVerboseCollapseThreshold = 4

// remoteCap is the number of remotes kept before eliding the rest.
const remoteCap = 20

// aggressiveRemote handles `git remote -v` (two lines per remote: fetch and
// push URL), deduplicating the fetch/push pair into a single line per
// remote when they share a URL. Plain `git remote` (bare remote names) and
// small `-v` output are left alone.
func aggressiveRemote(in format.Input, args []string) (format.Rendered, error) {
	if !hasVerboseFlag(args) {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	raw := readAll(in.Stdout)
	lines := nonEmptyLines(splitLines(raw))
	if len(lines) <= remoteVerboseCollapseThreshold {
		return format.Rendered{}, format.ErrTierInapplicable
	}

	type urls struct{ fetch, push string }
	order := make([]string, 0)
	byName := make(map[string]*urls)

	for _, l := range lines {
		fields := strings.Fields(l)
		if len(fields) < 3 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		name, url, kind := fields[0], fields[1], strings.Trim(fields[2], "()")
		u, ok := byName[name]
		if !ok {
			u = &urls{}
			byName[name] = u
			order = append(order, name)
		}
		switch kind {
		case "fetch":
			u.fetch = url
		case "push":
			u.push = url
		default:
			return format.Rendered{}, format.ErrTierInapplicable
		}
	}

	shownNames := order
	extra := 0
	if len(order) > remoteCap {
		extra = len(order) - remoteCap
		shownNames = order[:remoteCap]
	}

	var out []string
	for _, name := range shownNames {
		u := byName[name]
		if u.fetch != "" && u.fetch == u.push {
			out = append(out, fmt.Sprintf("%s %s", name, u.fetch))
		} else {
			out = append(out, fmt.Sprintf("%s fetch=%s push=%s", name, u.fetch, u.push))
		}
	}
	if extra > 0 {
		out = append(out, fmt.Sprintf("…+%d more remotes", extra))
	}

	body := strings.Join(out, "\n")
	if len(body) >= len(raw) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{
		Body: []byte(body),
		Note: fmt.Sprintf("%d remotes (%d shown)", len(order), len(shownNames)),
	}, nil
}

// hasVerboseFlag reports whether -v/--verbose is present in a git subcommand
// argument list.
func hasVerboseFlag(args []string) bool {
	for _, a := range args {
		if a == "-v" || a == "--verbose" {
			return true
		}
	}
	return false
}
