//go:build !unix

package cli

import "syscall"

// childAttrs needs no special attributes on this platform.
func childAttrs() *syscall.SysProcAttr { return nil }
