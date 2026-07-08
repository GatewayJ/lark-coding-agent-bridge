//go:build !windows

package codexcli

import (
	"os"
	"os/exec"
	"syscall"
)

func prepareProcessCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcess(process *os.Process) error {
	return signalProcessGroup(process, syscall.SIGTERM)
}

func killProcess(process *os.Process) error {
	return signalProcessGroup(process, syscall.SIGKILL)
}

func signalProcessGroup(process *os.Process, signal syscall.Signal) error {
	if process == nil {
		return nil
	}
	if err := syscall.Kill(-process.Pid, signal); err != nil {
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}
