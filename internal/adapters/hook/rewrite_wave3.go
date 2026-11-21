package hook

import (
	"strings"

	"github.com/synapctx/sctx/internal/adapters/format/clickhouse"
	"github.com/synapctx/sctx/internal/adapters/format/rediscli"
	"github.com/synapctx/sctx/internal/platform/nestedcmd"
)

// This file holds the streamsForever rules of the third coverage wave (scanners,
// language toolchains, database clients, certificate tools). The contract is the
// one documented on coverageStreams: an invocation that never reaches EOF, or
// that stops at a prompt or a REPL, is NEVER wrapped.

// scannerStreams covers trivy/semgrep/tflint/gitleaks: servers, language
// servers and a stdin-reading mode are the only spellings that do not finish.
func scannerStreams(program string, args []string) bool {
	switch program {
	case "trivy":
		return firstOperand(args) == "server" || anyArg(args, "--listen") || anyPrefix(args, "--listen=")
	case "semgrep":
		// lsp and mcp speak stdio forever; login waits for the browser flow.
		return hasOperand(args, "lsp", "mcp", "login")
	case "tflint":
		return anyArg(args, "--langserver")
	case "gitleaks":
		return anyArg(args, "--pipe") || hasOperand(args, "stdin")
	}
	return false
}

// opensslStreams refuses what blocks or serves: s_server and s_time, a
// key-generating `req` that would prompt for the subject, and an s_client that
// is not visibly given a stdin. s_client keeps the connection open until the
// server closes it, so without a redirect (`</dev/null`, `< file`, `<<<`) the
// agent would see nothing until its timeout. A redirect is recognised by an
// argument that starts with `<`; the match is on the unquoted token, so a
// quoted literal `'</dev/null'` is over-accepted, which no real invocation
// writes. A pipe consumer (`echo | openssl s_client`) is never wrapped at all
// (wrappable), so it needs no case here.
func opensslStreams(args []string) bool {
	switch firstOperand(args) {
	case "s_server", "s_time":
		return true
	case "s_client":
		return !anyPrefix(args, "<")
	case "req":
		// -new/-newkey/-x509 without -subj/-batch asks for each DN field.
		return (anyArg(args, "-new", "-x509") || anyPrefix(args, "-newkey")) &&
			!anyArg(args, "-subj", "-batch")
	}
	return false
}

// svnStreams refuses a commit that would open $EDITOR: no -m/--message/-F/--file.
func svnStreams(args []string) bool {
	switch firstOperand(args) {
	case "commit", "ci", "import":
		return !(anyArg(args, "-m", "--message", "-F", "--file") ||
			anyPrefix(args, "--message=", "--file=", "-m", "-F"))
	}
	return false
}

// flagCluster reports a single-dash option cluster made only of letters and
// digits (`-qy`, `-xvf`) that contains one of letters. A glued option value
// (`-oDebug=y`, `-dout`) is not a cluster and never matches, which matters when
// the letter is a consent flag: matching a value would wrap a prompt.
func flagCluster(args []string, letters string) bool {
	for _, a := range args {
		if len(a) < 2 || a[0] != '-' || a[1] == '-' || !strings.ContainsAny(a[1:], letters) {
			continue
		}
		clean := true
		for _, r := range a[1:] {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
				clean = false
				break
			}
		}
		if clean {
			return true
		}
	}
	return false
}

// unzipPrompts reports an extraction that may stop at "replace file? [y]es,
// [n]o, [A]ll": unzip asks whenever a target exists, unless -o/-n answers it.
// Listing, testing and piping modes (-l -t -v -p -c -Z) never ask. A cluster is
// read up to a letter that takes the rest of the argument as its value (-d DIR,
// -x LIST, -i LIST), so `-dout` is a directory, not -o -u -t.
func unzipPrompts(args []string) bool {
	for _, a := range args {
		if len(a) < 2 || a[0] != '-' || a[1] == '-' {
			continue
		}
		for _, r := range a[1:] {
			if strings.ContainsRune("dxi", r) {
				break
			}
			if strings.ContainsRune("olntvpcZ", r) {
				return false
			}
		}
	}
	return true
}

// sbtValueFlags are sbt launcher options that consume the next argument, so it
// is not mistaken for a task.
var sbtValueFlags = map[string]bool{
	"-mem": true, "-java-home": true, "-sbt-dir": true, "-sbt-boot": true, "-ivy": true,
	"-jvm-debug": true, "-sbt-version": true, "-scala-version": true, "-sbt-jar": true,
	"-sbt-launch-dir": true, "-sbt-launch-repo": true, "-batch-size": true,
}

