package gotest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

const runCompileErrStderr = "# example.com/mod\n" +
	"./main.go:12:3: undefined: fmt.Printfx\n" +
	"./main.go:15:9: cannot use x (variable of type int) as string value in argument to greet\n"

const runNormalStdout = "hello from the program\nprocessing item 1\nprocessing item 2\ndone\n"

func TestAggressive_Run(t *testing.T) {
	f := New()

	t.Run("compile error keeps diagnostics verbatim", func(t *testing.T) {
		in := newInput([]string{"go", "run", "main.go"}, "go run", "", runCompileErrStderr, 1, 0)

		rendered, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(rendered.Body)
		if !strings.Contains(body, "./main.go:12:3: undefined: fmt.Printfx") {
			t.Errorf("Body = %q, want the compile diagnostic preserved", body)
		}
		if !strings.Contains(body, "./main.go:15:9") {
			t.Errorf("Body = %q, want the second diagnostic preserved", body)
		}
		if !rendered.FoldStderr {
			t.Error("FoldStderr = false, want true for a compile failure")
		}
	})

	t.Run("normal program output is left to a later tier", func(t *testing.T) {
		in := newInput([]string{"go", "run", "main.go"}, "go run", runNormalStdout, "", 0, 0)

		_, err := f.Aggressive(context.Background(), in)
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable for normal program output", err)
		}
	})

	t.Run("runtime panic (non-compile failure) is left to a later tier", func(t *testing.T) {
		panicStderr := "panic: runtime error: index out of range [3] with length 3\n\ngoroutine 1 [running]:\nmain.main()\n\t/src/main.go:9 +0x1b\nexit status 2\n"
		in := newInput([]string{"go", "run", "main.go"}, "go run", "", panicStderr, 2, 0)

		_, err := f.Aggressive(context.Background(), in)
		if !errors.Is(err, format.ErrTierInapplicable) {
			t.Fatalf("Aggressive() error = %v, want ErrTierInapplicable for a runtime panic", err)
		}
	})
}
