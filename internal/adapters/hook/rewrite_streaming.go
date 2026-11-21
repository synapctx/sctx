package hook

import (
	"regexp"
	"strings"
)

// This file holds the streamsForever rules for programs added with the coverage
// expansion. The contract is the one documented on followFlags: a command that
// never reaches EOF (a watch mode, a dev server, an interactive prompt, a TTY
// session) must NEVER be wrapped, because sctx reads stdout to EOF before it
// formats and the agent would see nothing until its timeout.
//
// Every rule is scoped to the subcommand or flag that actually streams, never a
// blanket per-program refusal: `terraform plan`, `gradle test` and `just build`
// all finish and must keep their coverage. Where an argument cannot be told
// apart from a flag VALUE (`aws --profile logs ...`) the rule matches at any
// position and over-declines, which only costs a saving — the reverse error
// costs the agent's whole output.

// coverageStreams reports whether program (a table key, possibly a literal
// `./gradlew`) with args follows indefinitely. It is the default arm of
// streamsForever; programs handled there never reach it.
func coverageStreams(program string, args []string) bool {
	program = filepathBase(program)
	args = unquoteAll(args)
	switch program {
	case "gotestsum":
		return anyArg(args, "--watch", "-w") || anyPrefix(args, "--watch=")
	case "cargo":
		// `cargo watch` is a plugin subcommand that reruns forever. `cargo run` is
		// deliberately NOT refused here: see the TODO(owner decision) on the go row.
		return firstOperand(args) == "watch"
	case "ping":
		return !pingFinite(args)
	case "cmake":
		return cmakeStreams(args)
	case "ninja":
		return ninjaStreams(args)
	case "rake":
		return rakeStreams(args)
	case "oxlint":
		return anyArg(args, "--lsp")
	case "sqlfluff":
		return sqlfluffStreams(args)
	case "just", "task", "mise":
		return taskRunnerStreams(program, args)
	case "lsof":
		return lsofRepeats(args)
	case "terraform", "tofu":
		return terraformStreams(args)
	case "terragrunt":
		return terragruntStreams(args)
	case "aws":
		return awsStreams(args)
	case "gcloud":
		return gcloudStreams(args)
	case "az":
		return azStreams(args)
	case "pulumi":
		return pulumiStreams(args)
	case "ansible-playbook":
		return ansibleStreams(args)
	case "next":
		// Not wrapped today (only `next build` has a row) but named so the gap
		// meter records a dev server as streaming rather than as a gap.
		op := firstOperand(args)
		return op == "dev" || op == "start"
	case "prisma":
		// playwright/turbo/nx/bun/deno are routed by streamsForever itself, with
		// the package-runner paths (watchTools).
		return jsToolStreams(program, args)
	case "pyright", "basedpyright":
		return anyArg(args, "-w", "--watch")
	case "ty":
		return firstOperand(args) == "server" || anyArg(args, "--watch", "-W")
	case "ruff":
		return firstOperand(args) == "server" || anyArg(args, "--watch", "-w")
	case "pytest":
		return anyArg(args, "-f", "--looponfail")
	case "ptw", "pytest-watch":
		return true
	case "dmypy":
		op := firstOperand(args)
		return op == "run" || op == "start"
	case "black":
		return anyArg(args, "--watch")
	case "gradle", "gradlew":
		return gradleStreams(args)
	case "mvn", "mvnw", "mvnd":
		return mavenStreams(args)
	case "dotnet":
		op := firstOperand(args)
		return op == "watch" || op == "new"
	case "rails":
		return railsStreams(args)
	case "guard", "rackup", "puma", "foreman", "sidekiq", "solargraph", "spring":
		return true
	case "bundle":
		return bundleStreams(args)
	case "php":
		return phpStreams(args)
	case "rubocop":
		return anyArg(args, "--lsp")
	case "phpstan":
		// --pro starts a local web UI and --fix is an interactive session.
		return anyArg(args, "--watch", "--pro", "--fix")
	case "ast-grep", "sg":
		return firstOperand(args) == "lsp" || anyArg(args, "--interactive") || shortCluster(args, "i")

	// Wave 3 (rewrite_wave3.go, rewrite_linuxsys.go).
	case "trivy", "semgrep", "tflint", "gitleaks":
		return scannerStreams(program, args)
	case "openssl":
		return opensslStreams(args)
	case "svn":
		return svnStreams(args)
	case "unzip":
		return unzipPrompts(args)
	case "sbt", "mix", "dart", "zig", "rebar3", "cabal", "ghc", "bazel", "bazelisk", "nix", "nix-shell":
		return langStreams(program, args)
	case "mysql", "mariadb", "mongosh", "sqlite3", "redis-cli", "clickhouse-client", "dbt", "liquibase", "flyway":
		return dbStreams(program, args)
	case "apt", "apt-get", "dnf", "yum", "pacman", "top", "htop", "btop", "bpytop", "glances", "atop", "iotop", "iftop",
		"nload", "nethogs", "bmon", "slabtop", "watch", "tcpdump", "tshark", "ngrep", "mtr", "dmesg",
		"vmstat", "iostat", "mpstat", "pidstat", "sar", "ss", "netstat", "ip", "traceroute", "traceroute6", "tracepath", "apk":
		return linuxStreams(program, args)
	}
	return false
}

