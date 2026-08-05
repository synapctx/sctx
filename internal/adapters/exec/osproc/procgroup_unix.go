//go:build !windows

package osproc

import (
	"os"
	"syscall"
)

// newProcessGroupAttr puts the child in its OWN process group so a signal can be
// delivered to the whole tree it spawns, not just the immediate child. A wrapped
// `go test` starts compilers and test binaries; interrupting only the parent
// leaves those running.
func newProcessGroupAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// forwardSignal relays a signal to the child's process group.
func forwardSignal(p *os.Process, sig os.Signal) {
	if p == nil {
		return
	}
	if s, ok := sig.(syscall.Signal); ok {
		// A negative pid targets the process group.
		_ = syscall.Kill(-p.Pid, s)
	}
}
