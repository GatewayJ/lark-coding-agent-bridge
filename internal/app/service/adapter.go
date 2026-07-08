package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

type Result struct {
	OK     bool
	Stdout string
	Stderr string
}

type ParsedStatus struct {
	PID      string
	LastExit string
}

type Adapter interface {
	PlatformName() string
	FileExists() bool
	IsRunning(ctx context.Context) bool
	ServicePath() string
	Install(ctx context.Context) error
	Start(ctx context.Context) Result
	Stop(ctx context.Context) Result
	StopAndDisableAutostart(ctx context.Context) Result
	Restart(ctx context.Context) Result
	WaitUntilStopped(ctx context.Context, timeout time.Duration) (bool, error)
	DeleteFile(ctx context.Context) error
	DescribeStatus(ctx context.Context) string
	ParseStatus(text string) ParsedStatus
}

type AdapterOptions struct {
	Profile    string
	RootDir    string
	Executable string
	EnvPath    string
	Runner     CommandRunner
}

type CommandRunner interface {
	Run(ctx context.Context, command string, args ...string) Result
}

type CommandRunnerFunc func(ctx context.Context, command string, args ...string) Result

func (f CommandRunnerFunc) Run(ctx context.Context, command string, args ...string) Result {
	return f(ctx, command, args...)
}

func NewPlatformAdapter(opts AdapterOptions) (Adapter, error) {
	opts.Profile = strings.TrimSpace(opts.Profile)
	if opts.Profile == "" {
		return nil, fmt.Errorf("profile is required")
	}
	if opts.RootDir == "" {
		return nil, fmt.Errorf("root directory is required")
	}
	if opts.Executable == "" {
		return nil, fmt.Errorf("executable is required")
	}
	if opts.EnvPath == "" {
		opts.EnvPath = os.Getenv("PATH")
	}
	if opts.Runner == nil {
		opts.Runner = execRunner{}
	}
	base := platformAdapter{opts: opts}
	switch runtime.GOOS {
	case "darwin":
		return launchdAdapter{platformAdapter: base}, nil
	case "linux":
		return systemdAdapter{platformAdapter: base}, nil
	case "windows":
		return schtasksAdapter{platformAdapter: base}, nil
	default:
		return nil, fmt.Errorf("unsupported service platform: %s", runtime.GOOS)
	}
}

type platformAdapter struct {
	opts AdapterOptions
}

func (a platformAdapter) writeFile(path string, data string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(data), 0o600)
}

func (a platformAdapter) ensureLogDir() error {
	dir, err := DaemonLogDir(a.opts.RootDir, a.opts.Profile)
	if err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o700)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, command string, args ...string) Result {
	cmd := exec.CommandContext(ctx, command, args...)
	out, err := cmd.Output()
	result := Result{Stdout: string(out), OK: err == nil}
	if err == nil {
		return result
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.Stderr = string(exitErr.Stderr)
	} else {
		result.Stderr = err.Error()
	}
	return result
}

type launchdAdapter struct {
	platformAdapter
}

func (a launchdAdapter) PlatformName() string { return "launchd (macOS)" }

func (a launchdAdapter) FileExists() bool {
	path, err := LaunchAgentPlistPath(a.opts.Profile)
	return err == nil && fileExists(path)
}

func (a launchdAdapter) IsRunning(ctx context.Context) bool {
	return a.opts.Runner.Run(ctx, "launchctl", "print", a.serviceTarget()).OK
}

func (a launchdAdapter) ServicePath() string {
	path, _ := LaunchAgentPlistPath(a.opts.Profile)
	return path
}

func (a launchdAdapter) Install(ctx context.Context) error {
	stdout, err := DaemonStdoutPath(a.opts.RootDir, a.opts.Profile)
	if err != nil {
		return err
	}
	stderr, err := DaemonStderrPath(a.opts.RootDir, a.opts.Profile)
	if err != nil {
		return err
	}
	body, err := BuildLaunchdPlist(DefinitionInputs{
		Executable: a.opts.Executable,
		EnvPath:    a.opts.EnvPath,
		Profile:    a.opts.Profile,
		Home:       a.opts.RootDir,
		StdoutPath: stdout,
		StderrPath: stderr,
	})
	if err != nil {
		return err
	}
	if err := a.ensureLogDir(); err != nil {
		return err
	}
	return a.writeFile(a.ServicePath(), body)
}

func (a launchdAdapter) Start(ctx context.Context) Result {
	return a.opts.Runner.Run(ctx, "launchctl", "bootstrap", a.userTarget(), a.ServicePath())
}

func (a launchdAdapter) Stop(ctx context.Context) Result {
	return a.opts.Runner.Run(ctx, "launchctl", "bootout", a.serviceTarget())
}

func (a launchdAdapter) StopAndDisableAutostart(ctx context.Context) Result {
	return a.Stop(ctx)
}

