package main

import (
	"context"
	"fmt"
	"time"
)

const (
	defaultProcessStopTimeout = 2 * time.Second
	processStopPollInterval   = 100 * time.Millisecond
)

type stopProcessResult string

const (
	stopProcessTerminated stopProcessResult = "terminated"
	stopProcessKilled     stopProcessResult = "killed"
)

func stopProcessEntry(ctx context.Context, pid int, timeout time.Duration) (stopProcessResult, bool, error) {
	if pid <= 0 {
		return "", false, fmt.Errorf("process pid is missing")
	}
	if timeout <= 0 {
		timeout = defaultProcessStopTimeout
	}
	if err := signalProcessTerminate(pid); err != nil {
		return "", true, err
	}
	if waitProcessExit(ctx, pid, timeout) {
		return stopProcessTerminated, false, nil
	}
	if err := signalProcessKill(pid); err != nil {
		return "", true, err
	}
	if waitProcessExit(ctx, pid, timeout) {
		return stopProcessKilled, false, nil
	}
	return "", true, fmt.Errorf("process %d did not exit after SIGKILL", pid)
}

func waitProcessExit(ctx context.Context, pid int, timeout time.Duration) bool {
	if !processAlive(pid) {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(processStopPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return !processAlive(pid)
		case <-ticker.C:
			if !processAlive(pid) {
				return true
			}
		}
	}
}