// jsToolStreams applies the watch/serve rules of the JavaScript tools added with
// the coverage expansion to the tool's own arguments (after any runner).
func jsToolStreams(tool string, args []string) bool {
	switch tool {
	case "playwright":
		// --ui and --debug keep a browser session open; show-report and
		// show-trace serve a local web server; codegen and open are interactive.
		if anyArg(args, "--ui", "--ui-host", "--ui-port", "--debug") ||
			anyPrefix(args, "--ui=", "--ui-host=", "--ui-port=", "--debug=") {
			return true
		}
		return hasOperand(args, "show-report", "show-trace", "codegen", "open")
	case "turbo":
		if anyArg(args, "--watch", "--persistent") || anyPrefix(args, "--watch=", "--persistent=") {
			return true
		}
		return taskOperandsServe(args)
	case "nx":
		return nxStreams(args)
	case "bun":
		return anyArg(args, "--watch", "--hot") || anyPrefix(args, "--inspect")
	case "deno":
		return anyArg(args, "--watch", "--watch-hmr", "--hot") || anyPrefix(args, "--watch=", "--watch-hmr=", "--inspect")
	case "prisma":
		// `prisma dev` and `studio` are servers; `migrate dev` is interactive.
		return anyArg(args, "--watch", "studio", "dev")
	}
	return false
}

func nxStreams(args []string) bool {
	if taskOperandsServe(args) {
		return true
	}
	for i, a := range args {
		switch a {
		case "serve", "dev", "start", "watch", "graph", "--watch", "--graph":
			return true
		case "-t", "--target", "--targets":
			if i+1 < len(args) && servesTarget(args[i+1]) {
				return true
			}
		}
		for _, p := range []string{"--target=", "--targets=", "-t="} {
			if v, ok := strings.CutPrefix(a, p); ok && servesTarget(v) {
				return true
			}
		}
		for _, suffix := range []string{":serve", ":dev", ":start"} {
			if strings.HasSuffix(a, suffix) {
				return true
			}
		}
		if strings.HasPrefix(a, "--watch=") || strings.HasPrefix(a, "--graph=") {
			return true
		}
	}
	return false
}

// serveTaskWords are task-name components that start a server or a watcher.
var serveTaskWords = map[string]bool{
	"dev": true, "develop": true, "serve": true, "start": true, "watch": true,
	"preview": true, "storybook": true,
}

// taskOperandsServe reports whether any non-flag operand names a long-running
// task. Each operand is split on `#` and `:` so `web#dev`, `docs:dev` and
// `app:serve:development` are caught as well as a bare `dev`.
func taskOperandsServe(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		for _, c := range strings.FieldsFunc(a, func(r rune) bool { return r == '#' || r == ':' }) {
			if serveTaskWords[c] {
				return true
			}
		}
	}
	return false
}

// servesTarget reports whether a comma-separated nx target list names a
// long-running one.
func servesTarget(list string) bool {
	for _, t := range strings.Split(list, ",") {
		switch t {
		case "serve", "dev", "start":
			return true
		}
	}
	return false
}

