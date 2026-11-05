//go:build unix && !linux

package cli

import "syscall"

// childAttrs runs the app in its own process group, so a Ctrl+C reaches
// only semii dev, which then stops the app once, gracefully.
func childAttrs() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
