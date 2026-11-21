package fs

import (
	"context"
	"fmt"
	"strings"

	"github.com/synapctx/sctx/internal/domain/format"
)

// dfFormatter renders `df` tables: single-space separated, inode columns
// dropped (macOS prints them by default), and pseudo/system mounts folded
// into one marker line that still names every mount point.
type dfFormatter struct{}

func (f *dfFormatter) Descriptor() format.Match {
	return format.Match{Command: "df"}
}

// dfPseudoFS are filesystem names that are never what a disk-space question
// is about.
var dfPseudoFS = map[string]bool{
	"devfs": true, "tmpfs": true, "devtmpfs": true, "overlay": true, "shm": true,
	"squashfs": true, "efivarfs": true, "none": true, "udev": true, "cgroup": true,
	"proc": true, "sysfs": true, "map": true, "autofs": true, "fdescfs": true,
}

func dfPseudoMount(mount string) bool {
	for _, p := range []string{"/dev", "/proc", "/sys", "/run", "/System/Volumes/", "/private/var/vm"} {
		if mount == p || strings.HasPrefix(mount, p+"/") || strings.HasSuffix(p, "/") && strings.HasPrefix(mount, p) {
			return mount != "/System/Volumes/Data"
		}
	}
	return false
}

func (f *dfFormatter) Aggressive(_ context.Context, in format.Input) (format.Rendered, error) {
	raw, err := readStdout(in)
	if err != nil {
		return format.Rendered{}, err
	}
	lines := splitLines(raw)
	if len(lines) < 4 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	header := lines[0]
	mountOff := strings.Index(header, "Mounted on")
	if mountOff < 0 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	cols := strings.Fields(header[:mountOff])
	if len(cols) < 4 {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	keepInodes := false
	for _, a := range in.Argv[min(1, len(in.Argv)):] {
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a[1:], "iT") || a == "--inodes" || a == "--print-type" {
			keepInodes = true
		}
	}
	keep := make([]bool, len(cols))
	for i, c := range cols {
		switch strings.ToLower(c) {
		case "iused", "ifree", "%iused":
			keep[i] = keepInodes
		default:
			keep[i] = true
		}
	}
	dropped := 0
	for _, k := range keep {
		if !k {
			dropped++
		}
	}

	var out []string
	var folded []string
	rows := 0
	emit := func(fields []string, mount string) string {
		kept := make([]string, 0, len(fields)+1)
		for i, v := range fields {
			if keep[i] {
				kept = append(kept, v)
			}
		}
		return strings.Join(kept, " ") + " " + mount
	}
	head := make([]string, 0, len(cols)+1)
	for i, c := range cols {
		if keep[i] {
			head = append(head, c)
		}
	}
	out = append(out, strings.Join(head, " ")+" Mounted-on")

	body := lines[1:]
	for i := 0; i < len(body); i++ {
		l := body[i]
		if strings.TrimSpace(l) == "" {
			continue
		}
		var lead []string
		if len(l) <= mountOff {
			// busybox df wraps a long filesystem/type name onto its own line
			// and continues the numeric columns, aligned, on the next one.
			if i+1 >= len(body) || len(body[i+1]) <= mountOff {
				return format.Rendered{}, format.ErrTierInapplicable
			}
			lead = strings.Fields(l)
			i++
			l = body[i]
		}
		mount := strings.TrimSpace(l[mountOff:])
		fields := append(lead, strings.Fields(l[:mountOff])...)
		// The leading filesystem name may contain spaces ("map auto_home"):
		// the numeric columns are always the last len(cols)-1 fields.
		n := len(cols) - 1
		if len(fields) < n+1 {
			return format.Rendered{}, format.ErrTierInapplicable
		}
		fsName := strings.Join(fields[:len(fields)-n], " ")
		data := append([]string{fsName}, fields[len(fields)-n:]...)
		rows++
		if dfPseudoFS[strings.Fields(fsName)[0]] || dfPseudoMount(mount) {
			folded = append(folded, mount)
			continue
		}
		out = append(out, emit(data, mount))
	}
	if len(folded) > 0 {
		out = append(out, fmt.Sprintf("…+%d system/pseudo mounts folded (sizes omitted): %s", len(folded), strings.Join(folded, ", ")))
	}
	if dropped > 0 {
		out = append(out, "(inode columns omitted; df -i shows them)")
	}
	rendered := strings.Join(out, "\n")
	if !shrunk(raw, rendered) {
		return format.Rendered{}, format.ErrTierInapplicable
	}
	return format.Rendered{Body: []byte(rendered), Note: fmt.Sprintf("%d filesystems", rows), Elided: true}, nil
}

func (f *dfFormatter) Relaxed(context.Context, format.Input) (format.Rendered, error) {
	return format.Rendered{}, format.ErrTierInapplicable
}
