//go:build !windows

package claudecli

import (
	"os"
	"syscall"
)

func terminateProcess(process *os.Process) error {
	return process.Signal(syscall.SIGTERM)
}

func killProcess(process *os.Process) error {
	return process.Kill()
}
