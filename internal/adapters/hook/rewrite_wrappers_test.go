package hook

import "testing"

// TestRewriteWrappersPathsLaunchersOptOut covers shapes that sctx used to
// skip: process wrappers (sctx goes AFTER the wrapper so it still
// governs the process), program paths, package-runner launchers, and the inline
// opt-out. Every "" want is a DECLINE, the fail-open answer for anything the
// parser cannot read unambiguously.
func TestRewriteWrappersPathsLaunchersOptOut(t *testing.T) {
	tests := []struct{ name, in, want string }{
		// wrappers
		{"timeout", "timeout 60 go test ./...", "timeout 60 sctx go test ./..."},
		{"timeout fractional unit", "timeout 1.5m make", "timeout 1.5m sctx make"},
		// -k and non-TERM/INT signals are declined: a KILL can hit sctx while the child
		// ignores TERM, orphaning the child and losing its output (TestPeelHardening).
		{"timeout kill-after and signal declined", "timeout -k 5 -s KILL 60 go test ./...", ""},
		{"timeout long flags", "timeout --preserve-status --signal=TERM 60 go vet ./...", "timeout --preserve-status --signal=TERM 60 sctx go vet ./..."},
		{"timeout unknown flag", "timeout --frobnicate 60 go test ./...", ""},
		{"timeout missing duration", "timeout go test ./...", ""},
		{"timeout nothing to run", "timeout 60", ""},
		{"timeout bad kill-after", "timeout -k soon 60 go test", ""},
		{"timeout kill-after declined", "timeout --kill-after=5s 60 go test", ""},
		{"time", "time go test ./...", "time sctx go test ./..."},
		{"time -p", "time -p go build ./...", "time -p sctx go build ./..."},
		{"time unknown flag", "time -v go build ./...", ""},
		{"nice -n", "nice -n 10 go test ./...", "nice -n 10 sctx go test ./..."},
		{"nice short number", "nice -5 make", "nice -5 sctx make"},
		{"nice bare", "nice make", "nice sctx make"},
		{"nice bad adjustment", "nice -n fast make", ""},
		{"nohup", "nohup go test ./...", "nohup sctx go test ./..."},
		{"stdbuf", "stdbuf -oL go test ./...", "stdbuf -oL sctx go test ./..."},
		{"stdbuf several", "stdbuf -oL -eL make", "stdbuf -oL -eL sctx make"},
		{"stdbuf long", "stdbuf --output=L make", "stdbuf --output=L sctx make"},
		{"stdbuf unknown flag", "stdbuf -x make", ""},
		{"env assignments", "env FOO=1 BAR=2 go test ./...", "env FOO=1 BAR=2 sctx go test ./..."},
		// env -i / PATH changes: the rewritten `sctx` would not be found (exit 127).
		{"env -i declined", "env -i PATH=/bin go build ./...", ""},
		{"env -u", "env -u HOME make", "env -u HOME sctx make"},
		{"env -S unknown", "env -S 'go test' ./...", ""},
		{"env nothing to run", "env FOO=1", ""},
		{"leading assignment before wrapper", "FOO=1 timeout 5 go test ./...", "FOO=1 timeout 5 sctx go test ./..."},
		{"nested wrappers", "nice -n 5 timeout 60 go test ./...", "nice -n 5 timeout 60 sctx go test ./..."},
		{"wrapper around unknown program", "timeout 60 nomad status", ""},
		{"wrapper around streaming command", "timeout 10 tail -f app.log", ""},
		{"wrapper then already wrapped", "timeout 60 sctx go test ./...", ""},
		{"wrapper keeps downstream rule", "timeout 60 go test ./... | grep FAIL", ""},
		{"wrapper with narrowing pipe", "timeout 60 go test ./... 2>&1 | tail -20", "timeout 60 sctx go test ./... 2>&1 | tail -20"},
		{"wrapper with redirect", "timeout 60 go test ./... > out.txt", ""},
		{"sudo is not peeled", "sudo go test ./...", ""},
		{"xargs is not peeled", "xargs go test", ""},
		{"wrapper path not peeled", "/usr/bin/timeout 60 go test ./...", ""},

		// program paths
		{"absolute path", "/usr/local/bin/make build", "sctx /usr/local/bin/make build"},
		{"homebrew rg", "/opt/homebrew/bin/rg foo src", "sctx /opt/homebrew/bin/rg foo src"},
		{"relative path", "./bin/go test ./...", "sctx ./bin/go test ./..."},
		{"path with subcommand table", "/usr/local/go/bin/go test ./...", "sctx /usr/local/go/bin/go test ./..."},
		{"path with subcommand not in table", "/usr/local/go/bin/go env", ""},
		{"path git", "/usr/bin/git status", "sctx /usr/bin/git status"},
		{"path git interactive", "/usr/bin/git rebase -i HEAD~3", ""},
		{"path kubectl follow", "/usr/local/bin/kubectl logs -f pod", ""},
		{"path docker", "/usr/bin/docker ps", "sctx /usr/bin/docker ps"},
		{"path tail follow", "/usr/bin/tail -f x.log", ""},
		{"path unknown", "/usr/bin/nomad status", ""},
		{"path with expansion declines", "$HOME/bin/make", ""},
		{"path with tilde declines", "~/bin/make", ""},
		{"literal gradlew row", "./gradlew build", "sctx ./gradlew build"},
		{"path wrapper combo", "timeout 60 /usr/local/bin/make", "timeout 60 sctx /usr/local/bin/make"},
		{"path to sctx is wrapped already", "/usr/local/bin/sctx go test", ""},

		// launchers
		{"npx tsc", "npx tsc --noEmit", "sctx npx tsc --noEmit"},
		{"npx -y", "npx -y eslint .", "sctx npx -y eslint ."},
		{"npx versioned", "npx tsc@5.4 --noEmit", "sctx npx tsc@5.4 --noEmit"},
		{"npx unknown tool", "npx cowsay hi", ""},
		{"npx unknown flag", "npx -p typescript tsc", ""},
		{"bunx", "bunx tsc", "sctx bunx tsc"},
		{"pnpm dlx", "pnpm dlx eslint .", "sctx pnpm dlx eslint ."},
		{"pnpm exec", "pnpm exec tsc", "sctx pnpm exec tsc"},
		{"npm exec", "npm exec -- tsc", "sctx npm exec -- tsc"},
		{"npm exec unknown tool still its own row", "npm exec cowsay", "sctx npm exec cowsay"},
		{"python -m pytest", "python -m pytest -q", "sctx python -m pytest -q"},
		{"python3 -m mypy", "python3 -m mypy .", "sctx python3 -m mypy ."},
		{"python3.12 -m ruff", "python3.12 -m ruff check .", "sctx python3.12 -m ruff check ."},
		{"python -m ruff unknown subcommand", "python -m ruff server", ""},
		{"python -m pip", "python3 -m pip install -r r.txt", "sctx python3 -m pip install -r r.txt"},
		{"python -m pip unlisted subcommand", "python3 -m pip config list", ""},
		{"python -m other module", "python -m http.server", ""},
		{"bare python script stays unwrapped", "python script.py", ""},
		{"python -c stays unwrapped", "python -c 'print(1)'", ""},
		{"uv run pytest", "uv run pytest -x", "sctx uv run pytest -x"},
		{"uv run unknown tool keeps uv row", "uv run myscript", "sctx uv run myscript"},
		{"uv run decided by inner not outer", "uv run tail -f x.log", ""},
		{"poetry run mypy", "poetry run mypy .", "sctx poetry run mypy ."},
		{"wrapper then launcher", "timeout 60 npx tsc", "timeout 60 sctx npx tsc"},

		// inline opt-out
		{"opt-out 1", "SCT__REWRITE_DISABLED=1 go test ./...", ""},
		{"opt-out true", "SCT__REWRITE_DISABLED=true go test ./...", ""},
		{"opt-out quoted", `SCT__REWRITE_DISABLED="1" go test ./...`, ""},
		{"opt-out after another assignment", "FOO=1 SCT__REWRITE_DISABLED=1 go test ./...", ""},
		{"opt-out via env", "env SCT__REWRITE_DISABLED=1 go test ./...", ""},
		{"opt-out before wrapper", "SCT__REWRITE_DISABLED=1 timeout 5 go test ./...", ""},
		{"opt-out zero does not opt out", "SCT__REWRITE_DISABLED=0 go test ./...", "SCT__REWRITE_DISABLED=0 sctx go test ./..."},
		{"opt-out is per segment", "SCT__REWRITE_DISABLED=1 go test ./... && go vet ./...", "SCT__REWRITE_DISABLED=1 go test ./... && sctx go vet ./..."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rewrite(tc.in)
			if tc.want == "" {
				if ok || got != tc.in {
					t.Fatalf("rewrite(%q) = %q, %v; want declined and unchanged", tc.in, got, ok)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("rewrite(%q)\n got %q (%v)\nwant %q", tc.in, got, ok, tc.want)
			}
		})
	}
}
