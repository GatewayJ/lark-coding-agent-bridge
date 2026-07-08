package bridge

import (
	"context"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runtimecoord"
	appservice "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/service"
)

var ErrServiceConnectTimeout = appservice.ErrServiceConnectTimeout

type ServiceAdapterOptions struct {
	Profile    string
	RootDir    string
	Executable string
	EnvPath    string
	Runner     ServiceCommandRunner
}

type ServiceResult struct {
	OK     bool
	Stdout string
	Stderr string
}

type ServiceCommandRunner interface {
	Run(ctx context.Context, command string, args ...string) ServiceResult
}

type ServiceCommandRunnerFunc func(ctx context.Context, command string, args ...string) ServiceResult

func (f ServiceCommandRunnerFunc) Run(ctx context.Context, command string, args ...string) ServiceResult {
	return f(ctx, command, args...)
}

type ServiceDefinitionInputs struct {
	Executable string
	EnvPath    string
	Profile    string
	Home       string
	StdoutPath string
	StderrPath string
}

type ServiceAdapter interface {
	PlatformName() string
	FileExists() bool
	IsRunning(ctx context.Context) bool
	ServicePath() string
	Install(ctx context.Context) error
	Start(ctx context.Context) ServiceResult
	Stop(ctx context.Context) ServiceResult
	StopAndDisableAutostart(ctx context.Context) ServiceResult
	Restart(ctx context.Context) ServiceResult
	WaitUntilStopped(ctx context.Context, timeout time.Duration) (bool, error)
	DeleteFile(ctx context.Context) error
	DescribeStatus(ctx context.Context) string
	ParseStatus(text string) ServiceParsedStatus
}

type ServiceParsedStatus struct {
	PID      string
	LastExit string
}

type ServiceController struct {
	inner appservice.Controller
}

func (c ServiceController) RootDir() string {
	return c.inner.RootDir
}

func (c ServiceController) Profile() string {
	return c.inner.Profile
}

func (c ServiceController) AppID() string {
	return c.inner.AppID
}

type ServiceControllerOptions struct {
	Adapter          ServiceAdapter
	RootDir          string
	Profile          string
	AppID            string
	AgentKind        RuntimeAgentKind
	Preflight        func(context.Context) error
	WaitTimeout      time.Duration
	RequireConnected bool
	ProcessLister    ServiceProcessLister
	LockHandler      ServiceRuntimeLockHandler
}

type ServiceProcessLister interface {
	ListServiceProcesses(ctx context.Context) ([]ServiceProcessEntry, error)
}

type ServiceProcessListerFunc func(context.Context) ([]ServiceProcessEntry, error)

func (f ServiceProcessListerFunc) ListServiceProcesses(ctx context.Context) ([]ServiceProcessEntry, error) {
	return f(ctx)
}

type ServiceRuntimeLockHandler interface {
	HandleServiceRuntimeLockConflict(ctx context.Context, meta ServiceRuntimeLockMeta) (retry bool, err error)
}

type ServiceRuntimeLockHandlerFunc func(context.Context, ServiceRuntimeLockMeta) (bool, error)

func (f ServiceRuntimeLockHandlerFunc) HandleServiceRuntimeLockConflict(ctx context.Context, meta ServiceRuntimeLockMeta) (bool, error) {
	return f(ctx, meta)
}

type ServiceProcessEntry struct {
	ID          string
	PID         int
	AppID       string
	Tenant      string
	ProfileName string
	AgentKind   string
	ConfigPath  string
	StartedAt   string
	Version     string
	BotName     string
}

type ServiceRuntimeLockMeta struct {
	Kind      string
	Target    string
	Profile   string
	AgentKind string
	AppID     string
	PID       int
	StartedAt string
}

type ServiceStartResult struct {
	Profile    string
	AppID      string
	Connected  bool
	Process    *ServiceProcessEntry
	Service    ServiceStatus
	StartError string
}

type ServiceStatus struct {
	Profile      string
	Platform     string
	ServicePath  string
	Installed    bool
	Running      bool
	PID          string
	LastExit     string
	StdoutPath   string
	StderrPath   string
	Process      *ServiceProcessEntry
	ProcessError string
}

