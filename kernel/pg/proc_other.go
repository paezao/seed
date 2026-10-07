//go:build !linux

package pg

import "syscall"

func procAttr(bool) *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
