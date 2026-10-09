//go:build windows

package runner

import "os/exec"

// isolate bounds the wait for output pipes held open by child processes;
// Windows has no process groups to signal.
func isolate(cmd *exec.Cmd) {
	cmd.WaitDelay = killGrace
}