type ServiceStopResult struct {
	Profile string
	Stopped bool
	Message string
}

type ServiceUnregisterResult struct {
	Profile string
	Removed bool
}

func NewServiceController(options ServiceControllerOptions) ServiceController {
	return ServiceController{inner: appservice.Controller{
		Adapter:          wrapInternalServiceAdapter(options.Adapter),
		RootDir:          options.RootDir,
		Profile:          options.Profile,
		AppID:            options.AppID,
		AgentKind:        runtimecoord.AgentKind(options.AgentKind),
		Preflight:        options.Preflight,
		WaitTimeout:      options.WaitTimeout,
		RequireConnected: options.RequireConnected,
		ProcessLister:    wrapServiceProcessLister(options.ProcessLister),
		LockHandler:      wrapServiceRuntimeLockHandler(options.LockHandler),
	}}
}

func NewPlatformServiceAdapter(options ServiceAdapterOptions) (ServiceAdapter, error) {
	adapter, err := appservice.NewPlatformAdapter(toInternalServiceAdapterOptions(options))
	if err != nil {
		return nil, err
	}
	return serviceAdapterWrapper{inner: adapter}, nil
}

func (c ServiceController) Start(ctx context.Context) (ServiceStartResult, error) {
	result, err := c.inner.Start(ctx)
	return toServiceStartResult(result), toPublicRuntimeError(err)
}

func StartService(ctx context.Context, controller ServiceController) (ServiceStartResult, error) {
	return controller.Start(ctx)
}

func (c ServiceController) Stop(ctx context.Context) (ServiceStopResult, error) {
	result, err := c.inner.Stop(ctx)
	return ServiceStopResult(result), err
}

func (c ServiceController) Restart(ctx context.Context) (ServiceStartResult, error) {
	result, err := c.inner.Restart(ctx)
	return toServiceStartResult(result), toPublicRuntimeError(err)
}

func (c ServiceController) Status(ctx context.Context) ServiceStatus {
	return toServiceStatus(c.inner.Status(ctx))
}

func (c ServiceController) Unregister(ctx context.Context) (ServiceUnregisterResult, error) {
	result, err := c.inner.Unregister(ctx)
	return ServiceUnregisterResult(result), err
}

func BuildLaunchdPlist(inputs ServiceDefinitionInputs) (string, error) {
	return appservice.BuildLaunchdPlist(toInternalServiceDefinitionInputs(inputs))
}

func BuildSystemdUnit(inputs ServiceDefinitionInputs) (string, error) {
	return appservice.BuildSystemdUnit(toInternalServiceDefinitionInputs(inputs))
}

func BuildWindowsLauncherCmd(inputs ServiceDefinitionInputs) (string, error) {
	return appservice.BuildWindowsLauncherCmd(toInternalServiceDefinitionInputs(inputs))
}

func toInternalServiceAdapterOptions(options ServiceAdapterOptions) appservice.AdapterOptions {
	return appservice.AdapterOptions{
		Profile:    options.Profile,
		RootDir:    options.RootDir,
		Executable: options.Executable,
		EnvPath:    options.EnvPath,
		Runner:     wrapServiceCommandRunner(options.Runner),
	}
}

func toInternalServiceDefinitionInputs(inputs ServiceDefinitionInputs) appservice.DefinitionInputs {
	return appservice.DefinitionInputs{
		Executable: inputs.Executable,
		EnvPath:    inputs.EnvPath,
		Profile:    inputs.Profile,
		Home:       inputs.Home,
		StdoutPath: inputs.StdoutPath,
		StderrPath: inputs.StderrPath,
	}
}

func wrapServiceCommandRunner(runner ServiceCommandRunner) appservice.CommandRunner {
	if runner == nil {
		return nil
	}
	return appservice.CommandRunnerFunc(func(ctx context.Context, command string, args ...string) appservice.Result {
		return toInternalServiceResult(runner.Run(ctx, command, args...))
	})
}

