//go:build !windows

package gitcache

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup puts cmd in its own process group and arranges for
// context cancellation to kill that whole group, not just the direct git
// child. This matters because `git clone`/`fetch`/`push` over HTTP(S)
// delegate the actual network I/O to a `git-remote-http(s)` helper
// subprocess: without this, killing only the top-level `git` process on
// timeout can leave that helper running and still holding the TCP
// connection open, both leaking a process and (in tests using a local fake
// server) keeping the connection "active" indefinitely.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
