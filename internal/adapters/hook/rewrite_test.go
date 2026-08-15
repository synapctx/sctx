package hook

import (
	"reflect"
	"testing"
)

// rewriteTestCase is a single TestRewrite table row. It's also used to seed
// FuzzRewrite (see rewrite_fuzz_test.go) so the fuzzer starts from every
// hand-picked case instead of purely random input.
type rewriteTestCase struct {
	name   string
	cmd    string
	want   string
	wantOK bool
}

// rewriteTestCases returns TestRewrite's table.
func rewriteTestCases() []rewriteTestCase {
	return []rewriteTestCase{
		{"go test", "go test ./...", "sctx go test ./...", true},
		{"go build", "go build ./cmd/foo", "sctx go build ./cmd/foo", true},
		{"go vet", "go vet ./...", "sctx go vet ./...", true},
		{"git status", "git status", "sctx git status", true},
		{"git log", "git log --oneline", "sctx git log --oneline", true},
		{"git diff", "git diff HEAD~1", "sctx git diff HEAD~1", true},
		{"git show", "git show HEAD", "sctx git show HEAD", true},
		{"git add", "git add -A", "sctx git add -A", true},
		{"git commit", "git commit -m x", "sctx git commit -m x", true},
		{"git push", "git push origin main", "sctx git push origin main", true},
		{"git pull", "git pull", "sctx git pull", true},
		{"git fetch", "git fetch --all", "sctx git fetch --all", true},
		{"grep", "grep -rn foo .", "sctx grep -rn foo .", true},
		{"rg", "rg foo", "sctx rg foo", true},
		{"ls", "ls -la", "sctx ls -la", true},
		{"find", "find . -name x", "sctx find . -name x", true},
		{"tree", "tree", "sctx tree", true},
		{"docker ps", "docker ps -a", "sctx docker ps -a", true},
		{"docker images", "docker images", "sctx docker images", true},
		{"docker logs", "docker logs c1", "sctx docker logs c1", true},
		{"docker compose detached up", "docker compose up -d", "sctx docker compose up -d", true},
		{"kubectl get", "kubectl get pods", "sctx kubectl get pods", true},
		{"kubectl describe", "kubectl describe pod x", "sctx kubectl describe pod x", true},
		{"kubectl logs", "kubectl logs pod x", "sctx kubectl logs pod x", true},
		{"gh pr", "gh pr list", "sctx gh pr list", true},
		{"gh issue", "gh issue view 1", "sctx gh issue view 1", true},
		{"gh run", "gh run list", "sctx gh run list", true},
		{"gh repo", "gh repo view", "sctx gh repo view", true},
		{"gh api", "gh api repos/x/y", "sctx gh api repos/x/y", true},
		{"gh release", "gh release list", "sctx gh release list", true},
		{"gh global repo before command", "gh -R cli/cli pr list", "sctx gh -R cli/cli pr list", true},
		{"gh pr status", "gh pr status", "sctx gh pr status", true},
		{"gh pr diff", "gh pr diff 12", "sctx gh pr diff 12", true},
		{"gh run view logs", "gh run view 12 --log-failed", "sctx gh run view 12 --log-failed", true},
		{"gh checks watch declines", "gh pr checks 12 --watch", "gh pr checks 12 --watch", false},
		{"gh run watch declines", "gh run watch 12", "gh run watch 12", false},
		{"gh web declines", "gh repo view cli/cli --web", "gh repo view cli/cli --web", false},
		{"gh mutation declines", "gh pr create --fill", "gh pr create --fill", false},
		{"gh API stdin declines", "gh api graphql --input -", "gh api graphql --input -", false},
		{"gh API implicit POST declines", "gh api repos/x/y/issues -f title=x", "gh api repos/x/y/issues -f title=x", false},
		{"gh API explicit GET with field", "gh api search/issues --method GET -f q=x", "sctx gh api search/issues --method GET -f q=x", true},
		{"gh search prs", "gh search prs --repo cli/cli --limit 30", "sctx gh search prs --repo cli/cli --limit 30", true},
		{"gh workflow list", "gh workflow list -R cli/cli", "sctx gh workflow list -R cli/cli", true},
		{"gh workflow view", "gh workflow view go.yml -R cli/cli", "sctx gh workflow view go.yml -R cli/cli", true},
		{"gh workflow selector declines", "gh workflow view", "gh workflow view", false},
		{"gh workflow mutation declines", "gh workflow run go.yml", "gh workflow run go.yml", false},
		{"gh cache list", "gh cache list -R cli/cli", "sctx gh cache list -R cli/cli", true},
		{"gh cache mutation declines", "gh cache delete --all", "gh cache delete --all", false},
		{"gh gist list", "gh gist list --public", "sctx gh gist list --public", true},
		{"gh gist view", "gh gist view abc", "sctx gh gist view abc", true},
		{"gh gist selector declines", "gh gist view", "gh gist view", false},
		{"gh gist mutation declines", "gh gist create file.txt", "gh gist create file.txt", false},
		{"gh project list", "gh project list --owner cli", "sctx gh project list --owner cli", true},
		{"gh project item list", "gh project item-list 1 --owner cli", "sctx gh project item-list 1 --owner cli", true},
		{"gh project mutation declines", "gh project item-add 1 --owner cli", "gh project item-add 1 --owner cli", false},
		{"ssh finite nested command", "ssh host 'go test ./...'", "sctx ssh host 'go test ./...'", true},
		{"ssh interactive declines", "ssh host", "ssh host", false},
		{"ssh forced tty declines", "ssh -t host 'go test ./...'", "ssh -t host 'go test ./...'", false},
		{"ssh compound declines", "ssh host 'go test && echo done'", "ssh host 'go test && echo done'", false},
		{"ssh unknown inner declines", "ssh host uptime", "ssh host uptime", false},
		{"golangci-lint run", "golangci-lint run", "sctx golangci-lint run", true},
		{"make", "make build", "sctx make build", true},
		{"ps", "ps aux", "sctx ps aux", true},
		{"diff", "diff a b", "sctx diff a b", true},
		{"cat", "cat file.txt", "sctx cat file.txt", true},
		{"head", "head -n 5 file.txt", "sctx head -n 5 file.txt", true},
		{"tail", "tail -n 50 file.txt", "sctx tail -n 50 file.txt", true},
		// A FOLLOWING COMMAND MUST NOT BE WRAPPED. sctx reads stdout to EOF before
		// it formats, and `tail -f` never reaches EOF — so wrapping turns "the agent
		// sees lines until its timeout" into "the agent sees nothing at all". This
		// case previously asserted the opposite.
		{"tail follows", "tail -f file.txt", "tail -f file.txt", false},
		{"kubectl logs follows", "kubectl logs -f pod", "kubectl logs -f pod", false},
		{"kubectl global flags before get", "kubectl --context dev --request-timeout 5s -n ns get pods", "sctx kubectl --context dev --request-timeout 5s -n ns get pods", true},
		{"kubectl get watches", "kubectl --context dev get pods --watch", "kubectl --context dev get pods --watch", false},
		{"kubectl events watches", "kubectl events --watch-only", "kubectl events --watch-only", false},
		{"kubectl exec interactive", "kubectl exec -it pod -- go test ./...", "kubectl exec -it pod -- go test ./...", false},
		{"kubectl exec missing separator", "kubectl exec pod go test ./...", "kubectl exec pod go test ./...", false},
		{"docker compose logs follows", "docker compose logs -f api", "docker compose logs -f api", false},
		{"docker compose foreground up", "docker compose up", "docker compose up", false},
		{"docker live stats", "docker stats", "docker stats", false},
		{"docker finite stats", "docker stats --no-stream", "sctx docker stats --no-stream", true},
		{"docker global context", "docker -c desktop-linux ps", "sctx docker -c desktop-linux ps", true},
		{"docker complete globals", "docker --tlsverify --tlscert cert.pem -H tcp://host:2376 ps", "sctx docker --tlsverify --tlscert cert.pem -H tcp://host:2376 ps", true},
		{"docker compose globals", "docker compose -f compose.yaml -p demo ps", "sctx docker compose -f compose.yaml -p demo ps", true},
		{"docker image ls alias", "docker image ls", "sctx docker image ls", true},
		// …but -f meaning a FILE must keep its coverage.
		{"docker build -f is a file", "docker build -f Dockerfile .", "sctx docker build -f Dockerfile .", true},
		{"kubectl apply -f is a file", "kubectl apply -f manifest.yaml", "sctx kubectl apply -f manifest.yaml", true},

		{"go env not in table", "go env GOPATH", "go env GOPATH", false},
		{"docker save not in table", "docker save img", "docker save img", false},
		{"kubectl port-forward not in table", "kubectl port-forward pod 8080", "kubectl port-forward pod 8080", false},
		{"git rebase finite fallback", "git rebase main", "sctx git rebase main", true},
		{"gh auth not in table", "gh auth login", "gh auth login", false},
		{"golangci-lint linters not in table", "golangci-lint linters", "golangci-lint linters", false},

		{"already sctx-wrapped", "sctx git status", "sctx git status", false},
		{"already sctx-wrapped", "sctx go test", "sctx go test", false},

		{"reserved bare gain", "gain", "gain", false},
		{"reserved bare flush", "flush", "flush", false},
		{"reserved bare doctor", "doctor", "doctor", false},
		{"reserved bare version", "version", "version", false},
		{"reserved bare help", "help", "help", false},
		{"reserved bare hook", "hook", "hook", false},

		{"env assignment prefix", "FOO=1 go test ./...", "FOO=1 sctx go test ./...", true},
		{"multiple env assignments", "FOO=1 BAR=baz git status", "FOO=1 BAR=baz sctx git status", true},

		{"metachar && wraps first segment", "go test && echo hi", "sctx go test && echo hi", true},
		{"metachar ; wraps every eligible segment", "git status; ls", "sctx git status; sctx ls", true},
		{"metachar | wraps safe pipeline head", "git status | head", "sctx git status | head", true},
		{"metachar backtick bails out", "git status `echo x`", "git status `echo x`", false},
		{"metachar $( bails out", "git status $(echo x)", "git status $(echo x)", false},
		// A newline is a separator, like ';'. Declining cost the savings on every
		// multi-line command — heredocs, loop bodies, two statements on two lines —
		// which are among the most output-heavy an agent runs.
		{"newline separates commands", "git status\nls", "sctx git status\nsctx ls", true},
		{"metachar redirect bails out", "git status > out.txt", "git status > out.txt", false},
		{"metachar || wraps first segment", "git status || true", "sctx git status || true", true},

		{"quoted args preserved verbatim", `grep -rn "foo bar" .`, `sctx grep -rn "foo bar" .`, true},

		{"empty command", "", "", false},
		{"whitespace only", "   ", "   ", false},
		{"unknown program", "mix test", "mix test", false},

		{"pytest", "pytest -q", "sctx pytest -q", true},
		{"pytest piped to tail", "pytest 2>&1 | tail -50", "sctx pytest 2>&1 | tail -50", true},
		{"mypy", "mypy app", "sctx mypy app", true},
		{"ruff check", "ruff check .", "sctx ruff check .", true},
		{"ruff format", "ruff format .", "sctx ruff format .", true},
		{"ruff bare not in table", "ruff --version", "ruff --version", false},
		{"brew install", "brew install jq", "sctx brew install jq", true},
		{"brew upgrade", "brew upgrade", "sctx brew upgrade", true},
		{"brew list not in table", "brew list", "brew list", false},
		{"pip install", "pip install requests", "sctx pip install requests", true},
		{"pip list piped to head", "pip list | head", "sctx pip list | head", true},
		{"pip3 show", "pip3 show numpy", "sctx pip3 show numpy", true},
		{"pip uninstall", "pip uninstall -y old", "sctx pip uninstall -y old", true},
		{"pip freeze not-listed subcommand still covered", "pip freeze", "sctx pip freeze", true},
		{"mongosh eval", "mongosh --quiet --eval 'db.c.find()'", "sctx mongosh --quiet --eval 'db.c.find()'", true},
		{"du recursive", "du -h .", "sctx du -h .", true},
		{"jq bare", "jq . data.json", "sctx jq . data.json", true},
		{"curl json api", "curl -s https://api.example.com/v1/x", "sctx curl -s https://api.example.com/v1/x", true},
		{"npm install", "npm install", "sctx npm install", true},
		{"npm ci piped to tail", "npm ci 2>&1 | tail -30", "sctx npm ci 2>&1 | tail -30", true},
		{"pnpm test", "pnpm test", "sctx pnpm test", true},
		{"yarn add", "yarn add lodash", "sctx yarn add lodash", true},
		{"npm publish not in table", "npm publish", "npm publish", false},

		{"go mod tidy", "go mod tidy", "sctx go mod tidy", true},
		{"go list packages", "go list ./...", "sctx go list ./...", true},
		{"go run", "go run .", "sctx go run .", true},
		{"go doc not in table", "go doc fmt", "go doc fmt", false},
		{"git branch", "git branch -a", "sctx git branch -a", true},
		{"git stash list", "git stash list", "sctx git stash list", true},
		{"git blame piped to head", "git blame main.go | head", "sctx git blame main.go | head", true},
		{"git ls-files", "git ls-files", "sctx git ls-files", true},
		{"git worktree list", "git worktree list", "sctx git worktree list", true},
		{"git cherry-pick finite fallback", "git cherry-pick abc", "sctx git cherry-pick abc", true},
		{"git global flags and unknown finite verb", "git --no-pager -C repo -c color.ui=false check-ignore file", "sctx git --no-pager -C repo -c color.ui=false check-ignore file", true},
		{"absolute git path", "/usr/bin/git -C repo status", "sctx /usr/bin/git -C repo status", true},
		{"git ls-remote finite fallback", "git ls-remote origin", "sctx git ls-remote origin", true},
		{"git interactive add declines", "git add -p", "git add -p", false},
		{"git editor commit declines", "git commit", "git commit", false},
		{"git message commit wraps", "git commit -m message", "sctx git commit -m message", true},
		{"git interactive rebase declines", "git rebase -i HEAD~2", "git rebase -i HEAD~2", false},
		{"git batch protocol declines", "git cat-file --batch", "git cat-file --batch", false},
		{"git daemon declines", "git daemon", "git daemon", false},
		{"docker build", "docker build -t x .", "sctx docker build -t x .", true},
		{"docker inspect", "docker inspect c1", "sctx docker inspect c1", true},
		{"docker pull", "docker pull nginx", "sctx docker pull nginx", true},
		{"docker network ls", "docker network ls", "sctx docker network ls", true},
		{"docker exec shell declines", "docker exec c1 sh", "docker exec c1 sh", false},
		{"docker exec delegates", "docker exec c1 go test ./...", "sctx docker exec c1 go test ./...", true},
		{"docker exec interactive", "docker exec -it c1 go test ./...", "docker exec -it c1 go test ./...", false},
		{"docker compose exec delegates", "docker compose exec -T --interactive=false api git status", "sctx docker compose exec -T --interactive=false api git status", true},
		{"docker compose exec defaults decline", "docker compose exec api git status", "docker compose exec api git status", false},
		{"kubectl top pods", "kubectl top pods", "sctx kubectl top pods", true},
		{"kubectl apply", "kubectl apply -f x.yaml", "sctx kubectl apply -f x.yaml", true},
		{"kubectl rollout status", "kubectl rollout status deploy/x", "sctx kubectl rollout status deploy/x", true},
		{"kubectl api-resources", "kubectl api-resources", "sctx kubectl api-resources", true},
		{"kubectl exec shell declines", "kubectl exec pod -- sh", "kubectl exec pod -- sh", false},
		{"kubectl exec delegates", "kubectl exec pod -- go test ./...", "sctx kubectl exec pod -- go test ./...", true},

		{"go test with stderr merge then tail", "go test ./... 2>&1 | tail -50", "sctx go test ./... 2>&1 | tail -50", true},
		{"go test piped to head", "go test ./... | head -20", "sctx go test ./... | head -20", true},
		{"cd then go test", "cd sub && go test ./...", "cd sub && sctx go test ./...", true},
		{"cd cd then go test", "cd a && cd b && go test ./...", "cd a && cd b && sctx go test ./...", true},
		{"cd then env then go test", "cd sub && FOO=1 go test ./...", "cd sub && FOO=1 sctx go test ./...", true},
		{"git log piped to head", "git log --oneline | head", "sctx git log --oneline | head", true},
		{"mkdir noise then ls", "mkdir -p x; ls -la", "mkdir -p x; sctx ls -la", true},
		{"go test or true", "go test ./... || true", "sctx go test ./... || true", true},
		{"cd then go test piped to tail", "cd sub && go test ./... 2>&1 | tail -20", "cd sub && sctx go test ./... 2>&1 | tail -20", true},
		{"go test and go vet both wrap", "go test && go vet", "sctx go test && sctx go vet", true},
		{"go test piped to cat", "go test | cat", "sctx go test | cat", true},
		{"git diff piped to less", "git diff | less", "sctx git diff | less", true},
		{"grep with quoted && preserved", `grep 'a && b' file.txt`, `sctx grep 'a && b' file.txt`, true},
		{"grep with quoted pipe preserved", `grep "a|b" file.txt`, `sctx grep "a|b" file.txt`, true},
		{"find with escaped semicolon", `find . -name x -exec rm {} \;`, `sctx find . -name x -exec rm {} \;`, true},
		{"echo then git status", "echo done && git status", "echo done && sctx git status", true},

		{"go test piped to grep declines", "go test ./... | grep FAIL", "go test ./... | grep FAIL", false},
		{"go test piped to rg declines", "go test | rg FAIL", "go test | rg FAIL", false},
		{"go test piped through tail then grep declines", "go test | tail -5 | grep ok", "go test | tail -5 | grep ok", false},
		{"go test piped to wc declines", "go test | wc -l", "go test | wc -l", false},
		{"go test with stderr redirect to file declines", "go test 2> err.txt", "go test 2> err.txt", false},
		{"go test append redirect declines", "go test >> log.txt", "go test >> log.txt", false},
		// An INPUT redirect does not touch stdout, and sctx forwards stdin, so there is
		// nothing to protect against. Only output redirects disqualify a segment.
		{"input redirect is fine", "go test < in.txt", "sctx go test < in.txt", true},
		{"output redirect still declines", "go test > out.txt", "go test > out.txt", false},
		{"append redirect still declines", "go test >> out.txt", "go test >> out.txt", false},
		{"stderr-to-file still declines", "go test 2> err.txt", "go test 2> err.txt", false},
		{"go test stderr to devnull declines", "go test 2>/dev/null", "go test 2>/dev/null", false},
		{"go test piped to tail then redirected declines", "go test | tail > out.txt", "go test | tail > out.txt", false},
		{"go test with command substitution arg declines", "go test $(date)", "go test $(date)", false},
		{"go test with backtick arg declines", "go test `date`", "go test `date`", false},
		{"go test with process substitution declines", "go test <(cmd)", "go test <(cmd)", false},
		{"subshell declines", "(cd x && go test ./...)", "(cd x && go test ./...)", false},
		{"backgrounded declines", "go test &", "go test &", false},
		{"pipe-amp declines", "go test |& tail", "go test |& tail", false},
		{"newline chain wraps every eligible segment", "go test\ngo vet", "sctx go test\nsctx go vet", true},
		// HEREDOC BODIES ARE DATA. A line inside one can look exactly like a command —
		// a commit message quoting `go test ./...`, docs showing `git status` — and
		// inserting sctx there would corrupt the text being written.
		{"heredoc body is never rewritten",
			"git commit -F - <<EOF\ngo test ./...\ngit status\nEOF",
			"sctx git commit -F - <<EOF\ngo test ./...\ngit status\nEOF", true},
		{"command after a heredoc still rewrites",
			"cat <<EOF > f.txt\nls -la\nEOF\ngo vet ./...",
			"cat <<EOF > f.txt\nls -la\nEOF\nsctx go vet ./...", true},
		// `cat` is itself a table program with no OUTPUT redirect, so it is the first
		// eligible segment and gets wrapped — the heredoc body is still untouched.
		{"tab-stripped heredoc delimiter",
			"cat <<-END\n\tgit status\n\tEND\ngo build ./...",
			"sctx cat <<-END\n\tgit status\n\tEND\nsctx go build ./...", true},
		{"quoted heredoc delimiter",
			"cat <<'EOF'\ngit log --oneline\nEOF\ngo test ./...",
			"sctx cat <<'EOF'\ngit log --oneline\nEOF\nsctx go test ./...", true},
		// `<<` inside a quoted string is TEXT, not a redirect. Parsing it as a heredoc
		// swallows the rest of the command.
		{"heredoc marker inside quotes is not a heredoc",
			`git commit -m "use << for heredocs"`,
			`sctx git commit -m "use << for heredocs"`, true},
		// `<<-` strips leading TABS only. Matching spaces too terminates on a line
		// bash treats as body, truncating the heredoc and turning its remaining lines
		// into commands.
		// `tee` is NOT a table program, so nothing here is wrappable while the heredoc
		// is parsed correctly — `git status` is body text. If the terminator matched on
		// spaces, the heredoc would end at "  EOF", `git status` would become a real
		// segment, and it would be wrapped. The non-wrappable head is what makes the
		// two outcomes distinguishable; with `cat` in front they are identical and the
		// test proves nothing.
		{"space-indented delimiter is body, not terminator",
			"tee f.txt <<EOF\n  EOF\ngit status\nEOF",
			"tee f.txt <<EOF\n  EOF\ngit status\nEOF", false},
		// An unterminated heredoc cannot be delimited, so the whole command declines
		// rather than being guessed at.
		{"unterminated heredoc declines",
			"cat <<EOF\ngo test ./...\n", "cat <<EOF\ngo test ./...\n", false},
		{"quoted pipe with covered inner program declines", "echo 'foo | go test ./...'", "echo 'foo | go test ./...'", false},
		{"unterminated double quote declines", `git status "unterminated`, `git status "unterminated`, false},
		{"unterminated single quote declines", "git status 'unterminated", "git status 'unterminated", false},
		{"trailing backslash declines", `go test \`, `go test \`, false},
		{"already sctx-wrapped compound declines", "sctx go test && sctx go vet", "sctx go test && sctx go vet", false},
		{"sctx-wrapped segment anywhere declines", "ls && sctx git status", "ls && sctx git status", false},
		{"leading empty segment declines", "| head", "| head", false},
		{"trailing separator empty segment declines", "go test &&", "go test &&", false},
		{"doubled separator empty segment declines", "go test && && ls", "go test && && ls", false},
		{"trailing semicolon empty segment declines", "git status ;", "git status ;", false},
		{"unknown program piped to safe downstream declines", "foo | tail", "foo | tail", false},
		{"cat piped to grep declines", "cat a.txt | grep x", "cat a.txt | grep x", false},
		{"reserved name piped to safe downstream declines", "hook | tail", "hook | tail", false},
	}
}

func TestRewrite(t *testing.T) {
	for _, tt := range rewriteTestCases() {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := rewrite(tt.cmd)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("rewrite(%q) = (%q, %v), want (%q, %v)", tt.cmd, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestGapSegment(t *testing.T) {
	tests := []struct {
		name        string
		cmd         string
		wantProgram string
		wantOK      bool
	}{
		{"bare uncovered program", "mix test", "mix test", true},
		{"uncovered program piped to safe downstream", "mix test 2>&1 | tail -5", "mix test", true},
		{"cd then uncovered program", "cd sub && mix test", "mix test", true},
		{"uncovered program piped to head", "vault read secret/db | head", "vault read", true},
		{"covered program uncovered subcommand", "go env GOPATH", "go env", true},

		{"uncovered program piped to grep declines", "go test ./... | grep FAIL", "", false},
		{"already-covered program declines", "git status", "", false},
		{"interactive git is deliberate, not a gap", "git add -p", "", false},
		{"streaming gh is deliberate, not a gap", "gh run watch 12", "", false},
		{"file redirect declines", "mix test > out.txt", "", false},
		{"unsafe downstream pipe declines", "mix test | grep x", "", false},
		{"command substitution declines", "mix test $(x)", "", false},
		{"only noise builtin declines", "cd sub", "", false},
		{"bare noise builtin declines", "echo hi", "", false},
		{"already sctx-wrapped declines", "sctx npm test", "", false},
		{"empty command declines", "", "", false},
		{"whitespace only declines", "   ", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := gapSegment(tt.cmd)
			if ok != tt.wantOK {
				t.Fatalf("gapSegment(%q) ok = %v, want %v", tt.cmd, ok, tt.wantOK)
			}
			if ok && deriveProgram(got) != tt.wantProgram {
				t.Errorf("gapSegment(%q) segment %q derives program %q, want %q", tt.cmd, got, deriveProgram(got), tt.wantProgram)
			}
		})
	}
}

func TestSplitSegments(t *testing.T) {
	tests := []struct {
		name   string
		cmd    string
		want   []segment
		wantOK bool
	}{
		{
			name:   "single segment",
			cmd:    "go test ./...",
			want:   []segment{{text: "go test ./...", start: 0, pipeFrom: false}},
			wantOK: true,
		},
		{
			name: "semicolon",
			cmd:  "git status; ls",
			want: []segment{
				{text: "git status", start: 0, pipeFrom: false},
				{text: " ls", start: 11, pipeFrom: false},
			},
			wantOK: true,
		},
		{
			name: "and-and",
			cmd:  "go test && echo hi",
			want: []segment{
				{text: "go test ", start: 0, pipeFrom: false},
				{text: " echo hi", start: 10, pipeFrom: false},
			},
			wantOK: true,
		},
		{
			name: "or-or",
			cmd:  "go test ./... || true",
			want: []segment{
				{text: "go test ./... ", start: 0, pipeFrom: false},
				{text: " true", start: 16, pipeFrom: false},
			},
			wantOK: true,
		},
		{
			name: "pipe",
			cmd:  "git status | head",
			want: []segment{
				{text: "git status ", start: 0, pipeFrom: false},
				{text: " head", start: 12, pipeFrom: true},
			},
			wantOK: true,
		},
		{
			name: "chained cd and pipe",
			cmd:  "cd sub && go test ./... 2>&1 | tail -50",
			want: []segment{
				{text: "cd sub ", start: 0, pipeFrom: false},
				{text: " go test ./... 2>&1 ", start: 9, pipeFrom: false},
				{text: " tail -50", start: 30, pipeFrom: true},
			},
			wantOK: true,
		},
		{
			name:   "fd dup not a separator",
			cmd:    "go test ./... 2>&1",
			want:   []segment{{text: "go test ./... 2>&1", start: 0, pipeFrom: false}},
			wantOK: true,
		},
		{
			name:   "quoted pipe preserved",
			cmd:    `grep "a|b" file.txt`,
			want:   []segment{{text: `grep "a|b" file.txt`, start: 0, pipeFrom: false}},
			wantOK: true,
		},
		{
			name:   "quoted and-and preserved",
			cmd:    `grep 'a && b' file.txt`,
			want:   []segment{{text: `grep 'a && b' file.txt`, start: 0, pipeFrom: false}},
			wantOK: true,
		},
		{
			name:   "escaped semicolon not a separator",
			cmd:    `find . -name x -exec rm {} \;`,
			want:   []segment{{text: `find . -name x -exec rm {} \;`, start: 0, pipeFrom: false}},
			wantOK: true,
		},

		{name: "newline is a separator", cmd: "git status\nls",
			want: []segment{{text: "git status", start: 0}, {text: "ls", start: 11}}, wantOK: true},
		{name: "backtick hard-unsafe", cmd: "git status `echo x`", want: nil, wantOK: false},
		{name: "dollar-paren hard-unsafe", cmd: "git status $(echo x)", want: nil, wantOK: false},
		{name: "subshell paren hard-unsafe", cmd: "(cd x && go test ./...)", want: nil, wantOK: false},
		{name: "background hard-unsafe", cmd: "go test &", want: nil, wantOK: false},
		{name: "pipe-amp hard-unsafe", cmd: "go test |& tail", want: nil, wantOK: false},
		{name: "unterminated double quote", cmd: `git status "unterminated`, want: nil, wantOK: false},
		{name: "unterminated single quote", cmd: "git status 'unterminated", want: nil, wantOK: false},
		{name: "trailing backslash", cmd: `go test \`, want: nil, wantOK: false},
		{name: "process substitution hard-unsafe", cmd: "go test <(cmd)", want: nil, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := splitSegments(tt.cmd)
			if ok != tt.wantOK {
				t.Fatalf("splitSegments(%q) ok = %v, want %v", tt.cmd, ok, tt.wantOK)
			}
			if ok && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitSegments(%q) = %#v, want %#v", tt.cmd, got, tt.want)
			}
		})
	}
}