// sbtLongRunning are sbt tasks that run an application, open a console or start
// a shell; matched as a whole word anywhere in the command string.
var sbtLongRunning = map[string]bool{
	"console": true, "consoleQuick": true, "consoleProject": true, "shell": true,
	"run": true, "runMain": true, "reStart": true, "bgRun": true, "bgRunMain": true,
	"ide": true, "server": true, "client": true,
}

// sbtStreams refuses the interactive shell (no task), continuous execution
// (`~test`), consoles and application runs. sbt takes its tasks as one string
// (`sbt "testOnly X; ~compile"`), so each argument is split into words.
func sbtStreams(args []string) bool {
	tasks := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			switch {
			case a == "--client" || a == "-client" || a == "--server":
				return true
			case sbtValueFlags[a]:
				i++
			}
			continue
		}
		// Each `;`-separated command is its own task line; its FIRST word is the task.
		for ci, cmd := range strings.Split(a, ";") {
			for k, w := range strings.Fields(cmd) {
				// A scoped task is `project/Config/task` or `config:task`: the task is
				// the segment after the last `/` or `:` (`core/run`, `Compile/console`).
				task := w[strings.LastIndexAny(w, "/:")+1:]
				if strings.HasPrefix(w, "~") || sbtLongRunning[task] || (k == 0 && w == "new" && (ci > 0 || tasks == 0)) {
					// `sbt new <template>` runs giter8, which prompts for every field.
					return true
				}
				if !isDigits(w) {
					tasks++
				}
			}
		}
	}
	return tasks == 0 // no task opens the interactive shell
}

// mixStreams: servers and watchers. `mix` tasks are project-defined, so only
// the verbs in the table are ever wrapped; these spellings are named for them.
func mixStreams(args []string) bool {
	switch firstOperand(args) {
	case "phx.server", "test.watch", "iex", "shell", "release.remote", "phx.new", "new":
		return true
	case "run":
		return anyArg(args, "--no-halt")
	case "test":
		return anyArg(args, "--listen-on-stdin")
	}
	return false
}

// dartStreams: `dart run` and `serve` execute a user program or server, `pub run`
// and `pub global run` likewise; `pub publish` asks a y/N question unless forced
// and `login`/`token` are interactive.
func dartStreams(args []string) bool {
	switch op := firstOperand(args); op {
	case "run", "serve", "language-server", "devtools", "create", "debug_adapter":
		return true
	case "test":
		return anyArg(args, "--pause-after-load")
	case "pub":
		if hasOperand(args, "run", "serve", "login", "token") {
			return true
		}
		return hasOperand(args, "publish") && !anyArg(args, "-f", "--force", "-n", "--dry-run")
	}
	return false
}

// zigStreams: watch/fuzz/web-UI modes, `zig run`, and build steps that run the
// program (`zig build run`, `run-server`) or serve.
func zigStreams(args []string) bool {
	if anyArg(args, "--watch", "-fwatch", "--webui") || anyPrefix(args, "--fuzz", "--webui=") {
		return true
	}
	switch firstOperand(args) {
	case "run":
		return true
	case "build":
		first := true
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			if first {
				first = false // the `build` word itself
				continue
			}
			if cmakeTargetsRun[a] || strings.HasPrefix(a, "run-") || strings.HasPrefix(a, "run:") {
				return true
			}
		}
	}
	return false
}

// nixStreams: develop/shell/run/repl/daemon open a session or run a program.
func nixStreams(args []string) bool {
	switch firstOperand(args) {
	case "develop", "shell", "run", "repl", "daemon", "edit":
		return true
	}
	return false
}

// nixShellStreams: without --run/--command nix-shell opens an interactive shell.
// With one, the command is a shell string the hook can judge only when it is a
// single plain command it already knows (and that finishes); anything with a
// separator, pipe, redirect or substitution is refused.
func nixShellStreams(args []string) bool {
	cmd, found := "", false
	for i, a := range args {
		switch {
		case (a == "--run" || a == "--command") && i+1 < len(args):
			cmd, found = args[i+1], true
		case strings.HasPrefix(a, "--run="):
			cmd, found = strings.TrimPrefix(a, "--run="), true
		case strings.HasPrefix(a, "--command="):
			cmd, found = strings.TrimPrefix(a, "--command="), true
		}
	}
	if !found || strings.ContainsAny(cmd, ";|&<>$`()\n\\") {
		return true
	}
	// Quote-aware, and bounded by the argv shrinking at every level: the launcher
	// chain nix-shell -> ssh -> nix-shell cannot loop.
	fields, ok := nestedcmd.Split(shellTokenValue(cmd))
	return !ok || !hookKnowsInner(fields, 1)
}

