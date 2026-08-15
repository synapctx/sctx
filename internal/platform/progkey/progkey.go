// Package progkey derives the program key that identifies a command in telemetry:
// either "program" or "program subcommand", never anything more.
//
// It exists because that derivation used to be a copy of the same `subcommandish`
// predicate in two places (the hook's coverage-gap path and the run pipeline's
// exec-savings path), and the predicate was wrong in both: it joined the second token
// whenever the token LOOKED like a subcommand — short, lowercase, no punctuation.
//
// A hostname looks exactly like that. So does a directory name. Real telemetry collected
// before this package existed contains `ssh vm` (a host), and `find app`, `find css`,
// `find internal`, `find lib`, `ls migrations`, `ls templates`, `ls app` (directories) —
// against a code comment promising "paths and filenames never leak into telemetry". The
// promise held only for tokens carrying a `/`, a `.`, or an uppercase letter; a bare
// lowercase word walked straight through.
//
// The fix is to stop inferring from the token and decide from the PROGRAM: token shape
// cannot distinguish an operation from an argument, but the program's own CLI grammar can.
// `git` takes a subcommand and `ssh` takes a host, and that is knowable up front.
//
// Getting this wrong twice over also distorted the coverage-gap meter that decides which
// formatter to build next: `ssh` was split across `ssh` and `ssh vm`, `find` across four
// keys, `ls` across three, so every one of them ranked lower than its real usage.
package progkey

import (
	"strings"

	"github.com/synapctx/sctx/internal/platform/gitargv"
)

// subcommandBearing lists programs whose FIRST ARGUMENT names an operation from a bounded
// vocabulary defined by the tool itself — `git status`, `cargo build`, `terraform plan`.
// For these, joining the token adds real signal and cannot carry user data.
//
// Everything absent from this set is treated as taking ARGUMENTS, so only the program name
// is recorded. That is the safe default and it is deliberately the default: a program not
// listed here is one nobody has classified, and the cost of guessing wrong is shipping a
// customer's hostname or directory name to the platform.
//
// Add a program here when the coverage-gap meter shows it crossing the delegation
// threshold AND its first argument is genuinely an operation. Do not add a program whose
// first argument is a path, host, file, target, package, or URL.
var subcommandBearing = map[string]bool{
	// Mirrors every program in the hook's subcommandTable that declares subcommands.
	// TestSubcommandTableIsCoveredByProgkey enforces that correspondence, so a new
	// rewrite-table entry cannot silently lose its telemetry granularity.
	"go": true, "git": true, "docker": true, "kubectl": true, "gh": true,
	"golangci-lint": true, "ruff": true, "brew": true, "pip": true, "pip3": true,
	"npm": true, "pnpm": true, "yarn": true,

	// Not rewritten by sctx yet — these are the coverage-gap programs whose
	// subcommand genuinely identifies the workload. `terraform plan` is output-heavy
	// where `terraform apply` is not, and that difference is the whole point of the
	// meter.
	"cargo": true, "terraform": true, "aws": true, "gcloud": true, "helm": true,
	// The rest of the cloud/IaC family, added when they gained rewrite-table rows.
	// Each takes a service or verb from its own vocabulary as argv[1] — never a
	// path — so the key stays free of customer data while the meter keeps telling
	// `terraform plan` from `terraform apply`.
	"az": true, "tofu": true, "terragrunt": true, "pulumi": true,
	"glab": true, "gt": true, "jj": true, "poetry": true, "uv": true,
	"mix": true, "dotnet": true, "systemctl": true, "pre-commit": true,
	"rustup": true, "deno": true, "bun": true, "apt": true, "apt-get": true,
	"gem": true, "bundle": true, "composer": true, "vault": true, "consul": true,
	"nomad": true, "doctl": true, "flyctl": true, "wrangler": true, "argocd": true,
}

// HasSubcommands reports whether program's first argument names an operation rather than
// data. Exported so the hook can assert its rewrite table stays in correspondence.
func HasSubcommands(program string) bool { return subcommandBearing[program] }

// Key returns the telemetry program key for a command whose program is program and whose
// following token is next (empty if there is none).
//
// next is joined only when the PROGRAM is known to take subcommands and the token also has
// the shape of one. Both gates are needed: the program gate stops `ssh myhost` and
// `ls migrations`, and the shape gate stops `git -C`, `docker --rm` and `go ./...` from
// producing a key built out of a flag or a path.
func Key(program, next string) string {
	program = basename(program)
	if program == "" {
		return ""
	}
	if next == "" || !subcommandBearing[program] || !looksLikeSubcommand(next) {
		return program
	}
	return program + " " + next
}

// FromArgv derives a key from a full argv. Git is special because global
// options may precede its command; it uses the same parser as the hook and
// formatter so `git -C repo status` is still attributed to `git status`.
func FromArgv(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	program := basename(argv[0])
	if program == "git" {
		if inv, ok := gitargv.Parse(append([]string{"git"}, argv[1:]...)); ok {
			return Key(program, inv.Command)
		}
		return program
	}
	var next string
	if len(argv) > 1 {
		next = argv[1]
	}
	return Key(program, next)
}

// basename strips the directory from an invoked program, so a program key is a
// program NAME and never a location.
//
// Found in our own spool the first time `sctx telemetry --preview` was run
// against real data: `./bin/sctx` had been recorded verbatim, under a disclosure
// promising we never send file paths. The program token is the one argument
// position nobody thought of as an argument — but `./scripts/deploy.sh` and
// `/opt/acme/internal/rotate-prod-keys.sh` are invoked exactly the same way, and
// the path is the sensitive half of both.
//
// The NAME is kept because that is what the meter measures. `deploy.sh` is a
// legitimate observation; where it lives is not.
func basename(program string) string {
	if i := strings.LastIndexAny(program, `/\`); i >= 0 {
		return program[i+1:]
	}
	return program
}

// looksLikeSubcommand reports whether tok has the shape of a subcommand: a short,
// lowercase, unpunctuated word. This is NOT sufficient on its own — that was the original
// bug — it only rejects flags, paths and filenames for programs already known to take
// subcommands.
func looksLikeSubcommand(tok string) bool {
	if tok == "" || len(tok) > 20 {
		return false
	}
	if tok[0] < 'a' || tok[0] > 'z' {
		return false
	}
	for i := 1; i < len(tok); i++ {
		c := tok[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}
