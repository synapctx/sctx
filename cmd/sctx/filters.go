package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/projectfilter"
)

const filtersUsage = `usage: sctx filters verify | status | trust --yes`

// printProjectFilters is the doctor's line about this checkout's own rules.
//
// It exists because a project filter that is present but NOT trusted is
// invisible: nothing is applied, nothing is printed, and the only symptom is
// output that looks exactly like a command sctx does not cover. Anyone
// debugging that reaches for `sctx doctor` first, so the state belongs here —
// including the approval command, which is the one remedy and must be run by a
// human who has read the file.
func printProjectFilters() {
	wd, err := os.Getwd()
	if err != nil {
		return
	}
	loaded, found, err := projectfilter.LoadFrom(wd)
	switch {
	case err != nil:
		fmt.Printf("project filters: invalid — %v\n", err)
		return
	case !found:
		return
	}
	trustPath, err := projectfilter.DefaultTrustPath()
	if err != nil {
		return
	}
	trusted, err := loaded.Trusted(trustPath)
	if err != nil {
		fmt.Printf("project filters: %s (trust unreadable: %v)\n", loaded.Path, err)
		return
	}
	state := "NOT trusted — run: sctx filters trust --yes"
	if trusted {
		state = "trusted"
	}
	fmt.Printf("project filters: %d in %s (%s)\n", len(loaded.Config.Filters), loaded.Path, state)
}

func runFilters(args []string) int {
	if len(args) == 0 || len(args) > 2 {
		fmt.Fprintln(os.Stderr, filtersUsage)
		return 2
	}
	if (args[0] == "verify" || args[0] == "status") && len(args) != 1 {
		fmt.Fprintln(os.Stderr, filtersUsage)
		return 2
	}
	if args[0] == "trust" && (len(args) != 2 || args[1] != "--yes") {
		fmt.Fprintln(os.Stderr, filtersUsage)
		return 2
	}
	if args[0] != "verify" && args[0] != "status" && args[0] != "trust" {
		fmt.Fprintln(os.Stderr, filtersUsage)
		return 2
	}
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx filters: %v\n", err)
		return 1
	}
	loaded, found, err := projectfilter.LoadFrom(wd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx filters: %v\n", err)
		return 1
	}
	if !found {
		fmt.Fprintf(os.Stderr, "sctx filters: no %s found in this repository\n", projectfilter.RelativePath)
		return 1
	}
	trustPath, err := projectfilter.DefaultTrustPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx filters: %v\n", err)
		return 1
	}
	trusted, err := loaded.Trusted(trustPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx filters: %v\n", err)
		return 1
	}

	switch args[0] {
	case "verify":
		fixtures := 0
		for _, rule := range loaded.Config.Filters {
			fixtures += len(rule.Fixtures)
		}
		fmt.Printf("verified: %s\n", loaded.Path)
		fmt.Printf("digest:   %s\n", loaded.Digest)
		fmt.Printf("filters:  %d (%d inline fixtures)\n", len(loaded.Config.Filters), fixtures)
		fmt.Printf("trusted:  %t\n", trusted)
		return 0
	case "status":
		fmt.Printf("project:  %s\n", loaded.Root)
		fmt.Printf("file:     %s\n", loaded.Path)
		fmt.Printf("digest:   %s\n", loaded.Digest)
		fmt.Printf("trusted:  %t\n", trusted)
		if !trusted {
			fmt.Println("approve:  sctx filters trust --yes")
		}
		return 0
	case "trust":
		ids := make([]string, 0, len(loaded.Config.Filters))
		for _, rule := range loaded.Config.Filters {
			ids = append(ids, rule.ID)
		}
		if err := loaded.Trust(trustPath); err != nil {
			fmt.Fprintf(os.Stderr, "sctx filters: saving trust: %v\n", err)
			return 1
		}
		fmt.Printf("trusted project filters: %s\n", strings.Join(ids, ", "))
		fmt.Printf("digest: %s\n", loaded.Digest)
		fmt.Println("Any edit to .sctx/filters.json invalidates this approval.")
		return 0
	default:
		fmt.Fprintln(os.Stderr, filtersUsage)
		return 2
	}
}
