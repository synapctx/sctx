package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/synapctx/sctx/internal/platform/config"
)

// helperBinary is the workspace daemon, launched as a child process.
//
// WHY A SEPARATE BINARY. `sctx` is free and PUBLIC: anyone must be able to clone
// it and run `go build`, `go test` and `go mod tidy`. Importing the daemon put
// two PRIVATE modules into this module's graph and broke `go mod tidy` for
// everyone outside the SynapCTX machines — on the one repository that has to work
// for strangers. Publishing those modules is not an option either: the shared Go
// extractor also carries the HTTP endpoint canonicalisation and service-identity
// logic that `find_unused_endpoints` is built on.
//
// Splitting at the process boundary costs nothing real. `watch` requires a
// SynapCTX API key, so a build from public source with no key could never have
// used it; and both binaries ship in one release archive, so an installed
// customer still runs one command and installs one thing.
const helperBinary = "sctxd"

// runWatch keeps UNCOMMITTED code queryable by launching the workspace daemon.
//
// Without it the index is guaranteed WRONG about exactly the code the developer
// is changing — the acute form of staleness, and the one capability a
// repository-local graph structurally cannot copy.
//
// DELIBERATELY FOREGROUND: running it IS the consent. `sctx setup` installs
// nothing, nothing auto-starts, and stopping the command stops the streaming.
// Code is more sensitive than the command telemetry the rest of sctx collects, so
// it gets a stricter posture rather than the same one.
func runWatch(ctx context.Context, cfg config.Config, args []string) int {
	roots, showHelp := parseWatchArgs(args)
	if showHelp {
		printWatchUsage()
		return 0
	}
	if len(roots) == 0 {
		roots = defaultWatchRoots()
	}
	if len(roots) == 0 {
		fmt.Fprintln(os.Stderr, "sctx watch: no workspace root found. Pass --root <dir>.")
		fmt.Fprintln(os.Stderr, "  A root CONTAINS organization directories: <root>/<org>/<repo>.")
		return 2
	}

	// AN API KEY IS THE PRECONDITION, not a paywall. Everything else in sctx works
	// with no account: wrapping, savings, the agent hooks. `watch` is the one
	// capability that inherently cannot, because it STREAMS YOUR WORKING TREE —
	// with no key there is no organization to send it to and no boundary that owns
	// it, so refusing beats watching files and posting nothing.
	if token, _ := cfg.TokenForOrg(""); token == "" {
		fmt.Fprintln(os.Stderr, "sctx watch: needs a SynapCTX API key.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  Everything else in sctx works without one — this is the only command")
		fmt.Fprintln(os.Stderr, "  that sends your code anywhere, so it needs an organization to send it")
		fmt.Fprintln(os.Stderr, "  to. Run `sctx init` with a key from your SynapCTX console.")
		return 2
	}

	// Fail CLOSED on a proxy that was never configured. Pushing at loopback
	// forever would report nothing, which is exactly what a healthy quiet
	// workspace looks like.
	proxy := strings.TrimSpace(cfg.WorkspaceProxyURL)
	if proxy == "" || (isRemote(cfg.TelemetryEndpoint) && !isRemote(proxy)) {
		fmt.Fprintln(os.Stderr, "sctx watch: no workspace proxy configured.")
		fmt.Fprintln(os.Stderr, "  The workspace routes are served on the MCP host, which is a different")
		fmt.Fprintln(os.Stderr, "  host from telemetry ingest, so it cannot be inferred from your endpoint.")
		fmt.Fprintf(os.Stderr, "  Add to %s:\n\n", cfg.ConfigFilePath)
		fmt.Fprintln(os.Stderr, "    workspace_proxy_url = \"https://mcp.<your-synapctx-host>\"")
		fmt.Fprintln(os.Stderr, "\n  or set SCT__WORKSPACE_PROXY_URL.")
		return 2
	}

	helper, err := findHelper()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx watch: %v\n", err)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  `watch` runs a helper binary that ships beside sctx in the official")
		fmt.Fprintln(os.Stderr, "  release. Everything else in sctx works without it.")
		fmt.Fprintln(os.Stderr, "  If you built sctx from source, install the released archive instead,")
		fmt.Fprintf(os.Stderr, "  or point SCT__WATCH_HELPER at a %s you built.\n", helperBinary)
		return 2
	}

	startup, err := json.Marshal(watchStartup(cfg, roots))
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx watch: preparing configuration: %v\n", err)
		return 1
	}

	printWatchPosture(roots, proxy)

	cmd := exec.CommandContext(ctx, helper)
	// API KEYS GO ON STDIN, never argv: argv is visible in `ps` for the life of
	// the process. Same reason `sctx init` refuses a --key flag.
	cmd.Stdin = strings.NewReader(string(startup))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			// The helper already explained itself on stderr; do not editorialise.
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "sctx watch: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "sctx watch: stopped. Your uncommitted code is no longer being shared.")
	return 0
}

