package makefmt

import (
	"context"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

func TestDescriptor(t *testing.T) {
	f := New()
	got := f.Descriptor()
	if got.Command != "make" {
		t.Errorf("Command = %q, want %q", got.Command, "make")
	}
}

const dirNoiseFixture = `make[1]: Entering directory '/repo/pkg/a'
make[2]: Entering directory '/repo/pkg/a/sub'
make[2]: Leaving directory '/repo/pkg/a/sub'
make[1]: Leaving directory '/repo/pkg/a'
echo building
echo building
echo building
go build ./...
`

func TestAggressiveCollapsesDirNoiseAndRepeatedEchoes(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"make", "build"},
		Stdout: strings.NewReader(dirNoiseFixture),
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "dir: /repo/pkg/a ×4") {
		t.Errorf("missing dir collapse marker: %q", body)
	}
	if !strings.Contains(body, "echo building ×3") {
		t.Errorf("missing repeated echo collapse: %q", body)
	}
	if !strings.Contains(body, "go build ./...") {
		t.Errorf("missing trailing command line: %q", body)
	}
}

func TestAggressiveKeepsErrorSignalVerbatim(t *testing.T) {
	f := New()
	stdout := "make[1]: Entering directory '/repo'\n" +
		"make[1]: Leaving directory '/repo'\n" +
		"cc -c main.c\n" +
		"main.c:5:1: error: undefined reference to 'foo'\n" +
		"make: *** [Makefile:10: main.o] Error 1\n"
	in := format.Input{
		Argv:     []string{"make"},
		Stdout:   strings.NewReader(stdout),
		ExitCode: 2,
	}
	out, err := f.Aggressive(context.Background(), in)
	if err != nil {
		t.Fatalf("Aggressive() error = %v", err)
	}
	body := string(out.Body)
	if !strings.Contains(body, "error: undefined reference to 'foo'") {
		t.Errorf("error line dropped: %q", body)
	}
	if !strings.Contains(body, "Error 1") {
		t.Errorf("error summary dropped: %q", body)
	}
	if !strings.Contains(body, "dir:") {
		t.Errorf("directory noise not collapsed on non-zero exit: %q", body)
	}
}

func TestZeroTransformInapplicable(t *testing.T) {
	f := New()
	in := format.Input{
		Argv:   []string{"make", "build"},
		Stdout: strings.NewReader("go build ./...\nlinked binary bin/app\n"),
	}
	if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
		t.Errorf("err = %v, want ErrTierInapplicable", err)
	}
}
