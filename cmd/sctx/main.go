// sctx is the SynapCTX token-optimizing command wrapper: it runs developer
// commands and re-renders their output in a token-minimal form for AI
// agents, accounting the savings locally (`sctx gain`) and org-wide via the
// SynapCTX platform.
//
// Native subcommands (gain, flush, init, setup, version, doctor, help) are
// reserved; anything else is treated as a wrapped command and passed
// through untouched. `sctx -- <cmd>` forces passthrough for a command that
// shares a native name.
package main

import (
	"bufio"
	"bytes"
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/synapctx/sctx/internal/adapters/exec/osproc"
	"github.com/synapctx/sctx/internal/adapters/format/brew"
	"github.com/synapctx/sctx/internal/adapters/format/dig"
	dockerfmt "github.com/synapctx/sctx/internal/adapters/format/docker"
	"github.com/synapctx/sctx/internal/adapters/format/du"
	"github.com/synapctx/sctx/internal/adapters/format/filediff"
	"github.com/synapctx/sctx/internal/adapters/format/fs"
	"github.com/synapctx/sctx/internal/adapters/format/generic"
	ghfmt "github.com/synapctx/sctx/internal/adapters/format/gh"
	gitfmt "github.com/synapctx/sctx/internal/adapters/format/git"
	"github.com/synapctx/sctx/internal/adapters/format/golangcilint"
	"github.com/synapctx/sctx/internal/adapters/format/gotest"
	grepfmt "github.com/synapctx/sctx/internal/adapters/format/grep"
	"github.com/synapctx/sctx/internal/adapters/format/jsoncompact"
	kubectlfmt "github.com/synapctx/sctx/internal/adapters/format/kubectl"
	"github.com/synapctx/sctx/internal/adapters/format/makefmt"
	"github.com/synapctx/sctx/internal/adapters/format/mongosh"
	"github.com/synapctx/sctx/internal/adapters/format/mypy"
	npmfmt "github.com/synapctx/sctx/internal/adapters/format/npm"
	pipfmt "github.com/synapctx/sctx/internal/adapters/format/pip"
	"github.com/synapctx/sctx/internal/adapters/format/projectfilter"
	"github.com/synapctx/sctx/internal/adapters/format/psproc"
	"github.com/synapctx/sctx/internal/adapters/format/psql"
	"github.com/synapctx/sctx/internal/adapters/format/pytest"
	"github.com/synapctx/sctx/internal/adapters/format/read"
	"github.com/synapctx/sctx/internal/adapters/format/rsync"
	"github.com/synapctx/sctx/internal/adapters/format/ruff"
	"github.com/synapctx/sctx/internal/adapters/format/ssh"
	"github.com/synapctx/sctx/internal/adapters/hook"
	"github.com/synapctx/sctx/internal/adapters/stats/sqlite"
	"github.com/synapctx/sctx/internal/adapters/telemetry/spool"
	"github.com/synapctx/sctx/internal/application/report"
	"github.com/synapctx/sctx/internal/application/run"
	"github.com/synapctx/sctx/internal/domain/stats"
	"github.com/synapctx/sctx/internal/domain/telemetry"
	"github.com/synapctx/sctx/internal/platform/agentsetup"
	"github.com/synapctx/sctx/internal/platform/config"
	"github.com/synapctx/sctx/internal/platform/gitrepo"
	"github.com/synapctx/sctx/internal/platform/rawcache"
)

var version = "dev" // set via -ldflags at release time

func main() {
	os.Exit(realMain(os.Args[1:]))
}

