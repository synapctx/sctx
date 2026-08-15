package git

import "strings"

func hasAnyArg(args []string, names ...string) bool {
	for _, arg := range args {
		for _, name := range names {
			if arg == name || strings.HasPrefix(arg, name+"=") {
				return true
			}
		}
	}
	return false
}

func hasCustomLineFormat(args []string) bool {
	return hasAnyArg(args, "-z", "--format", "--pretty", "--output", "--template")
}