func wrapServiceProcessLister(lister ServiceProcessLister) appservice.ProcessLister {
	if lister == nil {
		return nil
	}
	return appservice.ProcessListerFunc(func(ctx context.Context) ([]runtimecoord.ProcessEntry, error) {
		entries, err := lister.ListServiceProcesses(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]runtimecoord.ProcessEntry, 0, len(entries))
		for _, entry := range entries {
			out = append(out, toRuntimeProcessEntry(entry))
		}
		return out, nil
	})
}

func wrapServiceRuntimeLockHandler(handler ServiceRuntimeLockHandler) appservice.RuntimeLockHandler {
	if handler == nil {
		return nil
	}
	return appservice.RuntimeLockHandlerFunc(func(ctx context.Context, meta runtimecoord.RuntimeLockMeta) (bool, error) {
		return handler.HandleServiceRuntimeLockConflict(ctx, toServiceRuntimeLockMeta(meta))
	})
}

func toServiceStartResult(result appservice.StartResult) ServiceStartResult {
	return ServiceStartResult{
		Profile:    result.Profile,
		AppID:      result.AppID,
		Connected:  result.Connected,
		Process:    toServiceProcessEntryPtr(result.Process),
		Service:    toServiceStatus(result.Service),
		StartError: result.StartError,
	}
}

func toServiceStatus(status appservice.StatusResult) ServiceStatus {
	return ServiceStatus{
		Profile:      status.Profile,
		Platform:     status.Platform,
		ServicePath:  status.ServicePath,
		Installed:    status.Installed,
		Running:      status.Running,
		PID:          status.PID,
		LastExit:     status.LastExit,
		StdoutPath:   status.StdoutPath,
		StderrPath:   status.StderrPath,
		Process:      toServiceProcessEntryPtr(status.Process),
		ProcessError: status.ProcessError,
	}
}

func toServiceProcessEntryPtr(entry *runtimecoord.ProcessEntry) *ServiceProcessEntry {
	if entry == nil {
		return nil
	}
	out := toServiceProcessEntry(*entry)
	return &out
}

func toServiceProcessEntry(entry runtimecoord.ProcessEntry) ServiceProcessEntry {
	return ServiceProcessEntry{
		ID:          entry.ID,
		PID:         entry.PID,
		AppID:       entry.AppID,
		Tenant:      string(entry.Tenant),
		ProfileName: entry.ProfileName,
		AgentKind:   string(entry.AgentKind),
		ConfigPath:  entry.ConfigPath,
		StartedAt:   entry.StartedAt,
		Version:     entry.Version,
		BotName:     entry.BotName,
	}
}

func toRuntimeProcessEntry(entry ServiceProcessEntry) runtimecoord.ProcessEntry {
	return runtimecoord.ProcessEntry{
		ID:          entry.ID,
		PID:         entry.PID,
		AppID:       entry.AppID,
		Tenant:      runtimecoord.TenantBrand(entry.Tenant),
		ProfileName: entry.ProfileName,
		AgentKind:   runtimecoord.AgentKind(entry.AgentKind),
		ConfigPath:  entry.ConfigPath,
		StartedAt:   entry.StartedAt,
		Version:     entry.Version,
		BotName:     entry.BotName,
	}
}

func toServiceRuntimeLockMeta(meta runtimecoord.RuntimeLockMeta) ServiceRuntimeLockMeta {
	return ServiceRuntimeLockMeta{
		Kind:      string(meta.Kind),
		Target:    meta.Target,
		Profile:   meta.Profile,
		AgentKind: string(meta.AgentKind),
		AppID:     meta.AppID,
		PID:       meta.PID,
		StartedAt: meta.StartedAt,
	}
}

func wrapInternalServiceAdapter(adapter ServiceAdapter) appservice.Adapter {
	if adapter == nil {
		return nil
	}
	return internalServiceAdapter{adapter: adapter}
}

type internalServiceAdapter struct {
	adapter ServiceAdapter
}