func (a launchdAdapter) Restart(ctx context.Context) Result {
	return a.opts.Runner.Run(ctx, "launchctl", "kickstart", "-k", a.serviceTarget())
}

func (a launchdAdapter) WaitUntilStopped(ctx context.Context, timeout time.Duration) (bool, error) {
	return waitUntil(ctx, timeout, func() bool { return !a.IsRunning(ctx) })
}

func (a launchdAdapter) DeleteFile(ctx context.Context) error {
	return os.Remove(a.ServicePath())
}

func (a launchdAdapter) DescribeStatus(ctx context.Context) string {
	result := a.opts.Runner.Run(ctx, "launchctl", "print", a.serviceTarget())
	if result.Stdout != "" {
		return result.Stdout
	}
	return result.Stderr
}

func (a launchdAdapter) ParseStatus(text string) ParsedStatus {
	return ParsedStatus{
		PID:      firstSubmatch(text, `pid\s*=\s*(\d+)`),
		LastExit: firstSubmatch(text, `(?i)last exit code\s*=\s*(-?\d+)`),
	}
}

func (a launchdAdapter) userTarget() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

func (a launchdAdapter) serviceTarget() string {
	label, _ := LaunchAgentLabel(a.opts.Profile)
	return a.userTarget() + "/" + label
}

type systemdAdapter struct {
	platformAdapter
}

func (a systemdAdapter) PlatformName() string { return "systemd (Linux user)" }

func (a systemdAdapter) FileExists() bool {
	path, err := SystemdUnitPath(a.opts.Profile)
	return err == nil && fileExists(path)
}

func (a systemdAdapter) IsRunning(ctx context.Context) bool {
	return a.opts.Runner.Run(ctx, "systemctl", "--user", "is-active", a.unitName()).OK
}

func (a systemdAdapter) ServicePath() string {
	path, _ := SystemdUnitPath(a.opts.Profile)
	return path
}

func (a systemdAdapter) Install(ctx context.Context) error {
	stdout, err := DaemonStdoutPath(a.opts.RootDir, a.opts.Profile)
	if err != nil {
		return err
	}
	stderr, err := DaemonStderrPath(a.opts.RootDir, a.opts.Profile)
	if err != nil {
		return err
	}
	body, err := BuildSystemdUnit(DefinitionInputs{
		Executable: a.opts.Executable,
		EnvPath:    a.opts.EnvPath,
		Profile:    a.opts.Profile,
		Home:       a.opts.RootDir,
		StdoutPath: stdout,
		StderrPath: stderr,
	})
	if err != nil {
		return err
	}
	if err := a.ensureLogDir(); err != nil {
		return err
	}
	if err := a.writeFile(a.ServicePath(), body); err != nil {
		return err
	}
	result := a.opts.Runner.Run(ctx, "systemctl", "--user", "daemon-reload")
	if !result.OK {
		return fmt.Errorf("systemctl daemon-reload failed: %s", strings.TrimSpace(result.Stderr))
	}
	return nil
}

func (a systemdAdapter) Start(ctx context.Context) Result {
	return a.opts.Runner.Run(ctx, "systemctl", "--user", "enable", "--now", a.unitName())
}

func (a systemdAdapter) Stop(ctx context.Context) Result {
	return a.opts.Runner.Run(ctx, "systemctl", "--user", "stop", a.unitName())
}

func (a systemdAdapter) StopAndDisableAutostart(ctx context.Context) Result {
	return a.opts.Runner.Run(ctx, "systemctl", "--user", "disable", "--now", a.unitName())
}

func (a systemdAdapter) Restart(ctx context.Context) Result {
	return a.opts.Runner.Run(ctx, "systemctl", "--user", "restart", a.unitName())
}

func (a systemdAdapter) WaitUntilStopped(ctx context.Context, timeout time.Duration) (bool, error) {
	return waitUntil(ctx, timeout, func() bool { return !a.IsRunning(ctx) })
}