// pingFinite reports whether ping carries a count or deadline, i.e. stops by
// itself. Without one it runs until killed. `-t` is deliberately NOT accepted as
// proof: it is a timeout on macOS, a TTL on Linux and "until stopped" on
// Windows, so no spelling of it proves termination across platforms. For the
// same reason `-w` counts as a deadline (Linux) only when `-t` is absent: on
// Windows `-w` is a per-reply timeout and `-t` pings forever.
func pingFinite(args []string) bool {
	hasT := false
	for _, a := range args {
		if a == "-t" {
			hasT = true
		}
	}
	for _, a := range args {
		switch {
		case a == "-c" || a == "--count":
			return true
		case a == "-w" || a == "--deadline":
			if !hasT {
				return true
			}
		case strings.HasPrefix(a, "--count="):
			return true
		case strings.HasPrefix(a, "--deadline="):
			if !hasT {
				return true
			}
		case strings.HasPrefix(a, "-c") && len(a) > 2 && isDigits(a[2:]):
			return true
		case strings.HasPrefix(a, "-w") && len(a) > 2 && isDigits(a[2:]):
			if !hasT {
				return true
			}
		}
	}
	return false
}

// cmakeTargetsRun are build-target names that run a program or a server rather
// than build something (`cmake --build . --target run`).
var cmakeTargetsRun = map[string]bool{"run": true, "serve": true, "start": true, "dev": true, "watch": true}

func cmakeStreams(args []string) bool {
	for i, a := range args {
		switch {
		case a == "--target" || a == "-t":
			// Several targets may follow a single flag.
			for _, v := range args[i+1:] {
				if strings.HasPrefix(v, "-") {
					break
				}
				if cmakeTargetsRun[v] {
					return true
				}
			}
		case strings.HasPrefix(a, "--target="):
			if cmakeTargetsRun[strings.TrimPrefix(a, "--target=")] {
				return true
			}
		}
	}
	return false
}

// ninjaStreams refuses `-t browse` (serves the dependency graph and waits) and
// the conventional `run` target.
func ninjaStreams(args []string) bool {
	for i, a := range args {
		switch {
		case a == "-t" && i+1 < len(args) && args[i+1] == "browse":
			return true
		case a == "-tbrowse":
			return true
		case !strings.HasPrefix(a, "-") && cmakeTargetsRun[a]:
			return true
		}
	}
	return false
}

// sqlfluffStreams refuses `fix` unless -f/--force answers its confirmation
// prompt, and `fix --check`.
func sqlfluffStreams(args []string) bool {
	if !hasOperand(args, "fix") {
		return false
	}
	if anyArg(args, "--check") {
		return true
	}
	return !anyArg(args, "-f", "--force")
}

// ansibleStreams refuses every way ansible-playbook prompts: the password
// flags (also inside a cluster such as `-bK`), `--step`, `--ask-*`, and a
// `--vault-id label@prompt`.
func ansibleStreams(args []string) bool {
	if shortCluster(args, "kKJ") || anyArg(args, "--step") || anyPrefix(args, "--ask-") {
		return true
	}
	for i, a := range args {
		switch {
		case a == "--vault-id" && i+1 < len(args) && strings.HasSuffix(args[i+1], "@prompt"):
			return true
		case strings.HasPrefix(a, "--vault-id=") && strings.HasSuffix(a, "@prompt"):
			return true
		}
	}
	return false
}

// rakeAllowed reports whether the first rake task operand is known to finish.
// Rake runs arbitrary project-defined tasks (`jobs:work`, `server`), so the rule
// is an allowlist, not a denylist.
func rakeAllowed(task string) bool {
	switch task {
	case "test", "spec", "routes", "stats", "about", "notes", "log:clear":
		return true
	case "db:console":
		return false
	}
	if finiteRecipes[task] {
		return true
	}
	for _, p := range []string{"test:", "spec:", "db:", "assets:", "tmp:"} {
		if strings.HasPrefix(task, p) {
			return true
		}
	}
	return false
}

func rakeStreams(args []string) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "-T" || a == "-P" || a == "-D" || a == "--tasks" || strings.HasPrefix(a, "--tasks=") {
			return false
		}
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if strings.Contains(a, "=") {
			continue // NAME=value environment assignment
		}
		return !rakeAllowed(a)
	}
	return true // bare `rake` runs the default task
}

// finiteRecipes are the recipe verbs of a task runner that run to completion.
// Anything else (`dev`, `serve`, `up`, a deploy) may never return, and a bare
// invocation runs the default recipe, which can be anything.
var finiteRecipes = map[string]bool{
	"build": true, "test": true, "check": true, "lint": true, "vet": true,
	"fmt": true, "format": true, "clean": true, "ci": true, "install": true,
	"typecheck": true, "bench": true, "tidy": true, "generate": true,
	"verify": true, "all": true, "coverage": true, "cover": true,
}

