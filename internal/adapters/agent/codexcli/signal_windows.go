//go:build windows

package codexcli

import (
	"os"
	"os/exec"
	"strconv"
)

func prepareProcessCommand(_ *exec.Cmd) {}

func terminateProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	if err := taskkill(process.Pid, false); err != nil {
		return process.Kill()
	}
	return nil
}

func killProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	if err := taskkill(process.Pid, true); err == nil {
		return nil
	}
	return process.Kill()
}

func taskkill(pid int, force bool) error {
	args := []string{"/PID", strconv.Itoa(pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	return exec.Command("taskkill", args...).Run()
}
