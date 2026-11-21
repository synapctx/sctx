package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/synapctx/sctx/internal/platform/config"
	"github.com/synapctx/sctx/internal/platform/rawcache"
)

const recallUsage = `usage: sctx recall <id> [--stderr | --all] [--grep RE] [--from N [--lines M]] [--path]
       sctx recall --list`

// runRecall prints cached raw output. It is a read-only native subcommand: it
// never goes through the run pipeline, so its output is verbatim by definition
// and is never compressed, accounted or stored in the raw cache again.
func runRecall(cfg config.Config, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		useErr, all, list, pathOnly bool
		grepExpr                    string
		from, lines                 int
	)
	fs.BoolVar(&useErr, "stderr", false, "")
	fs.BoolVar(&all, "all", false, "")
	fs.BoolVar(&list, "list", false, "")
	fs.BoolVar(&pathOnly, "path", false, "")
	fs.StringVar(&grepExpr, "grep", "", "")
	fs.IntVar(&from, "from", 0, "")
	fs.IntVar(&lines, "lines", 0, "")

	// Flags may follow the id (`sctx recall abc --grep x`), which flag does
	// not do on its own.
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			fmt.Fprintf(stderr, "sctx: recall: %v\n%s\n", err, recallUsage)
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if from < 0 || lines < 0 {
		fmt.Fprintf(stderr, "sctx: recall: --from and --lines must not be negative\n")
		return 2
	}
	var re *regexp.Regexp
	if grepExpr != "" {
		var err error
		if re, err = regexp.Compile(grepExpr); err != nil {
			fmt.Fprintf(stderr, "sctx: recall: invalid --grep: %v\n", err)
			return 2
		}
	}

	cache := rawcache.New(cfg.RawCacheDir, cfg.RawCacheTTL, cfg.RawCacheMaxBytes)
	if list {
		return recallList(cache, stdout, stderr)
	}
	if len(positional) != 1 {
		fmt.Fprintf(stderr, "sctx: recall: expected one id\n%s\n", recallUsage)
		return 2
	}
	info, err := cache.Lookup(positional[0])
	if err != nil {
		if errors.Is(err, rawcache.ErrNotFound) {
			fmt.Fprintf(stderr, "sctx: recall: %s: %v (see `sctx recall --list`)\n", positional[0], err)
		} else {
			fmt.Fprintf(stderr, "sctx: recall: %v\n", err)
		}
		return 1
	}
	if pathOnly {
		fmt.Fprintln(stdout, info.Path)
		return 0
	}

	streams := []string{"stdout"}
	switch {
	case all:
		streams = []string{"stdout", "stderr"}
	case useErr:
		streams = []string{"stderr"}
	}
	code := 0
	for _, name := range streams {
		f, err := os.Open(filepath.Join(info.Path, name))
		if err != nil {
			if os.IsNotExist(err) && name == "stderr" {
				// A command with no stderr stores no stderr file.
				if !all {
					fmt.Fprintf(stderr, "sctx: recall: %s has no stderr\n", info.ID)
					code = 1
				}
				continue
			}
			fmt.Fprintf(stderr, "sctx: recall: %v\n", err)
			return 1
		}
		if all {
			fmt.Fprintf(stdout, "== %s ==\n", name)
		}
		err = recallStream(stdout, f, re, from, lines)
		f.Close()
		if err != nil {
			fmt.Fprintf(stderr, "sctx: recall: reading %s: %v\n", name, err)
			return 1
		}
	}
	return code
}

// recallStream copies r to w. With no filter it is a byte-exact copy. Otherwise
// lines are numbered from 1; --from/--lines select a window and --grep keeps
// matching lines of that window, printed as `N:line`.
func recallStream(w io.Writer, r io.Reader, re *regexp.Regexp, from, lines int) error {
	if re == nil && from == 0 && lines == 0 {
		_, err := io.Copy(w, r)
		return err
	}
	if from == 0 {
		from = 1
	}
	br := bufio.NewReader(r)
	for n := 1; ; n++ {
		if lines > 0 && n >= from+lines {
			return nil
		}
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && n >= from {
			if re == nil {
				if _, werr := w.Write(line); werr != nil {
					return werr
				}
			} else if re.Match(bytes.TrimSuffix(line, []byte("\n"))) {
				if _, werr := fmt.Fprintf(w, "%d:%s", n, line); werr != nil {
					return werr
				}
				if line[len(line)-1] != '\n' {
					fmt.Fprintln(w)
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func recallList(cache *rawcache.Cache, stdout, stderr io.Writer) int {
	entries, err := cache.List()
	if err != nil {
		fmt.Fprintf(stderr, "sctx: recall: %v\n", err)
		return 1
	}
	if len(entries) == 0 {
		fmt.Fprintln(stderr, "sctx: recall: no cached raw output")
		return 0
	}
	now := cache.Now()
	fmt.Fprintf(stdout, "%-12s  %-8s  %-16s  %10s  %s\n", "ID", "AGE", "PROGRAM", "BYTES", "TTL LEFT")
	for _, e := range entries {
		prog := e.Program
		if prog == "" {
			prog = "-"
		}
		fmt.Fprintf(stdout, "%-12s  %-8s  %-16s  %10d  %s\n", e.ID,
			shortDur(now.Sub(e.Stored)), prog, e.Bytes, shortDur(e.Expires.Sub(now)))
	}
	return 0
}

func shortDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
