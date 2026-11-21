package hook

import (
	"strings"

	"github.com/synapctx/sctx/internal/platform/nestedcmd"
)

// linuxStreams reports whether a Linux system-administration command never
// reaches EOF or blocks on a prompt. It is the arm of coverageStreams for the
// package managers, monitors and capture tools. journalctl and tail are decided
// in streamsForever itself, which also reads follow flags inside a cluster.
//
// Package managers are refused only when a transaction would stop at its
// "Do you want to continue? [Y/n]" prompt; queries and refreshes never ask and
// keep their coverage.
func linuxStreams(program string, args []string) bool {
	switch program {
	case "apt", "apt-get", "dnf", "yum":
		return pkgPrompts(program, args)
	case "pacman":
		return pacmanPrompts(args)
	case "apk":
		// `-i` / `--interactive` asks before every operation.
		return anyArg(args, "--interactive") || nestedcmd.ShortCluster(args, "i", "tXpC")
	case "traceroute", "traceroute6", "tracepath":
		// A silent hop costs probes x wait seconds, 30 hops deep: minutes on macOS,
		// and an agent timeout would show nothing. Only a bounded run is wrapped:
		// a hop limit (-m N), or both a probe count and a wait (-q and -w).
		const values = "gimpstzNf" // value-taking options other than m, q, w
		return !(nestedcmd.ShortCluster(args, "m", values+"qw") || anyPrefix(args, "--max-hops", "--max-ttl") ||
			(nestedcmd.ShortCluster(args, "q", values+"mw") && nestedcmd.ShortCluster(args, "w", values+"mq")))
	case "top":
		// Interactive unless BOTH batch (-b) and an iteration count (-n N) are given.
		return !(shortCluster(args, "b") && (shortCluster(args, "n") || anyPrefix(args, "--iterations")))
	case "htop", "btop", "bpytop", "glances", "atop", "iotop", "iftop", "nload", "nethogs", "bmon", "slabtop", "watch",
		"tcpdump", "tshark", "ngrep":
		return true
	case "mtr":
		// Interactive curses UI unless a report/CSV/JSON mode is requested.
		return !(anyArg(args, "-r", "--report", "-w", "--report-wide", "--json", "--csv", "--xml", "--raw") || shortCluster(args, "r"))
	case "dmesg":
		return anyArg(args, "-w", "--follow", "-W", "--follow-new") || shortCluster(args, "wW")
	case "vmstat", "iostat", "mpstat", "pidstat", "sar":
		// `vmstat 1` repeats forever; `vmstat 1 3` and bare `vmstat` finish.
		return digitOperands(args) == 1
	case "ss":
		return anyArg(args, "--events", "--continuous") || shortCluster(args, "Ec")
	case "netstat":
		return anyArg(args, "--continuous") || shortCluster(args, "c")
	case "ip":
		// `ip -n ns monitor`, `ip route monitor`, `ip -s monitor`: any position.
		return hasOperand(args, "monitor")
	}
	return false
}

// digitOperands counts all-digit arguments (an interval and a count).
func digitOperands(args []string) int {
	n := 0
	for _, a := range args {
		if isDigits(a) {
			n++
		}
	}
	return n
}

// promptVerbs are the transactions that ask "Do you want to continue? [Y/n]".
var promptVerbs = map[string]map[string]bool{
	"apt":     {"install": true, "remove": true, "purge": true, "upgrade": true, "dist-upgrade": true, "full-upgrade": true, "autoremove": true, "autopurge": true, "reinstall": true},
	"apt-get": {"install": true, "remove": true, "purge": true, "upgrade": true, "dist-upgrade": true, "full-upgrade": true, "autoremove": true, "autopurge": true, "reinstall": true},
	"dnf":     {"install": true, "remove": true, "erase": true, "upgrade": true, "update": true, "autoremove": true, "reinstall": true, "downgrade": true, "distro-sync": true, "swap": true},
	"yum":     {"install": true, "remove": true, "erase": true, "upgrade": true, "update": true, "autoremove": true, "reinstall": true, "downgrade": true, "distro-sync": true, "swap": true},
}

// pkgManagers are the package managers whose verb may follow option flags
// (`apt-get -y install x`, `dnf -y upgrade`); matchAt looks for the verb with
// pkgVerb rather than at argv[1].
var pkgManagers = map[string]bool{"apt": true, "apt-get": true, "dnf": true, "yum": true, "apk": true}

// pkgVerb returns the first operand after the options, skipping the values of
// the options that take one (`-o APT::Get::Assume-Yes=true`, `-c file`). An
// option it does not know that takes a value shifts the result to that value,
// which is not a verb, so the invocation is left unwrapped.
func pkgVerb(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-o", "-c", "-t", "-a", "-p", "-X", "--enablerepo", "--disablerepo", "--repo", "--repository", "-x", "--exclude", "--setopt", "--installroot", "--releasever":
			i++ // consumes a value
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}

// pkgPrompts reports a transaction run without any consent or dry-run flag: it
// would sit at a prompt.
func pkgPrompts(program string, args []string) bool {
	verb := pkgVerb(args)
	if !promptVerbs[program][verb] {
		return false
	}
	// Consent: -y / --yes / --assume-yes / --assumeyes / -o APT::Get::Assume-Yes=true.
	// No prompt either: simulate / dry-run / --assume-no / --downloadonly.
	if anyArg(args, "--yes", "--assume-yes", "--assumeyes", "--assume-no", "--assumeno", "-s", "--simulate", "--just-print", "--dry-run", "--no-act", "--recon", "--downloadonly", "--download-only") ||
		flagCluster(args, "y") || anyPrefix(args, "--setopt=assumeyes") {
		return false
	}
	for _, a := range args {
		if strings.Contains(a, "Assume-Yes") || strings.Contains(a, "assume-yes") {
			return false
		}
	}
	return true
}

// pacmanPrompts: -S (install/upgrade/clean), -R and -U ask for confirmation
// unless --noconfirm or a print mode; queries and -Sy (refresh only) do not.
func pacmanPrompts(args []string) bool {
	if anyArg(args, "--noconfirm", "-p", "--print", "--print-format") {
		return false
	}
	operands := 0
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			operands++
		}
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			// The long operations: --remove and --upgrade always ask; --sync asks
			// except for its query modifiers, where only --sysupgrade/--clean or a
			// package operand make it a transaction.
			switch a {
			case "--remove", "--upgrade":
				return true
			case "--sync":
				if anyArg(args, "--search", "--info", "--list", "--groups") {
					return false
				}
				return anyArg(args, "--sysupgrade", "--clean") || operands > 0
			}
			continue
		}
		if len(a) < 2 || a[0] != '-' {
			continue
		}
		c := a[2:]
		switch a[1] {
		case 'R', 'U':
			return true
		case 'S':
			if strings.ContainsAny(c, "silg") {
				return false
			}
			return strings.ContainsAny(c, "uc") || operands > 0
		}
	}
	return false
}