func realMain(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 2
	}

	// "hook" is dispatched before config load: the hook must stay fail-open
	// (exit 0, print nothing) even if the environment can't produce a valid
	// Config, since Claude Code treats a non-zero hook exit as a real error.
	if args[0] == "hook" {
		return runHook(args[1:])
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: %v\n", err)
		return 1
	}

	ctx := context.Background()

	switch args[0] {
	case "help", "-h", "--help":
		printUsage()
		return 0
	case "version", "--version":
		fmt.Println("sctx " + version)
		return 0
	case "gain":
		return runGain(ctx, cfg, args[1:])
	case "filters":
		return runFilters(args[1:])
	case "flush":
		return runFlush(ctx, cfg)
	case "init":
		return runInit(ctx, cfg, args[1:])
	case "doctor":
		return runDoctor(cfg)
	case "setup":
		return runSetup(cfg, args[1:])
	case "telemetry":
		return runTelemetry(cfg, args[1:])
	case "watch":
		return runWatch(ctx, cfg, args[1:])
	case "--":
		args = args[1:]
		if len(args) == 0 {
			printUsage()
			return 2
		}
	}

	// Checked on the wrapped path, not only on the native subcommands: someone who
	// installs sctx and never runs `setup` is exactly the person who needs telling,
	// and `sctx go test` is the first thing they run. Interactive-only and
	// rate-limited — see nudgeSetup.
	nudgeSetup(cfg)
	return runWrapped(ctx, cfg, args)
}

func runWrapped(ctx context.Context, cfg config.Config, argv []string) int {
	registry := run.NewRegistry()
	registry.Register(gotest.New())
	registry.Register(dig.New())
	registry.Register(psql.New())
	registry.Register(gitfmt.New())
	registry.Register(dockerfmt.New(registry.ResolveBuiltInByArgv))
	registry.Register(kubectlfmt.New(registry.ResolveBuiltInByArgv))
	registry.Register(ghfmt.New())
	registry.Register(golangcilint.New())
	registry.Register(makefmt.New())
	registry.Register(psproc.New())
	registry.Register(filediff.New())
	registry.Register(pytest.New())
	registry.Register(ruff.New())
	registry.Register(mypy.New())
	registry.Register(brew.New())
	registry.Register(mongosh.New())
	registry.Register(du.New())
	registry.Register(rsync.New())
	// ssh delegates to the REMOTE command's formatter, so it needs to look formatters up.
	// Registered last and given the built-in-only resolver as a method value: the resolver is
	// bound to the registry that is being populated, so it sees every built-in formatter
	// regardless of registration order without applying checkout-local trust to a remote
	// host. The adapter declares the function type itself rather than
	// importing the application package, keeping the dependency pointing inwards.
	registry.Register(ssh.New(registry.ResolveBuiltInByArgv))
	for _, f := range pipfmt.All() {
		registry.Register(f)
	}
	for _, f := range npmfmt.All() {
		registry.Register(f)
	}
	for _, f := range grepfmt.All() {
		registry.Register(f)
	}
	for _, f := range fs.All() {
		registry.Register(f)
	}
	for _, f := range read.All() {
		registry.Register(f)
	}
	registerProjectFilters(registry)

	// Stats and telemetry are auxiliary: a failure disables them with a
	// warning, never the wrapped command. Assignment into the interface
	// variables only happens for usable values so the service's nil checks
	// stay honest.
	var statsStore stats.Store
	if store, err := sqlite.NewStore(cfg.StatsDBPath); err != nil {
		fmt.Fprintf(os.Stderr, "sctx: stats disabled: %v\n", err)
	} else {
		statsStore = store
		defer store.Close()
	}

	var emitter telemetry.Emitter
	var spooler *spool.Emitter
	if cfg.TelemetryEnabled {
		spooler = spool.NewEmitter(cfg.SpoolDir, cfg.TelemetryEndpoint, cfg)
		emitter = spooler
	}

	var recovery *rawcache.Cache
	if cfg.RawCacheEnabled {
		recovery = rawcache.New(cfg.RawCacheDir, cfg.RawCacheTTL, cfg.RawCacheMaxBytes)
	}

	svc := run.NewService(registry, osproc.NewRunner(cfg.MaxOutputBytes),
		statsStore, emitter, generic.New(),
		os.Stdout, os.Stderr,
		run.Options{
			Version: version, ForceTier: cfg.ForceTier, RawCache: recovery,
			// A dedicated formatter's decline used to end at verbatim. This is the
			// one transform that is safe to apply without knowing whether the
			// decline meant "not my shape" or "leave the caller's machine output
			// exactly as it is": the same JSON document, minus whitespace.
			LosslessFallback: jsoncompact.New(),
		})

	code, err := svc.Execute(ctx, argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: %v\n", err)
	}

	// Opportunistic spool drain after output is already delivered; strictly
	// deadline-bounded inside Flush, so offline costs nothing noticeable.
	// Throttled to at most once per 60s (AutoFlush) so a busy shell doesn't
	// attempt a network round trip on every single wrapped command.
	if spooler != nil {
		_ = spooler.AutoFlush(ctx)
	}
	return code
}

