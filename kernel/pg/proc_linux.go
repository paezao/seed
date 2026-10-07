//go:build linux

package pg

import "syscall"

// procAttr puts postgres in its own process group. With stopWithParent, the
// kernel also stops it when the parent dies (tests use this; it is tied to
// the starting OS thread, so long-running kernels rely on their container's
// init instead).
func procAttr(stopWithParent bool) *syscall.SysProcAttr {
	a := &syscall.SysProcAttr{Setpgid: true}
	if stopWithParent {
		a.Pdeathsig = syscall.SIGINT
	}
	return a
}
