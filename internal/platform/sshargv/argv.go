// Package sshargv extracts finite remote commands from OpenSSH argv.
package sshargv

import (
	"strings"

	"github.com/synapctx/sctx/internal/platform/nestedcmd"
)

// Options that consume a value. Keeping this grammar shared prevents a hook
// decision and formatter decision from disagreeing about which token is host.
var valueOptions = map[byte]bool{
	'B': true, 'b': true, 'c': true, 'D': true, 'E': true, 'e': true, 'F': true,
	'I': true, 'i': true, 'J': true, 'L': true, 'l': true, 'm': true, 'O': true,
	'o': true, 'p': true, 'Q': true, 'R': true, 'S': true, 'W': true, 'w': true,
}

// RemoteCommand returns the single simple remote command. Interactive shells,
// tunnels, forced TTYs, malformed options, compound commands and recursive ssh
// invocations decline.
func RemoteCommand(argv []string) ([]string, bool) {
	if len(argv) < 2 {
		return nil, false
	}
	i := 1
	for i < len(argv) {
		a := argv[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			break
		}
		if a == "--" {
			i++
			break
		}
		if strings.HasPrefix(a, "--") {
			return nil, false
		}
		for k := 1; k < len(a); k++ {
			option := a[k]
			if option == 'N' || option == 't' {
				return nil, false
			}
			if valueOptions[option] {
				if k == len(a)-1 {
					i++
					if i >= len(argv) {
						return nil, false
					}
				}
				break
			}
		}
		i++
	}
	if i >= len(argv) {
		return nil, false
	}
	return nestedcmd.Remote(argv[i+1:])
}
