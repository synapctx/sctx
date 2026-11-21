package sysinfo

import (
	"fmt"
	"regexp"
	"strings"
)

var mountRE = regexp.MustCompile(`^(\S+) on (.+) type (\S+) \((.*)\)(?: \[(.*)\])?$`)

// pseudoFS are filesystems that are kernel interfaces, not storage.
var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true, "tmpfs": true, "cgroup": true, "cgroup2": true,
	"mqueue": true, "debugfs": true, "tracefs": true, "configfs": true, "fusectl": true, "binfmt_misc": true,
	"hugetlbfs": true, "securityfs": true, "pstore": true, "bpf": true, "autofs": true, "efivarfs": true,
	"nsfs": true, "ramfs": true, "rpc_pipefs": true, "selinuxfs": true, "fuse.gvfsd-fuse": true,
	"fuse.portal": true, "fuse.lxcfs": true, "squashfs": false,
}

// renderMount handles the bare listing forms. `mount <dev> <dir>` performs a
// mount and is not touched.
func renderMount(args, lines []string, aggressive bool) result {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-t" || a == "--types" {
			i++
			continue
		}
		if !strings.HasPrefix(a, "-") {
			return result{} // an operand: this is an action, not a listing
		}
		if a != "-l" && a != "--show-labels" && !strings.HasPrefix(a, "-t") && !strings.HasPrefix(a, "--types=") {
			return result{}
		}
	}
	var out, pseudo []string
	elided, parsed := 0, 0
	for _, l := range lines {
		m := mountRE.FindStringSubmatch(l)
		if m == nil {
			out = append(out, l)
			continue
		}
		parsed++
		src, dir, typ, opts := m[1], m[2], m[3], m[4]
		if aggressive && pseudoFS[typ] {
			pseudo = append(pseudo, typ+":"+dir)
			elided++
			continue
		}
		if aggressive && typ == "overlay" {
			short, n := shortOverlayOpts(opts)
			if n > 0 {
				elided += n
				opts = short
			}
		}
		line := fmt.Sprintf("%s on %s type %s (%s)", src, dir, typ, opts)
		if m[5] != "" {
			line += " [" + m[5] + "]"
		}
		out = append(out, line)
	}
	if parsed == 0 {
		return result{}
	}
	if len(pseudo) > 0 {
		out = append(out, fmt.Sprintf("pseudo/kernel mounts ×%d (type:mountpoint): %s", len(pseudo), strings.Join(pseudo, " ")))
	}
	return result{out: out, elided: elided, ok: true}
}

// shortOverlayOpts replaces the layer paths of an overlay mount with their
// count. They are long, opaque, and different on every container.
func shortOverlayOpts(opts string) (string, int) {
	parts := strings.Split(opts, ",")
	var keep []string
	n := 0
	for _, p := range parts {
		switch {
		case strings.HasPrefix(p, "lowerdir="):
			layers := strings.Count(p, ":") + 1
			keep = append(keep, fmt.Sprintf("lowerdir=…(%d layers)", layers))
			n++
		case strings.HasPrefix(p, "upperdir="):
			keep = append(keep, "upperdir=…")
			n++
		case strings.HasPrefix(p, "workdir="):
			keep = append(keep, "workdir=…")
			n++
		default:
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, ","), n
}
