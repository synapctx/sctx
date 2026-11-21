package hook

import "testing"

// innerRewriteCases pins that the command a launcher runs gets the SAME decision
// as a top-level one (hookKnowsInner -> matchTokens), plus the rule holes and the
// glued-option-value over-decline closed alongside. Appended to TestRewrite's
// table, so FuzzRewrite seeds from every row.
func innerRewriteCases() []rewriteTestCase {
	var cases []rewriteTestCase
	wrap := func(cmd string) { cases = append(cases, rewriteTestCase{"wrap " + cmd, cmd, "sctx " + cmd, true}) }
	keep := func(cmd string) { cases = append(cases, rewriteTestCase{"decline " + cmd, cmd, cmd, false}) }

	// A wrapper in front of a follow: the inner decision peels it like the outer one.
	keep("ssh h 'env X=1 tail -f x'")
	keep("docker exec c env X=1 tail -f x")
	keep("kubectl exec pod -- env X=1 tail -f x")
	keep("docker run --rm img env X=1 tail -f x")
	keep("nix-shell --run 'env X=1 tail -f x'")
	keep("ssh h 'timeout 5 tail -f x'")

	// Launcher inside a launcher.
	keep("ssh h 'docker exec c tail -f x'")
	keep("ssh h 'kubectl exec p -- tail -f x'")
	keep("ssh h 'docker run img top'")
	keep(`nix-shell --run "docker exec c tail -f x"`)
	keep("nix-shell --run 'git commit'")
	keep("ssh h 'git commit'")
	keep("docker exec c git commit")
	keep("ssh h 'env X=1 tail -f x'")
	wrap("ssh h 'docker exec c go test ./...'")

	// Remote pipeline: every stage counts, and quotes are honoured.
	keep("ssh h 'ls | tail -f x'")
	keep("ssh h 'ls | tail -fn 5 x'")
	keep(`ssh h 'sbt "~ test"'`)
	wrap("ssh h 'ls | tail -n 5'")
	wrap("ssh h 'go test ./... | tail -20'")

	// Positive controls: the launchers still wrap a finite inner command.
	wrap("ssh h 'go test ./...'")
	wrap("docker exec c go test ./...")
	wrap("kubectl exec p -- go test ./...")
	wrap("docker run --rm golang:1.23 go test ./...")
	cases = append(cases, rewriteTestCase{"wrap env FOO=1 go test", "env FOO=1 go test ./...", "env FOO=1 sctx go test ./...", true})
	wrap("env")
	wrap("nix-shell --run 'go test ./...'")

	// sbt scoped tasks, giter8.
	keep("sbt core/run")
	keep("sbt Compile/run")
	keep("sbt core/console")
	keep("sbt new scala/scala-seed.g8")
	wrap("sbt test")
	wrap("sbt core/test")
	wrap("sbt testOnly new")

	// redis-cli.
	keep("redis-cli --lru-test 100000")
	keep("redis-cli --cluster create a:1 b:2")
	keep("redis-cli --cluster reshard a:1")
	keep("redis-cli --cluster rebalance a:1")
	keep("redis-cli --cluster fix a:1")
	wrap("redis-cli --cluster check a:1")
	wrap("redis-cli --cluster create a:1 --cluster-yes")

	// pacman long operations, apk interactive, unbounded traceroute.
	keep("pacman --sync foo")
	keep("pacman --remove foo")
	keep("pacman --upgrade x.pkg")
	wrap("pacman -Ss foo")
	wrap("pacman --sync --noconfirm foo")
	keep("apk add -i curl")
	keep("apk add --interactive curl")
	wrap("apk add curl")
	keep("traceroute6 host")
	keep("tracepath host")
	wrap("traceroute -q 1 -w 1 host")

	// A glued option VALUE containing `f` is not --follow; real follows still are.
	wrap("journalctl -ufoo.service -n 50")
	wrap("journalctl -tfoo")
	wrap("journalctl -gfail")
	wrap("journalctl -u nginx -n 50")
	keep("journalctl -fu nginx")
	keep("journalctl -fn 100")
	keep("tail -fn 100 x")
	return cases
}

// TestHookKnowsInnerIsBounded: a chain of launchers deeper than maxInnerDepth is
// declined rather than followed indefinitely.
func TestHookKnowsInnerIsBounded(t *testing.T) {
	if !hookKnowsInner([]string{"docker", "exec", "c", "go", "test"}, 0) {
		t.Fatal("one launcher must be vouched for")
	}
	if hookKnowsInner([]string{"go", "test"}, maxInnerDepth) {
		t.Fatal("past maxInnerDepth the answer must be no")
	}
}
