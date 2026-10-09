//go:build !windows

package runner

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// isolate starts cmd in its own process group. Cancelling sends SIGTERM to the
// whole group, so children of the shell (docker compose, node, ...) stop too
// instead of being orphaned, and SIGKILL to whatever is left after killGrace.
func isolate(cmd *exec.Cmd) {
	grace := killGrace
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		pgid := -cmd.Process.Pid
		time.AfterFunc(grace, func() { _ = syscall.Kill(pgid, syscall.SIGKILL) })
		if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone // the group already exited
			}
			return err
		}
		return nil
	}
	cmd.WaitDelay = grace
}