// watchStartup is the configuration handed to the helper. sctx owns config
// resolution — precedence lives in ONE place — and the helper is told the
// answers rather than working them out again from the same files.
func watchStartup(cfg config.Config, roots []string) map[string]any {
	hostname, _ := os.Hostname()
	tokens := map[string]string{}
	for org := range cfg.OrgTokens {
		if t, ok := cfg.TokenForOrg(org); ok && t != "" {
			tokens[org] = t
		}
	}
	// A single-key configuration (env override, or a pre-multi-org file) has no
	// per-org sections. Resolve the default so those installs still work.
	if len(tokens) == 0 {
		if t, ok := cfg.TokenForOrg(""); ok && t != "" {
			tokens[cfg.DefaultOrg] = t
		}
	}
	return map[string]any{
		"proxyUrl":   strings.TrimRight(cfg.WorkspaceProxyURL, "/"),
		"roots":      roots,
		"hostname":   hostname,
		"stateDir":   filepath.Join(filepath.Dir(cfg.ConfigFilePath), "workspace"),
		"orgTokens":  tokens,
		"defaultOrg": cfg.DefaultOrg,
		"debug":      cfg.DebugLoggingEnabled,
	}
}

// findHelper locates the daemon binary.
//
// Beside sctx FIRST, and that ordering is deliberate: the release archive ships
// both together, so a matched pair is the common case and PATH could otherwise
// serve an older copy from a previous install.
func findHelper() (string, error) {
	if override := strings.TrimSpace(os.Getenv("SCT__WATCH_HELPER")); override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("SCT__WATCH_HELPER points at %s, which is not there", override)
		}
		return override, nil
	}
	if self, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			self = resolved
		}
		candidate := filepath.Join(filepath.Dir(self), helperBinary)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	if found, err := exec.LookPath(helperBinary); err == nil {
		return found, nil
	}
	return "", fmt.Errorf("%s not found beside sctx or on PATH", helperBinary)
}

func parseWatchArgs(args []string) (roots []string, help bool) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help", "help":
			return nil, true
		case "--root":
			if i+1 < len(args) {
				i++
				roots = append(roots, args[i])
			}
		default:
			roots = append(roots, args[i])
		}
	}
	return roots, false
}

// defaultWatchRoots is the conventional checkout layout. NOTE THE SHAPE: a root
// CONTAINS organization directories (`<root>/<org>/<repo>`), it is not itself an
// organization directory. Pointing it one level too deep finds nothing — and says
// nothing about it, which cost real diagnosis time.
func defaultWatchRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var found []string
	for _, dir := range []string{
		filepath.Join(home, "git", "github.com"),
		filepath.Join(home, "src", "github.com"),
		filepath.Join(home, "go", "src", "github.com"),
	} {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			found = append(found, dir)
		}
	}
	return found
}

// printWatchPosture states what leaves the machine, every time, before anything
// does — and it lives in the PUBLIC binary on purpose, so the claim can be
// audited against the source by anyone who cares to.
func printWatchPosture(roots []string, proxy string) {
	fmt.Fprintln(os.Stderr, "sctx watch — keeping your UNCOMMITTED code queryable")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  What is sent:  symbol names, signatures, doc comments and content HASHES")
	fmt.Fprintln(os.Stderr, "  What is NOT:   function bodies, file contents, anything .gitignored")
	fmt.Fprintln(os.Stderr, "  Who can see:   only you — overlays are per-developer, never shared with")
	fmt.Fprintln(os.Stderr, "                 teammates, and expire automatically")
	fmt.Fprintln(os.Stderr, "  Stops when:    you stop this command")
	fmt.Fprintln(os.Stderr, "")
	for _, r := range roots {
		fmt.Fprintf(os.Stderr, "  watching %s\n", r)
	}
	fmt.Fprintf(os.Stderr, "  sending to %s\n\n", proxy)
}

func isRemote(endpoint string) bool {
	e := strings.ToLower(endpoint)
	return strings.HasPrefix(e, "https://") &&
		!strings.Contains(e, "127.0.0.1") && !strings.Contains(e, "localhost")
}
