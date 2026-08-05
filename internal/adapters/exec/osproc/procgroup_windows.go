//go:build windows

package osproc

import (
	"os"
	"syscall"
)

// newProcessGroupAttr returns nil on Windows, and that is deliberate rather than
// unimplemented.
//
// Windows has no process groups in the POSIX sense. The nearest equivalent,
// CREATE_NEW_PROCESS_GROUP, would DETACH the child from the console's Ctrl-C
// handling — so the interrupt a developer presses would stop reaching the very
// process they meant to stop, and sctx would have made the terminal worse. Left
// in the console's group, a wrapped command responds to Ctrl-C exactly as it does
// unwrapped, which is the property that matters.
func newProcessGroupAttr() *syscall.SysProcAttr { return nil }

// forwardSignal terminates the child.
//
// Windows cannot deliver a POSIX signal to another process, so there is nothing
// to relay: SIGTERM is never generated, and a console Ctrl-C already reaches the
// child directly because it shares the console group. This exists for the case
// where sctx itself is signalled programmatically, where terminating the child is
// the only available equivalent to passing the signal on.
func forwardSignal(p *os.Process, sig os.Signal) {
	if p == nil {
		return
	}
	_ = p.Kill()
}