// taskRunnerStreams decides just/task/mise. The first operand (the recipe; after
// `run`/`r` for mise) must be a finite verb; later operands must be finite verbs
// or parameter-shaped (a path, `NAME=value`, a number). A bare invocation runs
// the default recipe and is never wrapped, except for the pure information
// flags, which print and exit.
func taskRunnerStreams(program string, args []string) bool {
	if anyArg(args, "-w", "--watch") || anyPrefix(args, "--watch=") {
		return true
	}
	var operands []string
	for _, a := range args {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			operands = append(operands, a)
		}
	}
	if program == "mise" && len(operands) > 0 && (operands[0] == "run" || operands[0] == "r") {
		operands = operands[1:]
	}
	if len(operands) == 0 {
		return !anyArg(args, "-l", "--list", "--list-all", "--summary", "--help", "-h",
			"--version", "-V", "--dump", "--evaluate", "--variables", "--init")
	}
	if !finiteOperand(operands[0]) {
		return true
	}
	for _, o := range operands[1:] {
		if !finiteOperand(o) && !paramShaped(program, o) {
			return true
		}
	}
	return false
}

// finiteOperand reports whether a recipe name is a finite verb, or a namespaced
// recipe (`docs:build`, `docs::build`) whose last component is one.
func finiteOperand(o string) bool {
	if finiteRecipes[o] {
		return true
	}
	if i := strings.LastIndexByte(o, ':'); i >= 0 {
		return finiteRecipes[o[i+1:]]
	}
	return false
}

// paramShaped reports whether an operand looks like a value handed to a recipe
// (a path, `NAME=value`, a number) rather than the name of another one. `:` is
// not a parameter character for task and just, where `docs:serve` is a task name.
func paramShaped(program, o string) bool {
	chars := "/.=:@"
	if program == "task" || program == "just" {
		chars = "/.=@"
	}
	return strings.ContainsAny(o, chars) || isDigits(o)
}

// lsofRepeats reports lsof's repeat mode: `-r [t]` / `+r [t]` re-list every t
// seconds until interrupted. It also catches the option inside a single-dash
// cluster (`-nPr 2`) and with a format (`-r1m==%T==`).
func lsofRepeats(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "+r") {
			return true
		}
	}
	return shortCluster(args, "r")
}

// terraformStreams refuses `console` (a REPL) and an `apply`/`destroy` that will
// stop at its confirmation prompt. -auto-approve answers it; -input=false makes
// it fail instead of asking.
func terraformStreams(args []string) bool {
	switch firstOperand(args) {
	case "console", "login":
		// A REPL, and a browser-login flow that asks for confirmation first.
		return true
	case "force-unlock":
		return !anyArg(args, "-force")
	case "apply", "destroy":
		return !approvedNonInteractive(args)
	}
	return false
}

// terragruntStreams is terraformStreams for a runner that takes the verb at any
// position (`run-all apply`, `run --all -- apply`).
func terragruntStreams(args []string) bool {
	if hasOperand(args, "console", "login") {
		return true
	}
	if hasOperand(args, "apply", "destroy") {
		nonInteractive := anyArg(args, "--terragrunt-non-interactive", "--non-interactive")
		if hasOperand(args, "run-all", "runall") || anyArg(args, "--all") {
			// run-all asks its own confirmation, which -auto-approve does not answer.
			return !nonInteractive
		}
		return !approvedNonInteractive(args) && !nonInteractive
	}
	return false
}

func approvedNonInteractive(args []string) bool {
	return anyArg(args, "-auto-approve", "--auto-approve", "-auto-approve=true", "-input=false")
}

func awsStreams(args []string) bool {
	switch {
	case hasOperand(args, "start-session", "execute-command"):
		return true
	case hasSeq(args, "logs", "tail"):
		return anyArg(args, "--follow", "-f") || anyPrefix(args, "--follow=")
	case hasSeq(args, "sso", "login"):
		return true
	case anyArg(args, "configure"):
		// `aws configure` prompts for values; its sub-commands (`set`, `list`)
		// are not told apart because the word can sit at any position.
		return true
	case hasSeq(args, "logs", "start-live-tail"):
		return true
	case hasSeq(args, "ec2-instance-connect", "ssh"), hasSeq(args, "ec2-instance-connect", "open-tunnel"):
		return true
	}
	return false
}

