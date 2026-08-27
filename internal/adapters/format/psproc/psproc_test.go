package psproc

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/synapctx/sctx/internal/domain/format"
)

// psAuxFixture is real `ps aux` output (macOS BSD-style) captured from a
// single invocation so header/data column offsets are guaranteed consistent.
// It includes long command paths and a duplicated -zsh row so both
// truncation and dupe-collapsing have something to do.
const psAuxFixture = `USER               PID  %CPU %MEM      VSZ    RSS   TT  STAT STARTED      TIME COMMAND
root                 1  44.0  0.0 435245744  23856   ??  Us   Thu10p.m.  68:52.12 /sbin/launchd
sebastiangogoasa  1316   2.6  0.4 436063056 251888   ??  S    Thu10p.m. 548:39.42 /Applications/iTerm.app/Contents/MacOS/iTerm2
_coreaudiod        633   1.3  0.1 435395024  66016   ??  Ss   Thu10p.m.  66:57.85 /usr/sbin/coreaudiod
sebastiangogoasa  1205   1.1  0.1 435675104  99728   ??  S    Thu10p.m.  23:11.88 /System/Library/CoreServices/ControlCenter.app/Contents/MacOS/ControlCenter
root               589   0.7  0.0 435363184  22704   ??  Ss   Thu10p.m.   7:46.93 /usr/libexec/opendirectoryd
root               706   0.4  0.0 435358160  10752   ??  Ss   Thu10p.m.   2:29.82 /usr/libexec/audioanalyticsd
sebastiangogoasa 75311   0.4  0.5 1951658560 310480   ??  S    Fri10p.m.  19:01.28 /Applications/Visual Studio Code.app/Contents/MacOS/Code .
root               742   0.3  0.0 435392752  32400   ??  Ss   Thu10p.m.  11:35.36 /usr/libexec/searchpartyd
root               614   0.3  0.0 435321952   5168   ??  Ss   Thu10p.m.   4:33.81 /usr/sbin/notifyd
_locationd         596   0.2  0.0 435393120  32880   ??  Ss   Thu10p.m.  19:28.84 /usr/libexec/locationd
root               616   0.1  0.0 435357824  15184   ??  Ss   Thu10p.m.  22:25.91 /usr/libexec/corebrightnessd --launchd
sebastiangogoasa  1330   0.1  0.2 435822256 164016   ??  S    Thu10p.m.   5:48.85 /System/Library/CoreServices/Finder.app/Contents/MacOS/Finder
root               548   0.1  0.1 435585120  60608   ??  Ss   Thu10p.m.  16:45.33 /usr/libexec/logd
sebastiangogoasa 56258   0.1  0.0 435311136   4896 s003  S+   Fri11a.m.   0:30.39 -zsh
sebastiangogoasa 56258   0.1  0.0 435311136   4896 s003  S+   Fri11a.m.   0:30.39 -zsh
`

const psPlainFixture = `  PID TTY           TIME CMD
 1448 ttys000    0:04.98 -zsh
 1486 ttys000    0:00.01 -zsh
20991 ttys001    0:00.02 vim notes.txt
`

func TestAggressive(t *testing.T) {
	f := New()

	t.Run("aux fixture: header summary, collapse dupes, truncate command", func(t *testing.T) {
		in := format.Input{Argv: []string{"ps", "aux"}, Stdout: strings.NewReader(psAuxFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.HasPrefix(body, "15 processes\n") {
			t.Errorf("body missing process count header, got: %q", body[:min(40, len(body))])
		}
		if !strings.Contains(body, "-zsh ×2") {
			t.Errorf("body missing collapsed duplicate marker, got: %q", body)
		}
		if !strings.Contains(body, "…") {
			t.Errorf("body missing truncation marker for a long command, got: %q", body)
		}
		if strings.Contains(body, "ControlCenter.app/Contents/MacOS/ControlCenter\n") {
			t.Errorf("long command was not truncated: %q", body)
		}
		if len(out.Body) >= len(psAuxFixture) {
			t.Errorf("body not smaller than raw: %d >= %d", len(out.Body), len(psAuxFixture))
		}
		lines := strings.Split(body, "\n")
		if len(lines) < 2 || !strings.HasPrefix(lines[1], "root 1 44.0") {
			t.Errorf("expected highest %%CPU row (launchd) first, got: %q", lines[1])
		}
	})

	t.Run("plain ps fixture renders pid and command", func(t *testing.T) {
		in := format.Input{Argv: []string{"ps"}, Stdout: strings.NewReader(psPlainFixture)}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.HasPrefix(body, "3 processes\n") {
			t.Errorf("body missing process count, got: %q", body)
		}
		if !strings.Contains(body, "-zsh ×2") {
			t.Errorf("body missing collapsed -zsh rows, got: %q", body)
		}
		if !strings.Contains(body, "20991") {
			t.Errorf("body missing distinct pid row, got: %q", body)
		}
	})

	t.Run("caps at 40 rows with explicit marker", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("USER               PID  %CPU %MEM      VSZ    RSS   TT  STAT STARTED      TIME COMMAND\n")
		for i := range 50 {
			fmt.Fprintf(&b, "user%02d          %5d  %4.1f  0.1 400000000  10000   ??  S    Fri10p.m.   0:00.00 /usr/bin/worker-%d --unique-arg\n", i, 10100+i, float64(50-i)/10, i)
		}
		in := format.Input{Argv: []string{"ps", "aux"}, Stdout: strings.NewReader(b.String())}
		out, err := f.Aggressive(context.Background(), in)
		if err != nil {
			t.Fatalf("Aggressive() error = %v", err)
		}
		body := string(out.Body)
		if !strings.HasPrefix(body, "50 processes\n") {
			t.Errorf("body missing count, got: %q", body[:min(30, len(body))])
		}
		if !strings.Contains(body, "…+10 more processes") {
			t.Errorf("body missing cap marker, got tail: %q", body[max(0, len(body)-80):])
		}
	})

	t.Run("unparseable header degrades", func(t *testing.T) {
		in := format.Input{Argv: []string{"ps", "-o", "weird"}, Stdout: strings.NewReader("WEIRDCOL\nfoo\n")}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})

	t.Run("non-zero exit degrades", func(t *testing.T) {
		in := format.Input{
			Argv:     []string{"ps", "-p", "999999"},
			Stdout:   strings.NewReader(""),
			Stderr:   strings.NewReader("ps: 999999: No such process\n"),
			ExitCode: 1,
		}
		if _, err := f.Aggressive(context.Background(), in); err != format.ErrTierInapplicable {
			t.Errorf("err = %v, want ErrTierInapplicable", err)
		}
	})
}