func registerProjectFilters(registry *run.Registry) {
	wd, err := os.Getwd()
	if err != nil {
		return
	}
	trustPath, err := projectfilter.DefaultTrustPath()
	if err != nil {
		return
	}
	loaded, trusted, err := projectfilter.LoadTrustedFrom(wd, trustPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: project filters disabled: %v\n", err)
		return
	}
	if !trusted {
		return
	}
	for _, formatter := range loaded.Formatters() {
		registry.RegisterProject(formatter, formatter.OverrideBuiltin())
	}
}

const gainUsage = `usage: sctx gain [--project|-p] [--since <dur>] [--failures|-F] [--format text|json]`

func runGain(ctx context.Context, cfg config.Config, args []string) int {
	opts, err := parseGainArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: %v\n", err)
		fmt.Fprintln(os.Stderr, gainUsage)
		return 2
	}

	store, err := sqlite.NewStore(cfg.StatsDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: %v\n", err)
		return 1
	}
	defer store.Close()
	// Color is a TTY affordance: on only for the text renderers writing to an
	// interactive stdout, and suppressed when NO_COLOR is set (https://no-color.org).
	if opts.Format != "json" {
		if _, noColor := os.LookupEnv("NO_COLOR"); !noColor {
			opts.Color = term.IsTerminal(int(os.Stdout.Fd()))
		}
	}
	if err := report.Render(ctx, store, os.Stdout, opts); err != nil {
		fmt.Fprintf(os.Stderr, "sctx: %v\n", err)
		return 1
	}

	// `gain` is the one routine command a human reads on purpose — it is where
	// someone goes to decide whether this tool is earning its place. A savings
	// report that omits "your agent was never told any of this exists" is not an
	// honest one, so unlike the wrapped-path nudge this is neither rate-limited
	// nor conditional on detection. Suppressed for --format json, which is
	// machine-read, and written to stderr so it can never corrupt that contract.
	if opts.Format != "json" {
		if home, err := os.UserHomeDir(); err == nil {
			if st, err := agentsetup.InspectWithCodexMCP(home, codexOrgTokens(cfg), cfg.WorkspaceProxyURL, docsFor(cfg)...); err == nil {
				if notice := gainNotice(st); notice != "" {
					fmt.Fprintf(os.Stderr, "\n%s\n", notice)
				}
			}
		}
	}
	return 0
}