func (a internalServiceAdapter) PlatformName() string { return a.adapter.PlatformName() }
func (a internalServiceAdapter) FileExists() bool     { return a.adapter.FileExists() }
func (a internalServiceAdapter) IsRunning(ctx context.Context) bool {
	return a.adapter.IsRunning(ctx)
}
func (a internalServiceAdapter) ServicePath() string { return a.adapter.ServicePath() }
func (a internalServiceAdapter) Install(ctx context.Context) error {
	return a.adapter.Install(ctx)
}
func (a internalServiceAdapter) Start(ctx context.Context) appservice.Result {
	return toInternalServiceResult(a.adapter.Start(ctx))
}
func (a internalServiceAdapter) Stop(ctx context.Context) appservice.Result {
	return toInternalServiceResult(a.adapter.Stop(ctx))
}
func (a internalServiceAdapter) StopAndDisableAutostart(ctx context.Context) appservice.Result {
	return toInternalServiceResult(a.adapter.StopAndDisableAutostart(ctx))
}
func (a internalServiceAdapter) Restart(ctx context.Context) appservice.Result {
	return toInternalServiceResult(a.adapter.Restart(ctx))
}
func (a internalServiceAdapter) WaitUntilStopped(ctx context.Context, timeout time.Duration) (bool, error) {
	return a.adapter.WaitUntilStopped(ctx, timeout)
}
func (a internalServiceAdapter) DeleteFile(ctx context.Context) error {
	return a.adapter.DeleteFile(ctx)
}
func (a internalServiceAdapter) DescribeStatus(ctx context.Context) string {
	return a.adapter.DescribeStatus(ctx)
}
func (a internalServiceAdapter) ParseStatus(text string) appservice.ParsedStatus {
	return toInternalServiceParsedStatus(a.adapter.ParseStatus(text))
}

type serviceAdapterWrapper struct {
	inner appservice.Adapter
}

func (a serviceAdapterWrapper) PlatformName() string { return a.inner.PlatformName() }
func (a serviceAdapterWrapper) FileExists() bool     { return a.inner.FileExists() }
func (a serviceAdapterWrapper) IsRunning(ctx context.Context) bool {
	return a.inner.IsRunning(ctx)
}
func (a serviceAdapterWrapper) ServicePath() string { return a.inner.ServicePath() }
func (a serviceAdapterWrapper) Install(ctx context.Context) error {
	return a.inner.Install(ctx)
}
func (a serviceAdapterWrapper) Start(ctx context.Context) ServiceResult {
	return toServiceResult(a.inner.Start(ctx))
}
func (a serviceAdapterWrapper) Stop(ctx context.Context) ServiceResult {
	return toServiceResult(a.inner.Stop(ctx))
}
func (a serviceAdapterWrapper) StopAndDisableAutostart(ctx context.Context) ServiceResult {
	return toServiceResult(a.inner.StopAndDisableAutostart(ctx))
}
func (a serviceAdapterWrapper) Restart(ctx context.Context) ServiceResult {
	return toServiceResult(a.inner.Restart(ctx))
}
func (a serviceAdapterWrapper) WaitUntilStopped(ctx context.Context, timeout time.Duration) (bool, error) {
	return a.inner.WaitUntilStopped(ctx, timeout)
}
func (a serviceAdapterWrapper) DeleteFile(ctx context.Context) error {
	return a.inner.DeleteFile(ctx)
}
func (a serviceAdapterWrapper) DescribeStatus(ctx context.Context) string {
	return a.inner.DescribeStatus(ctx)
}
func (a serviceAdapterWrapper) ParseStatus(text string) ServiceParsedStatus {
	return toServiceParsedStatus(a.inner.ParseStatus(text))
}

func toServiceParsedStatus(status appservice.ParsedStatus) ServiceParsedStatus {
	return ServiceParsedStatus{PID: status.PID, LastExit: status.LastExit}
}

func toInternalServiceParsedStatus(status ServiceParsedStatus) appservice.ParsedStatus {
	return appservice.ParsedStatus{PID: status.PID, LastExit: status.LastExit}
}

func toServiceResult(result appservice.Result) ServiceResult {
	return ServiceResult{OK: result.OK, Stdout: result.Stdout, Stderr: result.Stderr}
}

func toInternalServiceResult(result ServiceResult) appservice.Result {
	return appservice.Result{OK: result.OK, Stdout: result.Stdout, Stderr: result.Stderr}
}