// langStreams routes the language toolchains of the third wave.
func langStreams(program string, args []string) bool {
	switch program {
	case "sbt":
		return sbtStreams(args)
	case "mix":
		return mixStreams(args)
	case "dart":
		return dartStreams(args)
	case "zig":
		return zigStreams(args)
	case "rebar3":
		return hasOperand(args, "shell", "auto", "xref_watch")
	case "cabal":
		switch firstOperand(args) {
		case "run", "repl", "exec", "v2-run", "v2-repl", "v2-exec", "new-run", "new-repl", "new-exec":
			return true
		}
	case "ghc":
		return anyArg(args, "--interactive", "-interactive", "-e", "--ghci")
	case "bazel", "bazelisk":
		return firstOperand(args) == "run"
	case "nix":
		return nixStreams(args)
	case "nix-shell":
		return nixShellStreams(args)
	}
	return false
}

// dbStreams routes the database clients and migration tools.
func dbStreams(program string, args []string) bool {
	switch program {
	case "mysql", "mariadb":
		// REPL unless a statement or an informational flag is given. `-e` may be
		// attached (`-e'select 1'`) or end a short-flag cluster (`-Nse`, `-Be`).
		return !(anyArg(args, "-e", "--execute", "-V", "--version", "-?", "--help", "-I") ||
			anyPrefix(args, "--execute=", "-e") || mysqlShortClusterHasE(args))
	case "mongosh":
		// REPL with no script; `--shell` keeps the REPL open AFTER a script.
		if anyArg(args, "--shell", "--nodb") {
			return true
		}
		return !(anyArg(args, "--eval", "-f", "--file", "--version", "-h", "--help", "--build-info") ||
			anyPrefix(args, "--eval=", "--file=") || hasScriptOperand(args))
	case "sqlite3":
		// `sqlite3 FILE` with no SQL operand opens the REPL. Operands are the
		// non-flag arguments after dropping the values of -cmd -init -separator
		// -newline -nullvalue -vfs; FILE plus at least one SQL/dot-command is
		// needed. -version/-help/-? are finite.
		if anyArg(args, "-version", "--version", "-help", "--help", "-?") {
			return false
		}
		return sqliteOperands(args) < 2 || anyArg(args, "-interactive")
	case "redis-cli":
		return rediscli.Interactive(append([]string{program}, args...))
	case "clickhouse-client":
		return clickhouse.Interactive(append([]string{program}, args...))
	case "dbt":
		// `dbt docs serve` blocks forever; `dbt init` prompts.
		op := firstOperand(args)
		return op == "init" || (op == "docs" && hasOperand(args, "serve"))
	case "liquibase":
		// `liquibase init start-h2` serves an H2 console forever; `init project` prompts.
		return hasOperand(args, "init", "start-h2")
	}
	return false // every flyway verb terminates
}

// mysqlShortClusterHasE reports a short-option cluster that ends in e and is
// otherwise made only of mysql's value-less letters (`-Nse`, `-Be`, `-tBe`).
// A glued value such as the password in `-psecure` starts with a value-taking
// option and is not a cluster: treating it as `-e` would wrap a REPL.
func mysqlShortClusterHasE(args []string) bool {
	for _, a := range args {
		if len(a) < 3 || a[0] != '-' || a[1] == '-' || a[len(a)-1] != 'e' {
			continue
		}
		if strings.Trim(a[1:len(a)-1], "NsBtvErqCfXAUHniLojw") == "" {
			return true
		}
	}
	return false
}

// hasScriptOperand reports a mongosh script operand (*.js / *.mongodb).
func hasScriptOperand(args []string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") && (strings.HasSuffix(a, ".js") || strings.HasSuffix(a, ".mongodb")) {
			return true
		}
	}
	return false
}

// sqliteOperands counts sqlite3's non-flag operands, skipping the values of the
// options that take one.
func sqliteOperands(args []string) int {
	n := 0
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-cmd", "-init", "-separator", "-newline", "-nullvalue", "-vfs":
			i++
		default:
			if !strings.HasPrefix(a, "-") {
				n++
			}
		}
	}
	return n
}