// parseGainArgs parses the flags accepted after `sctx gain`. Unknown flags
// are a usage error (caller exits 2); everything else builds a
// report.Options for report.Render.
func parseGainArgs(args []string) (report.Options, error) {
	var opts report.Options
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--project", "-p":
			wd, err := os.Getwd()
			if err != nil {
				return report.Options{}, fmt.Errorf("--project: %w", err)
			}
			repo := gitrepo.Detect(wd)
			if repo == "" {
				return report.Options{}, fmt.Errorf("--project: could not detect the current repository (no git origin)")
			}
			opts.Repository = repo
		case "--since":
			i++
			if i >= len(args) {
				return report.Options{}, fmt.Errorf("--since requires a value, e.g. --since 7d")
			}
			d, err := parseSince(args[i])
			if err != nil {
				return report.Options{}, err
			}
			opts.Since = time.Now().Add(-d)
		case "--failures", "-F":
			opts.Failures = true
		case "--format":
			i++
			if i >= len(args) {
				return report.Options{}, fmt.Errorf("--format requires a value: text or json")
			}
			switch args[i] {
			case "text", "json":
				opts.Format = args[i]
			default:
				return report.Options{}, fmt.Errorf("--format: unsupported value %q (want text or json)", args[i])
			}
		default:
			return report.Options{}, fmt.Errorf("unknown flag %q", a)
		}
	}
	return opts, nil
}

// parseSince parses an --since duration: a plain number followed by "d"
// (days), or anything time.ParseDuration accepts ("24h", "90m", ...).
func parseSince(s string) (time.Duration, error) {
	if before, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.ParseFloat(before, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid --since duration %q: %w", s, err)
		}
		return time.Duration(days * 24 * float64(time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --since duration %q: %w", s, err)
	}
	return d, nil
}

// backlogFlushTimeout is the budget given to explicit, user-initiated
// drains (`sctx flush`, `sctx init`) so a near-maxSpoolBytes backlog has
// room to fully send, unlike the tight opportunistic post-command budget.
const backlogFlushTimeout = 15 * time.Second

func runFlush(ctx context.Context, cfg config.Config) int {
	// Without this, `sctx flush` is a bypass: someone who declined could still
	// push everything their spool accumulated before they answered. The gate has
	// to be on DELIVERY, not only on the code path that records.
	if !cfg.TelemetryEnabled {
		fmt.Fprintln(os.Stderr, "sctx: flush: telemetry is off, nothing was sent (see 'sctx telemetry')")
		return 0
	}
	// Partially-authorised flushes are normal now and must not look like an
	// error: the spool is filtered by purpose inside FlushWithTimeout.
	emitter := spool.NewEmitter(cfg.SpoolDir, cfg.TelemetryEndpoint, cfg)
	if err := emitter.FlushWithTimeout(ctx, backlogFlushTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "sctx: flush: %v\n", err)
		return 1
	}
	fmt.Println("spool flushed")
	return 0
}

// defaultInitEndpoint is the SynapCTX platform's authenticated remote
// telemetry ingest route, used by `sctx init` unless --endpoint overrides it.
const defaultInitEndpoint = "https://sctx.synapctx.com/v1/telemetry/exec"

const initUsage = `usage: sctx init [--endpoint <url>] [--key <sctx_live_...>] [--default]`

