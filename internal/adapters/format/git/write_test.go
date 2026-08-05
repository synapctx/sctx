package git

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const pushStderr = `Enumerating objects: 9, done.
Counting objects: 100% (9/9), done.
Delta compression using up to 8 threads
Compressing objects: 100% (6/6), done.
Writing objects: 100% (7/7), 682 bytes | 682.00 KiB/s, done.
Total 7 (delta 1), reused 0 (delta 0), pack-reused 0
remote: Resolving deltas: 100% (1/1), completed with 1 local object.
To github.com:example/repo.git
   d018a40..850a312  main -> main
`

const pullFastForward = `remote: Enumerating objects: 5, done.
remote: Counting objects: 100% (5/5), done.
remote: Compressing objects: 100% (3/3), done.
remote: Total 3 (delta 0), reused 0 (delta 0), pack-reused 0
Unpacking objects: 100% (3/3), 897 bytes | 897.00 KiB/s, done.
From github.com:example/repo
   d018a40..850a312  main       -> origin/main
Updating d018a40..850a312
Fast-forward
 e.txt | 5 +++++
 1 file changed, 5 insertions(+)
`

const commitOutput = `[main 850a312] add e.txt
 1 file changed, 5 insertions(+)
 create mode 100644 e.txt
`

const pushRejected = `To github.com:example/repo.git
 ! [rejected]        main -> main (fetch first)
error: failed to push some refs to 'github.com:example/repo.git'
hint: Updates were rejected because the remote contains work that you do
hint: not have locally. This is usually caused by another repository pushing
hint: to the same ref. You may want to first integrate the remote changes
hint: (e.g., 'git pull ...') before pushing again.
`

func TestAggressiveWrite(t *testing.T) {
	f := New()

	t.Run("push drops transfer progress, keeps ref update, folds stderr", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"git", "push"},
			Stdout: strings.NewReader(""),
			Stderr: strings.NewReader(pushStderr),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !out.FoldStderr {
			t.Error("FoldStderr = false, want true")
		}
		body := string(out.Body)
		if !strings.Contains(body, "d018a40..850a312  main -> main") {
			t.Errorf("body missing ref update: %q", body)
		}
		for _, unwanted := range []string{"Counting objects", "Compressing objects", "Writing objects", "Enumerating objects", "Resolving deltas"} {
			if strings.Contains(body, unwanted) {
				t.Errorf("body still contains progress noise %q: %q", unwanted, body)
			}
		}
		if len(out.Body) >= len(pushStderr) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(pushStderr))
		}
	})

	t.Run("pull keeps fast-forward summary, drops progress", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"git", "pull"},
			Stdout: strings.NewReader(pullFastForward),
			Stderr: strings.NewReader(""),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		for _, want := range []string{"Fast-forward", "1 file changed, 5 insertions(+)"} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		if strings.Contains(body, "Unpacking objects") {
			t.Errorf("body still contains progress noise: %q", body)
		}
	})

	t.Run("commit keeps summary line", func(t *testing.T) {
		in := format.Input{
			Argv:   []string{"git", "commit"},
			Stdout: strings.NewReader(commitOutput),
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !strings.Contains(string(out.Body), "[main 850a312] add e.txt") {
			t.Errorf("body missing commit summary: %q", out.Body)
		}
	})

	t.Run("rejected push keeps error content and drops hints", func(t *testing.T) {
		in := format.Input{
			Argv:     []string{"git", "push"},
			Stdout:   strings.NewReader(""),
			Stderr:   strings.NewReader(pushRejected),
			ExitCode: 1,
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		for _, want := range []string{"rejected", "error: failed to push"} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q, got: %q", want, body)
			}
		}
		if strings.Contains(body, "hint:") {
			t.Errorf("body still contains hint lines: %q", body)
		}
		if len(out.Body) == 0 {
			t.Error("body is empty for non-empty raw input")
		}
	})

	t.Run("failed push with only progress/hint noise degrades instead of reporting done", func(t *testing.T) {
		// All lines here are progress/hint noise per filterWriteNoise, with
		// no fatal:/error:/rejected/conflict keyword for isErrorLine to
		// catch — the kind of gap that must never be reported as "done".
		noisy := "Counting objects: 100% (9/9), done.\nhint: some advice\n"
		in := format.Input{
			Argv:     []string{"git", "push"},
			Stdout:   strings.NewReader(""),
			Stderr:   strings.NewReader(noisy),
			ExitCode: 1,
		}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable (must degrade, not report done on failure)", err)
		}
	})

	t.Run("successful op with only progress/hint noise still reports done", func(t *testing.T) {
		noisy := "Counting objects: 100% (9/9), done.\nhint: some advice\n"
		in := format.Input{
			Argv:     []string{"git", "push"},
			Stdout:   strings.NewReader(""),
			Stderr:   strings.NewReader(noisy),
			ExitCode: 0,
		}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		if !strings.Contains(string(out.Body), "push: done") {
			t.Errorf("body = %q, want it to contain %q", out.Body, "push: done")
		}
	})
}
