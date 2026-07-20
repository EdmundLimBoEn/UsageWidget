//go:build windows

package server

import "os/exec"

// exec.CommandContext terminates the direct child on Windows. Avoid Unix-only
// process-group fields so the main daemon can be built and run natively.
func configureCommandCancellation(cmd *exec.Cmd) {}
