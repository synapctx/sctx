package gofmt

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// diffFixture is `gofmt -d` output captured from the real darwin/arm64
// gofmt binary: two `:=` spacing fixes bracketing a 5-line unchanged run
// (gofmt's own diff merges the two 3-line-context hunks into one at this
// gap, verified empirically — a gap of 6 lines instead stays split), long
// enough for filediff's context-collapsing to actually save bytes.
const diffFixture = `diff j.go.orig j.go
--- j.go.orig
+++ j.go
@@ -3,12 +3,12 @@
 import "fmt"

 func main() {
-	x:=1
+	x := 1
 	variable_number_1 := 1
 	variable_number_2 := 2
 	variable_number_3 := 3
 	variable_number_4 := 4
 	variable_number_5 := 5
-	y:=2
+	y := 2
 	fmt.Println(x, y)
 }
`

func input(argv []string, stdout string, exit int) format.Input {
	return format.Input{Argv: argv, Stdout: strings.NewReader(stdout), Stderr: bytes.NewReader(nil), ExitCode: exit}
}

func TestAggressiveDelegatesDashDToFilediff(t *testing.T) {
	f := New()
	out, err := f.Aggressive(context.Background(), input([]string{"gofmt", "-d", "a.go"}, diffFixture, 1))
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	if len(out.Body) == 0 || len(out.Body) >= len(diffFixture) {
		t.Fatalf("Aggressive() body not smaller: got %d bytes from %d raw", len(out.Body), len(diffFixture))
	}
	if !strings.Contains(string(out.Body), "@@") {
		t.Fatalf("Aggressive() body lost the hunk header: %q", out.Body)
	}
}

func TestListAndWriteDecline(t *testing.T) {
	f := New()
	tests := []format.Input{
		input([]string{"gofmt", "-l", "a.go"}, "a.go\n", 0),
		input([]string{"gofmt", "-w", "a.go"}, "", 0),
		input([]string{"gofmt", "a.go"}, "package main\n", 0),
	}
	for _, in := range tests {
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("Aggressive(%v) error = %v, want ErrTierInapplicable", in.Argv, err)
		}
		if _, err := f.Relaxed(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("Relaxed(%v) error = %v, want ErrTierInapplicable", in.Argv, err)
		}
	}
}