func gcloudStreams(args []string) bool {
	if anyArg(args, "--follow", "--watch") || anyPrefix(args, "--follow=", "--watch=") {
		return true
	}
	switch {
	case hasSeq(args, "logging", "tail"), hasSeq(args, "logs", "tail"):
		return true
	case hasOperand(args, "ssh", "start-iap-tunnel", "login", "interactive",
		"connect-to-serial-port", "start-tcp-tunnel"):
		return true
	case firstOperand(args) == "init":
		return true
	case hasOperand(args, "emulators") && hasOperand(args, "start"):
		return true
	case hasSeq(args, "services", "proxy"), hasSeq(args, "sql", "connect"), hasSeq(args, "code", "dev"):
		return true
	}
	return false
}

func azStreams(args []string) bool {
	if anyArg(args, "--watch", "--follow") || anyPrefix(args, "--watch=", "--follow=") {
		return true
	}
	if hasSeq(args, "log", "tail") {
		return true
	}
	if hasOperand(args, "logs") && anyArg(args, "-f", "--follow") {
		return true
	}
	if hasOperand(args, "ssh", "login", "create-remote-connection") ||
		hasSeq(args, "container", "exec") || hasSeq(args, "container", "attach") ||
		hasSeq(args, "containerapp", "exec") || hasSeq(args, "aks", "browse") ||
		hasSeq(args, "bastion", "tunnel") || hasSeq(args, "serial-console", "connect") {
		return true
	}
	switch firstOperand(args) {
	case "configure", "interactive":
		return true
	}
	return false
}

func pulumiStreams(args []string) bool {
	if firstOperand(args) == "watch" {
		return true
	}
	return hasOperand(args, "logs") && anyArg(args, "--follow", "-f")
}

// gradleLongRunning are task names that start a server or an application and
// stay up (also matched as a `:project:task` suffix).
var gradleLongRunning = []string{
	"run", "bootRun", "bootTestRun", "jettyRun", "appRun", "tomcatRun",
	"quarkusDev", "runIde", "runClient", "runServer",
}

var gradleRunWord = regexp.MustCompile(`Run[A-Z]`)

func gradleStreams(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--continuous" || a == "-t" || a == "init":
			// `-t` is gradle's short --continuous; `init` is an interactive wizard.
			return true
		case strings.HasPrefix(a, "-"):
			continue
		}
		// The task name is the last `:`-separated component (`:app:bootRun`).
		task := a[strings.LastIndexByte(a, ':')+1:]
		switch {
		case task == "run", strings.HasSuffix(task, "Run"), strings.HasSuffix(task, "RunWar"),
			gradleRunWord.MatchString(task):
			return true
		}
		for _, long := range gradleLongRunning {
			if task == long || gradleAbbreviates(task, long) {
				return true
			}
		}
	}
	return false
}

// gradleAbbreviates reports whether name is a camel-case abbreviation of a
// multi-word task (`bootR`, `bRu` for `bootRun`): gradle runs the task they
// resolve to.
func gradleAbbreviates(name, full string) bool {
	nh, fh := camelHumps(name), camelHumps(full)
	if len(fh) < 2 || len(nh) != len(fh) {
		return false
	}
	for i := range nh {
		if !strings.HasPrefix(fh[i], nh[i]) {
			return false
		}
	}
	return true
}

// camelHumps splits a camelCase name before each upper-case letter.
func camelHumps(s string) []string {
	var out []string
	start := 0
	for i := 1; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			out = append(out, s[start:i])
			start = i
		}
	}
	return append(out, s[start:])
}

// mavenLongRunning maps a plugin prefix to the goals that start something that
// stays up.
var mavenLongRunning = map[string][]string{
	"spring-boot": {"run", "start"},
	"quarkus":     {"dev", "run"},
	"jetty":       {"run", "run-war"},
	"tomcat":      {"run"},
	"tomcat7":     {"run"},
	"tomcat9":     {"run"},
	"exec":        {"java", "exec"},
	"liberty":     {"dev", "run"},
	"wildfly":     {"run"},
	"cargo":       {"run", "start"},
	"gwt":         {"run"},
	"fabric8":     {"watch"},
}

// mavenLongGoals are goal names that start a server, a watcher or a shell
// whatever plugin provides them.
var mavenLongGoals = map[string]bool{
	"run": true, "dev": true, "run-war": true, "test-run": true, "devmode": true,
	"watch": true, "console": true, "shell": true, "start": true,
}