// runInit authenticates this machine against the SynapCTX platform: it takes an
// API key, validates it with a zero-event ping, and on success persists
// ~/.config/sctx/config.toml so future runs deliver telemetry remotely with
// that key. On any validation failure it writes nothing.
//
// One machine can hold one key per org: each successful `sctx init` adds (or
// replaces) that org's key under [org.<slug>] without disturbing other
// orgs' keys already on file. --default marks the org just authenticated as
// the one attributed to events run outside any git repo; the first org ever
// configured becomes the default automatically.
func runInit(ctx context.Context, cfg config.Config, args []string) int {
	endpoint := defaultInitEndpoint
	makeDefault := false
	key := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--endpoint":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "sctx: init: --endpoint requires a value")
				fmt.Fprintln(os.Stderr, initUsage)
				return 2
			}
			endpoint = args[i]
		case "--key":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "sctx: init: --key requires a value")
				fmt.Fprintln(os.Stderr, initUsage)
				return 2
			}
			key = args[i]
		case "--default":
			makeDefault = true
		default:
			fmt.Fprintf(os.Stderr, "sctx: init: unknown flag %q\n", args[i])
			fmt.Fprintln(os.Stderr, initUsage)
			return 2
		}
	}

	// STDIN IS STILL THE SECURE PATH, and the reason is worth keeping rather
	// than deleting along with the restriction: a key passed as --key lands in
	// this process's argv, so it is visible in `ps` to anything running on the
	// machine for as long as the call takes, and in shell history afterwards.
	// Stdin avoids the argv exposure entirely.
	//
	// --key exists anyway because the console hands a developer a ready-to-run
	// one-liner per organization, and a copy-paste flow that silently does the
	// wrong thing is its own hazard — the earlier console text printed a --key
	// flag this command did not have, which simply failed. Prefer piping on a
	// shared machine; use --key on your own.
	token := key
	if token == "" {
		read, err := readToken(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sctx: init: %v\n", err)
			return 1
		}
		token = read
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "sctx: init: empty token")
		return 1
	}

	org, err := validateTelemetryToken(ctx, endpoint, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sctx: init: %v\n", err)
		return 1
	}

	orgTokens := make(map[string]string, len(cfg.OrgTokens)+1)
	maps.Copy(orgTokens, cfg.OrgTokens)
	orgTokens[org] = token
	defaultOrg := cfg.DefaultOrg
	if defaultOrg == "" || makeDefault {
		defaultOrg = org
	}

	// An operator who pointed sctx at their own MCP host keeps it: this function
	// rewrites the config file wholesale, so a value not threaded through here is
	// ERASED — the same trap the consent record carries a comment about. The
	// hosted default is deliberately NOT written: it lives in code, so a machine
	// that never chose a host follows the product rather than a line frozen into
	// a file on the day it was installed.
	if err := writeConfigFile(cfg.ConfigFilePath, endpoint, configuredWorkspaceProxy(cfg), defaultOrg, orgTokens, cfg.Consent); err != nil {
		fmt.Fprintf(os.Stderr, "sctx: init: writing config: %v\n", err)
		return 1
	}
	fmt.Printf("authenticated: organization %s\n", org)

	slugs := make([]string, 0, len(orgTokens))
	for slug := range orgTokens {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	fmt.Printf("configured organizations: %s (default: %s)\n", strings.Join(slugs, ", "), defaultOrg)

	// An API key on its own buys nothing an agent will use. It authorises the MCP
	// tools; it does not tell the agent they exist, and that gap is where the
	// product silently under-delivers — measured at a 7.6x drop in invocation when
	// the instruction file went missing. This is the moment to say so: the key just
	// worked, so the next step is obvious rather than nagging.
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		cfgAfter := cfg
		cfgAfter.OrgTokens = orgTokens
		if st, inspErr := agentsetup.InspectWithCodexMCP(home, codexOrgTokens(cfgAfter), cfgAfter.WorkspaceProxyURL, docsFor(cfgAfter)...); inspErr == nil && !st.Complete() {
			fmt.Println()
			fmt.Println(pendingLine(st))
			fmt.Println("run: sctx setup --install")
		}
	}

	pending := spool.CountPending(cfg.SpoolDir)
	if pending > 0 {
		cfg2, loadErr := config.Load()
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "sctx: init: backlog drain: reloading config: %v\n", loadErr)
		} else {
			emitter := spool.NewEmitter(cfg.SpoolDir, endpoint, cfg2)
			if err := emitter.FlushWithTimeout(ctx, backlogFlushTimeout); err != nil {
				fmt.Fprintf(os.Stderr, "sctx: init: backlog drain: %v\n", err)
			} else {
				fmt.Printf("drained %d spooled event(s)\n", pending)
			}
		}
	}
	return 0
}

