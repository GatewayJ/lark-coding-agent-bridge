package runexecutor

import "fmt"

type RunRejectedCode string

const (
	RunRejectedPoolFull            RunRejectedCode = "pool-full"
	RunRejectedPolicyExpired       RunRejectedCode = "policy-expired"
	RunRejectedReconnectInProgress RunRejectedCode = "reconnect-in-progress"
	RunRejectedAlreadyActive       RunRejectedCode = "run-already-active"
)

type RunRejected struct {
	Code    RunRejectedCode
	Message string
}

func (e *RunRejected) Error() string {
	return e.Message
}

func reject(code RunRejectedCode, message string) error {
	return &RunRejected{Code: code, Message: message}
}

type SpawnFailedCode string

const (
	SpawnFailedAgentSpawn   SpawnFailedCode = "agent-spawn-failed"
	SpawnFailedAgentPrepare SpawnFailedCode = "agent-prepare-failed"
)

type SpawnFailed struct {
	Code    SpawnFailedCode
	Message string
	Cause   error
}

func (e *SpawnFailed) Error() string {
	if e.Cause == nil {
		return e.Message
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Cause)
}

func (e *SpawnFailed) Unwrap() error {
	return e.Cause
}

func spawnFailed(code SpawnFailedCode, message string, cause error) error {
	return &SpawnFailed{Code: code, Message: message, Cause: cause}
}