func mavenStreams(args []string) bool {
	batch := anyArg(args, "-B", "--batch-mode", "-DinteractiveMode=false")
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		// `plugin:goal@execution-id` runs the goal bound to that execution.
		a, _, _ = strings.Cut(a, "@")
		parts := strings.Split(a, ":")
		if len(parts) < 2 {
			continue
		}
		goal := parts[len(parts)-1]
		if mavenLongGoals[goal] || (goal == "test" && strings.TrimSuffix(parts[len(parts)-2], "-maven-plugin") == "quarkus") {
			return true
		}
		// `plugin:goal`, or full coordinates `group:artifact:[version:]goal`.
		for _, p := range []string{parts[len(parts)-2], coordPlugin(parts)} {
			p = strings.TrimSuffix(p, "-maven-plugin")
			for _, g := range mavenLongRunning[p] {
				if g == goal {
					return true
				}
			}
			// archetype:generate and release:prepare/perform prompt for every
			// value unless batch mode answers them.
			if !batch && ((p == "archetype" && goal == "generate") ||
				(p == "release" && (goal == "prepare" || goal == "perform"))) {
				return true
			}
		}
	}
	return false
}

// coordPlugin returns the artifact of `group:artifact:version:goal` coordinates.
func coordPlugin(parts []string) string {
	if len(parts) >= 4 {
		return parts[len(parts)-3]
	}
	return ""
}

func railsStreams(args []string) bool {
	switch firstOperand(args) {
	case "server", "s", "console", "c", "dbconsole", "db", "runner":
		return true
	}
	return false
}

// bundleStreams looks through `bundle exec <x>` to the program it runs.
func bundleStreams(args []string) bool {
	i := 0
	for i < len(args) && args[i] != "exec" {
		if !strings.HasPrefix(args[i], "-") {
			return false // not `exec`
		}
		i++
	}
	if i >= len(args) {
		return false
	}
	rest := args[i+1:]
	for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return false
	}
	inner := filepathBase(rest[0])
	if inner == "rails" {
		return railsStreams(rest[1:])
	}
	switch inner {
	case "guard", "rackup", "puma", "foreman", "sidekiq", "solargraph", "spring":
		return true
	}
	return coverageStreams(inner, rest[1:]) && inner != "bundle"
}

// phpStreams covers `php artisan <cmd>` for the Laravel commands that serve or
// loop.
func phpStreams(args []string) bool {
	var operands []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			operands = append(operands, a)
		}
	}
	if len(operands) < 2 || filepathBase(operands[0]) != "artisan" {
		return false
	}
	switch operands[1] {
	case "serve", "tinker", "queue:work", "queue:listen", "schedule:work", "horizon",
		"reverb:start", "octane:start", "pail":
		return true
	}
	return false
}

// ── small argument helpers ─────────────────────────────────────────────────

// shortCluster reports whether any single-dash argument (`-nPr`, `-bK`, not
// `--long`) contains one of the letters. A flag value glued to its option
// (`-uroot`) can match too, which only over-declines.
func shortCluster(args []string, letters string) bool {
	for _, a := range args {
		if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], letters) {
			return true
		}
	}
	return false
}

func unquoteAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = shellTokenValue(a)
	}
	return out
}

// anyArg reports whether any argument equals one of names.
func anyArg(args []string, names ...string) bool {
	for _, a := range args {
		for _, n := range names {
			if a == n {
				return true
			}
		}
	}
	return false
}

// anyPrefix reports whether any argument starts with one of prefixes.
func anyPrefix(args []string, prefixes ...string) bool {
	for _, a := range args {
		for _, p := range prefixes {
			if strings.HasPrefix(a, p) {
				return true
			}
		}
	}
	return false
}

// hasOperand is anyArg restricted to non-flag arguments.
func hasOperand(args []string, names ...string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		for _, n := range names {
			if a == n {
				return true
			}
		}
	}
	return false
}

// firstOperand returns the first argument that is not a flag, skipping a
// leading `+toolchain` selector; "" when there is none. Flag values are not
// modelled, so a value-taking flag can shift it — callers that care use
// hasOperand/hasSeq instead.
func firstOperand(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+") {
			continue
		}
		return a
	}
	return ""
}

// hasSeq reports whether a and b appear as adjacent non-flag operands.
func hasSeq(args []string, a, b string) bool {
	prev := ""
	for _, x := range args {
		if strings.HasPrefix(x, "-") {
			continue
		}
		if prev == a && x == b {
			return true
		}
		prev = x
	}
	return false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
