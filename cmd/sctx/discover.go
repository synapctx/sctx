package main

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/synapctx/sctx/internal/adapters/hook"
	"github.com/synapctx/sctx/internal/adapters/stats/sqlite"
	"github.com/synapctx/sctx/internal/application/discover"
	"github.com/synapctx/sctx/internal/domain/stats"
	"github.com/synapctx/sctx/internal/platform/config"
)

const discoverUsage = `usage: sctx discover [--since 7d] [--all] [--project] [--format text|json]`

// discoverFallbackPct is the saved fraction assumed for a program with no local
// history when the store has none at all. It is a round, conservative figure and
// every row that uses it is printed as "est.".
const discoverFallbackPct = 0.5

type discoverFlags struct {
	since  time.Time
	all    bool
	format string
}

func parseDiscoverArgs(args []string) (discoverFlags, error) {
	f := discoverFlags{format: "text"}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--all", "-a":
			f.all = true
		case "--project", "-p":
			f.all = false // the default; accepted for symmetry with `gain`
		case "--since":
			i++
			if i >= len(args) {
				return f, fmt.Errorf("--since requires a value, e.g. --since 7d")
			}
			d, err := parseSince(args[i])
			if err != nil {
				return f, err
			}
			f.since = time.Now().Add(-d)
		case "--format":
			i++
			if i >= len(args) || (args[i] != "text" && args[i] != "json") {
				return f, fmt.Errorf("--format requires text or json")
			}
			f.format = args[i]
		default:
			return f, fmt.Errorf("unknown flag %q", a)
		}
	}
	return f, nil
}

// claudeProjectDirName is how Claude Code names a project's transcript
// directory: the working directory with every non-alphanumeric rune replaced by
// '-' (so "/a/b.c" becomes "-a-b-c").
func claudeProjectDirName(cwd string) string {
	var b strings.Builder
	for _, r := range cwd {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// discoverDirs lists the transcript directories to scan under projectsRoot.
func discoverDirs(projectsRoot, cwd string, all bool) ([]string, error) {
	if !all {
		return []string{filepath.Join(projectsRoot, claudeProjectDirName(cwd))}, nil
	}
	entries, err := os.ReadDir(projectsRoot)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(projectsRoot, e.Name()))
		}
	}
	return dirs, nil
}

func runDiscover(ctx context.Context, cfg config.Config, args []string) int {
	flags, err := parseDiscoverArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: %v\n%s\n", err, discoverUsage)
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: discover: %v\n", err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: discover: %v\n", err)
		return 1
	}
	dirs, err := discoverDirs(filepath.Join(home, ".claude", "projects"), cwd, flags.all)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: discover: no Claude Code transcripts found: %v\n", err)
		return 1
	}

	opts := discover.Options{
		Dirs:  dirs,
		Since: flags.since,
		Classify: func(cmd string) (discover.Kind, string) {
			v, key := hook.Classify(cmd)
			switch v {
			case hook.VerdictWrap:
				return discover.KindWrap, key
			case hook.VerdictGap:
				return discover.KindGap, key
			}
			return discover.KindSkip, ""
		},
	}

	// Local history is optional: without a stats db nothing is created.
	history := map[string]float64{}
	fallback := discoverFallbackPct
	if _, statErr := os.Stat(cfg.StatsDBPath); statErr == nil {
		if store, err := sqlite.NewStore(cfg.StatsDBPath); err == nil {
			if counts, err := store.SessionRunCounts(ctx, flags.since); err == nil {
				opts.Wrapped = counts
			}
			if rep, err := store.Aggregate(ctx, stats.AggregateOptions{}); err == nil {
				for _, c := range rep.ByCommand {
					if c.RawTokens > 0 && c.SavedTokens > 0 {
						history[c.Command] = float64(c.SavedTokens) / float64(c.RawTokens)
					}
				}
			}
			store.Close()
		}
	}

	rep, err := discover.Scan(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: discover: %v\n", err)
		return 1
	}
	rep.ApplySavings(history, fallback)
	rep.Trim(10)

	if flags.format == "json" {
		out, err := json.Marshal(rep)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sctx: discover: %v\n", err)
			return 1
		}
		fmt.Printf("%s\n", out)
		return 0
	}
	renderDiscover(os.Stdout, rep, flags)
	return 0
}

func renderDiscover(w io.Writer, rep discover.Report, flags discoverFlags) {
	scope := "this project"
	if flags.all {
		scope = "all projects"
	}
	window := "all time"
	if !flags.since.IsZero() {
		window = "since " + flags.since.Format("2006-01-02")
	}
	fmt.Fprintf(w, "sctx discover: %s, %s\n", scope, window)
	fmt.Fprintf(w, "%d Bash commands in %d sessions (%d transcripts); %d already wrapped by the hook\n\n",
		rep.BashCommands, rep.Sessions, rep.Files, rep.AlreadyWrapped)

	fmt.Fprintf(w, "Missed savings: %d commands ran without sctx although a rule covers them\n", rep.MissedCount)
	if len(rep.Missed) == 0 {
		fmt.Fprintln(w, "  none")
	} else {
		fmt.Fprintf(w, "  ~%d output tokens, ~%d recoverable (est.)\n", rep.MissedTokens, rep.MissedSaved)
		fmt.Fprintf(w, "  %-28s %6s %10s %10s %8s\n", "command", "runs", "~tokens", "~saved", "saved%")
		for _, r := range rep.Missed {
			mark := ""
			if r.Estimated {
				mark = " est."
			}
			fmt.Fprintf(w, "  %-28s %6d %10d %10d %7.0f%%%s\n", r.Key, r.Count, r.EstTokens, r.EstSavedTokens, r.SavedPct*100, mark)
		}
	}

	fmt.Fprintf(w, "\nUncovered programs: %d commands with no formatter\n", rep.UncoveredCount)
	if len(rep.Uncovered) == 0 {
		fmt.Fprintln(w, "  none")
	} else {
		fmt.Fprintf(w, "  %-28s %6s %10s\n", "program", "runs", "~tokens")
		for _, r := range rep.Uncovered {
			fmt.Fprintf(w, "  %-28s %6d %10d\n", r.Key, r.Count, r.EstTokens)
		}
	}
	fmt.Fprintln(w, "\nToken figures are output bytes/4 as the agent saw them. Read-only and local: nothing was sent anywhere.")
	if rep.MissedCount > 0 {
		fmt.Fprintln(w, "If the hook is not installed, run `sctx setup --install`.")
	}
}