func (a systemdAdapter) DeleteFile(ctx context.Context) error {
	err := os.Remove(a.ServicePath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	result := a.opts.Runner.Run(ctx, "systemctl", "--user", "daemon-reload")
	if !result.OK {
		return fmt.Errorf("systemctl daemon-reload failed: %s", strings.TrimSpace(result.Stderr))
	}
	return nil
}

func (a systemdAdapter) DescribeStatus(ctx context.Context) string {
	result := a.opts.Runner.Run(ctx, "systemctl", "--user", "status", a.unitName(), "--no-pager")
	if result.Stdout != "" {
		return result.Stdout
	}
	return result.Stderr
}

func (a systemdAdapter) ParseStatus(text string) ParsedStatus {
	return ParsedStatus{
		PID:      firstSubmatch(text, `Main PID:\s*(\d+)`),
		LastExit: firstSubmatch(text, `Process:\s+\d+\s+ExecStart=.*status=(\d+)`),
	}
}

func (a systemdAdapter) unitName() string {
	name, _ := SystemdUnitName(a.opts.Profile)
	return name
}

type schtasksAdapter struct {
	platformAdapter
}

func (a schtasksAdapter) PlatformName() string { return "Task Scheduler (Windows)" }

func (a schtasksAdapter) FileExists() bool {
	return a.opts.Runner.Run(context.Background(), "schtasks", "/Query", "/TN", a.taskName()).OK
}

func (a schtasksAdapter) IsRunning(ctx context.Context) bool {
	result := a.opts.Runner.Run(ctx, "schtasks", "/Query", "/V", "/FO", "LIST", "/TN", a.taskName())
	return result.OK && regexp.MustCompile(`(?i)Status:\s+Running`).MatchString(result.Stdout)
}

func (a schtasksAdapter) ServicePath() string {
	name, _ := WindowsTaskName(a.opts.Profile)
	return name
}

func (a schtasksAdapter) Install(ctx context.Context) error {
	stdout, err := DaemonStdoutPath(a.opts.RootDir, a.opts.Profile)
	if err != nil {
		return err
	}
	stderr, err := DaemonStderrPath(a.opts.RootDir, a.opts.Profile)
	if err != nil {
		return err
	}
	body, err := BuildWindowsLauncherCmd(DefinitionInputs{
		Executable: a.opts.Executable,
		EnvPath:    a.opts.EnvPath,
		Profile:    a.opts.Profile,
		Home:       a.opts.RootDir,
		StdoutPath: stdout,
		StderrPath: stderr,
	})
	if err != nil {
		return err
	}
	if err := a.ensureLogDir(); err != nil {
		return err
	}
	launcher, err := WindowsLauncherCmdPath(a.opts.RootDir, a.opts.Profile)
	if err != nil {
		return err
	}
	if err := a.writeFile(launcher, body); err != nil {
		return err
	}
	result := a.opts.Runner.Run(ctx, "schtasks", "/Create", "/F", "/SC", "ONLOGON", "/RL", "LIMITED", "/TN", a.taskName(), "/TR", `"`+launcher+`"`)
	if !result.OK {
		return fmt.Errorf("schtasks /Create failed: %s", strings.TrimSpace(result.Stderr))
	}
	return nil
}

func (a schtasksAdapter) Start(ctx context.Context) Result {
	return a.opts.Runner.Run(ctx, "schtasks", "/Run", "/TN", a.taskName())
}

func (a schtasksAdapter) Stop(ctx context.Context) Result {
	return a.opts.Runner.Run(ctx, "schtasks", "/End", "/TN", a.taskName())
}

func (a schtasksAdapter) StopAndDisableAutostart(ctx context.Context) Result {
	ended := a.Stop(ctx)
	disabled := a.opts.Runner.Run(ctx, "schtasks", "/Change", "/TN", a.taskName(), "/Disable")
	if disabled.OK {
		return disabled
	}
	if ended.OK {
		return disabled
	}
	return ended
}

func (a schtasksAdapter) Restart(ctx context.Context) Result {
	_ = a.Stop(ctx)
	ok, _ := a.WaitUntilStopped(ctx, 5*time.Second)
	if !ok {
		return Result{OK: false, Stderr: "task did not stop before restart"}
	}
	return a.Start(ctx)
}

func (a schtasksAdapter) WaitUntilStopped(ctx context.Context, timeout time.Duration) (bool, error) {
	return waitUntil(ctx, timeout, func() bool { return !a.IsRunning(ctx) })
}

func (a schtasksAdapter) DeleteFile(ctx context.Context) error {
	result := a.opts.Runner.Run(ctx, "schtasks", "/Delete", "/F", "/TN", a.taskName())
	if !result.OK {
		return fmt.Errorf("schtasks /Delete failed: %s", strings.TrimSpace(result.Stderr))
	}
	launcher, err := WindowsLauncherCmdPath(a.opts.RootDir, a.opts.Profile)
	if err == nil {
		_ = os.Remove(launcher)
	}
	return nil
}

func (a schtasksAdapter) DescribeStatus(ctx context.Context) string {
	result := a.opts.Runner.Run(ctx, "schtasks", "/Query", "/V", "/FO", "LIST", "/TN", a.taskName())
	if result.Stdout != "" {
		return result.Stdout
	}
	return result.Stderr
}

func (a schtasksAdapter) ParseStatus(text string) ParsedStatus {
	return ParsedStatus{
		PID:      firstSubmatch(text, `(?i)Process ID:\s*(\d+)`),
		LastExit: firstSubmatch(text, `(?i)Last Result:\s*(\d+)`),
	}
}

func (a schtasksAdapter) taskName() string {
	name, _ := WindowsTaskName(a.opts.Profile)
	return name
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func waitUntil(ctx context.Context, timeout time.Duration, done func() bool) (bool, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if done() {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.C:
			return false, nil
		case <-ticker.C:
		}
	}
}

func firstSubmatch(text, pattern string) string {
	match := regexp.MustCompile(pattern).FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}
