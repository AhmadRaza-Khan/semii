package cli

import "syscall"

// childAttrs runs the app in its own process group, so a Ctrl+C reaches
// only semii dev (which then stops the app once, gracefully), and makes the
// app receive SIGTERM if semii dev dies, so it is never orphaned.
func childAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}