// readToken reads the API key from r: masked interactive entry when r is a
// TTY, a plain line read otherwise (piped input, e.g. in scripts or tests).
// It is never accepted as a command-line flag.
func readToken(r io.Reader) (string, error) {
	if f, ok := r.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(os.Stderr, "SynapCTX API key: ")
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("reading token: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("reading token: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// validateTelemetryToken pings endpoint with an empty batch under the
// Bearer token, returning the organization slug the platform reports on a
// 200. Any non-200 or transport failure is returned verbatim so the caller
// can surface the HTTP status without writing any config.
func validateTelemetryToken(ctx context.Context, endpoint, token string) (string, error) {
	payload, err := json.Marshal(map[string]any{"events": []any{}})
	if err != nil {
		return "", fmt.Errorf("encoding validation ping: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, backlogFlushTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("building validation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("contacting %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("authentication failed: %s", resp.Status)
	}

	var parsed struct {
		Organization string `json:"organization"`
	}
	org := "unknown"
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Organization != "" {
		org = parsed.Organization
	}
	return org, nil
}

// configuredWorkspaceProxy is the MCP host to persist when the config file is
// rewritten: a host somebody CHOSE, and nothing otherwise.
//
// Writing the shipped default would freeze it. The endpoint is ours and may
// move; a machine that never chose one should follow the binary, not a copy of
// today's value left in a TOML file by an install two years ago.
func configuredWorkspaceProxy(cfg config.Config) string {
	proxy := strings.TrimSpace(cfg.WorkspaceProxyURL)
	if proxy == config.DefaultWorkspaceProxy {
		return ""
	}
	return proxy
}

// writeConfigFile persists endpoint, defaultOrg, and one [org.<slug>] section
// per orgTokens entry to path in the strict key = "value" format config.Load
// parses, mode 0600 (the keys are secrets), creating the parent directory
// 0700 if needed. Orgs are written in sorted slug order for deterministic
// output; defaultOrg is omitted from the file when empty.
func writeConfigFile(path, endpoint, workspaceProxy, defaultOrg string, orgTokens map[string]string, consent config.ConsentRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "telemetry_endpoint = %q\n", endpoint)
	// The MCP host every agent's registrations point at. Omitted when unknown
	// rather than written as the local dev default, so `sctx setup` can tell
	// "not configured" from "configured to something".
	if workspaceProxy != "" {
		fmt.Fprintf(&b, "workspace_proxy_url = %q\n", workspaceProxy)
	}
	if defaultOrg != "" {
		fmt.Fprintf(&b, "default_org = %q\n", defaultOrg)
	}
	// The consent record rides through every write of this file. This function
	// rewrites it wholesale, so anything not threaded through here is erased —
	// and a customer's answer being quietly forgotten by an unrelated `sctx init`
	// is the one failure this whole feature exists to prevent.
	if consent.Decision != "" {
		fmt.Fprintf(&b, "telemetry_consent = %q\n", consent.Decision)
		fmt.Fprintf(&b, "telemetry_consent_at = %q\n", consent.At)
		fmt.Fprintf(&b, "telemetry_disclosure = %q\n", strconv.Itoa(consent.Disclosure))
	}

	slugs := make([]string, 0, len(orgTokens))
	for slug := range orgTokens {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		fmt.Fprintf(&b, "\n[org.%s]\ntoken = %q\n", slug, orgTokens[slug])
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return nil
}

func runHook(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: sctx hook claude | sctx hook claude-post-tool | sctx hook claude-session-start | sctx hook claude-first-search | sctx hook codex | sctx hook gemini | sctx hook rewrite <command>")
		return 2
	}
	switch args[0] {
	case "claude":
		return hook.RunClaude(args[1:], os.Stdin, os.Stdout, version)
	case "codex":
		// Codex CLI's PreToolUse contract is byte-identical to Claude Code's —
		// same `tool_name`, same `hookSpecificOutput.updatedInput.command`. It
		// gets its own verb anyway: hook detection matches on the subcommand, so
		// one name for two clients would make an installed Codex hook look like
		// an installed Claude hook and vice versa.
		return hook.RunClaude(args[1:], os.Stdin, os.Stdout, version)
	case "rewrite":
		// Plain-text rewrite for callers that speak no hook protocol: the JS
		// plugin sctx installs into Kilo Code and OpenCode.
		return hook.RunRewrite(args[1:], os.Stdout, version)
	case "gemini":
		return hook.RunGemini(args[1:], os.Stdin, os.Stdout, version)
	case "claude-post-tool":
		// Memory surfacing. Config is loaded HERE rather than in realMain's
		// pre-hook branch because this hook needs an API key and the Bash hook
		// deliberately does not — the Bash rewrite must stay fail-open even when
		// no configuration can be produced at all.
		cfg, err := config.Load()
		if err != nil {
			return 0 // fail open, exactly like every other hook path
		}
		return hook.RunClaudePostTool(os.Stdin, os.Stdout, cfg)
	case "claude-session-start":
		// The session brief. Config is loaded here for the same reason as
		// claude-post-tool: it needs an API key, and the Bash hook must keep
		// working on a machine where no configuration can be produced at all.
		cfg, err := config.Load()
		if err != nil {
			return 0
		}
		return hook.RunClaudeSessionStart(os.Stdin, os.Stdout, cfg, version)
	case "claude-first-search":
		cfg, err := config.Load()
		if err != nil {
			return 0
		}
		return hook.RunClaudeFirstSearch(os.Stdin, os.Stdout, cfg)
	default:
		fmt.Fprintln(os.Stderr, "usage: sctx hook claude | sctx hook claude-post-tool | sctx hook claude-session-start | sctx hook claude-first-search | sctx hook codex | sctx hook gemini | sctx hook rewrite <command>")
		return 2
	}
}

// runDoctor prints the effective configuration AND the agent-side install.
// Keeping them in one command is the point: "is sctx configured" and "does the
// agent know sctx exists" are different questions with the same symptom — the
// savings number is lower than it should be and nothing says why.
func runDoctor(cfg config.Config) int {
	fmt.Printf("sctx %s\n", version)
	fmt.Printf("stats db:       %s\n", cfg.StatsDBPath)
	fmt.Printf("spool dir:      %s\n", cfg.SpoolDir)
	if cfg.RawCacheEnabled {
		fmt.Printf("raw recovery:   enabled (%s, %s, max %d bytes)\n", cfg.RawCacheDir, cfg.RawCacheTTL, cfg.RawCacheMaxBytes)
	} else {
		fmt.Println("raw recovery:   disabled (opt in with SCT__RAW_CACHE_ENABLED=true)")
	}
	fmt.Printf("config file:    %s\n", cfg.ConfigFilePath)
	printProjectFilters()
	fmt.Printf("telemetry:      enabled=%t mode=%s\n", cfg.TelemetryEnabled, telemetryMode(cfg))
	fmt.Printf("endpoint:       %s\n", cfg.TelemetryEndpoint)
	if host := endpointHost(cfg.TelemetryEndpoint); host != "" {
		fmt.Printf("endpoint host:  %s\n", host)
	}
	switch {
	case len(cfg.OrgTokens) > 0:
		fmt.Printf("default org:    %s\n", cfg.DefaultOrg)
		fmt.Println("organizations:")
		slugs := make([]string, 0, len(cfg.OrgTokens))
		for slug := range cfg.OrgTokens {
			slugs = append(slugs, slug)
		}
		sort.Strings(slugs)
		for _, slug := range slugs {
			suffix := ""
			if slug == cfg.DefaultOrg {
				suffix = " (default)"
			}
			fmt.Printf("  %-12s %s%s\n", slug, maskToken(cfg.OrgTokens[slug]), suffix)
		}
	case cfg.TelemetryTokenEnv != "" || cfg.LegacyToken != "":
		token := cfg.LegacyToken
		if cfg.TelemetryTokenEnv != "" {
			token = cfg.TelemetryTokenEnv
		}
		fmt.Printf("api key:        %s\n", maskToken(token))
		fmt.Println("                (legacy single-key — run 'sctx init' to adopt per-org keys)")
	}
	if cfg.ForceTier != "" {
		fmt.Printf("force tier:     %s\n", cfg.ForceTier)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if st, err := agentsetup.InspectWithCodexMCP(home, codexOrgTokens(cfg), cfg.WorkspaceProxyURL, docsFor(cfg)...); err == nil {
			printSetupStatus(os.Stdout, st, cfg, false)
		} else {
			fmt.Printf("\nagent setup:    error: %v\n", err)
		}
	}
	return 0
}

// telemetryMode summarizes the effective delivery path: authenticated
// remote (post-init), local unauthenticated (default topology), or fully
// disabled (SCT__TELEMETRY_ENABLED=false).
func telemetryMode(cfg config.Config) string {
	switch {
	case !cfg.TelemetryEnabled:
		return "disabled"
	case cfg.HasAnyToken():
		return "remote (authenticated)"
	default:
		return "local"
	}
}

func endpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return u.Host
}

// maskToken shows only enough of an API key to recognize it at a glance —
// the fixed prefix plus the last 4 characters — never more.
func maskToken(token string) string {
	const prefixLen, suffixLen = 14, 4
	if len(token) <= prefixLen+suffixLen {
		return strings.Repeat("*", len(token))
	}
	return token[:prefixLen] + "…" + token[len(token)-suffixLen:]
}

func printUsage() {
	fmt.Println(`sctx — SynapCTX token-optimizing command wrapper

Usage:
  sctx <command> [args...]   run a command with token-optimized output
  sctx -- <command>          force passthrough for a reserved name
  sctx hook claude           Claude Code PreToolUse Bash hook: rewrites covered
                              commands to "sctx <cmd>" (fail-open)
  sctx hook claude-session-start
                              Claude Code SessionStart hook: briefs the agent
                              with org memory bound to this repository, index
                              freshness and the tools to open with
  sctx hook claude-first-search
                              Claude Code PreToolUse Grep/Glob/Agent hook: on
                              the first local searches of a session, points at
                              the org-wide graph and memory
  sctx hook claude-post-tool  Claude Code PostToolUse hook: surfaces org memory
                              for files you edit, and the cross-repository call
                              sites a grep cannot see
  sctx gain                  show token-savings report
    --project, -p              scope to the current repository
    --since <dur>               only runs newer than <dur> (e.g. 7d, 24h)
    --failures, -F              show the degradation log instead
    --format text|json          output format (default text)
  sctx flush                 force-drain the telemetry spool
  sctx init [--endpoint <url>] [--default]
                              authenticate against the SynapCTX platform for
                              remote telemetry delivery; reads the API key
                              from stdin, never a flag. Keys are stored per
                              organization, one machine can hold several;
                              --default attributes events outside any git
                              repo to the org just authenticated
  sctx watch [--root <dir>]  keep UNCOMMITTED code queryable: streams the
                              structural diff of your working tree (symbol
                              names, signatures, doc comments, content hashes
                              — never bodies) so retrieve_context answers about
                              the code you are changing, not the last commit.
                              Foreground and per-developer; stop it and nothing
                              further is sent
  sctx setup [--install]     check (and repair) the agent-side install: whether
                              the coding agents configured on this machine have
                              been told sctx and SynapCTX exist. Writes ONLY to
                              agents it detects; creates nothing otherwise
    --list-agents               show every agent this sctx knows how to teach
    --agent <id>                also teach one it did not detect
  sctx telemetry             what usage data sctx collects, and whether it is
                              being sent. Off until you say otherwise.
    --preview                   print the events queued on THIS machine, verbatim
    --enable / --disable        record your decision
  sctx filters verify        validate .sctx/filters.json and all inline fixtures
  sctx filters status        show its digest and content-bound trust state
  sctx filters trust --yes   approve that exact digest for this checkout
  sctx doctor                show effective configuration
  sctx version               show version

Configuration is env-over-file (SCT__* env vars, then
~/.config/sctx/config.toml written by 'sctx init'), see the repository
README.`)
}
