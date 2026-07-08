package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	larksdk "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/configstore"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runtimecoord"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/secretstore"
	appworkspace "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/workspace"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/compat/apppaths"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
	"github.com/zarazhangrui/lark-coding-agent-bridge/pkg/bridge"
	"golang.org/x/term"
)

var version = "dev"

var unsupportedLarkChannelSourceDiagnosticRE = regexp.MustCompile(`(?i)(unknown flag:\s*--source|unknown command ["']?bind["']?|invalid --source[^-\n]*lark-channel|unsupported source:\s*lark-channel)`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printHelp(stdout)
		return 0
	}
	switch args[0] {
	case "-h", "--help", "help":
		printHelp(stdout)
		return 0
	case "-v", "--version", "version":
		fmt.Fprintf(stdout, "lark-channel-bridge %s\n", version)
		return 0
	case "run":
		if err := runStart(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "%s failed: %v\n", args[0], err)
			return 1
		}
		return 0
	case "start":
		if err := runServiceStart(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "start failed: %v\n", err)
			return 1
		}
		return 0
	case "stop":
		if err := runServiceStop(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "stop failed: %v\n", err)
			return 1
		}
		return 0
	case "restart":
		if err := runServiceRestart(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "restart failed: %v\n", err)
			return 1
		}
		return 0
	case "status":
		if err := runServiceStatus(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "status failed: %v\n", err)
			return 1
		}
		return 0
	case "unregister":
		if err := runServiceUnregister(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "unregister failed: %v\n", err)
			return 1
		}
		return 0
	case "migrate":
		if err := runMigrate(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "migrate failed: %v\n", err)
			return 1
		}
		return 0
	case "ps":
		if err := runPS(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "ps failed: %v\n", err)
			return 1
		}
		return 0
	case "kill":
		if err := runKill(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "kill failed: %v\n", err)
			return 1
		}
		return 0
	case "profile":
		return runProfile(args[1:], stdout, stderr)
	case "secrets":
		return runSecrets(args[1:], os.Stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		printHelp(stderr)
		return 2
	}
}

type startOptions struct {
	Home      string
	Config    string
	Profile   string
	Agent     string
	AppID     string
	AppSecret string
	Tenant    string
	Workspace string

	SkipCheckLarkCli bool
}

type serviceProfileOptions struct {
	Home    string
	Profile string
}

type secretsOptions struct {
	Home    string
	Profile string
	AppID   string
	Value   string
}

type psOptions struct {
	Home string
}

type killOptions struct {
	Home   string
	Target string
}

type migrateOptions struct {
	Home    string
	Config  string
	Profile string
	Agent   string
}

type profileOptions struct {
	Home           string
	Output         string
	Force          bool
	IncludeSecrets bool
	Yes            bool
	Profile        string
}

type profileCreateOptions struct {
	Home      string
	Agent     string
	Workspace string
	AppID     string
	AppSecret string
	Tenant    string
	Profile   string
}

type profileRemoveOptions struct {
	Home    string
	Purge   bool
	Yes     bool
	Profile string
}

type execSecretRequest struct {
	ProtocolVersion int      `json:"protocolVersion,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	IDs             []string `json:"ids,omitempty"`
}

type execSecretError struct {
	Message string `json:"message"`
}

type execSecretResponse struct {
	ProtocolVersion int                        `json:"protocolVersion"`
	Values          map[string]string          `json:"values"`
	Errors          map[string]execSecretError `json:"errors,omitempty"`
}

func runStart(args []string, stdout, stderr io.Writer) error {
	opts, err := parseStartOptions(args, stderr)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	bridgeInstance, profileName, err := buildStartBridge(ctx, opts)
	if err != nil {
		return err
	}
	if err := bridgeInstance.Start(ctx); err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		defer cancel()
		if err := bridgeInstance.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintf(stderr, "shutdown failed: %v\n", err)
		}
	}()

	status, _ := bridgeInstance.Status(ctx)
	botName := status.Lark.BotName
	if botName == "" {
		botName = status.Lark.BotOpenID
	}
	fmt.Fprintf(stdout, "bridge started profile=%s mode=%s bot=%s\n", profileName, status.Bridge.Mode, botName)
	<-ctx.Done()
	fmt.Fprintln(stdout, "stopping bridge...")
	return nil
}

const defaultShutdownTimeout = 10 * time.Second
const larkCLIInstallTimeout = 5 * time.Minute

func parseStartOptions(args []string, stderr io.Writer) (startOptions, error) {
	var opts startOptions
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Home, "home", "", "bridge home directory")
	fs.StringVar(&opts.Config, "config", "", "config.json path")
	fs.StringVar(&opts.Profile, "profile", "", "profile name")
	fs.StringVar(&opts.Agent, "agent", "", "agent kind: codex or claude")
	fs.StringVar(&opts.AppID, "app-id", "", "override app id")
	fs.StringVar(&opts.AppSecret, "app-secret", "", "override app secret")
	fs.StringVar(&opts.Tenant, "tenant", "", "override tenant: feishu or lark")
	fs.StringVar(&opts.Workspace, "workspace", "", "default workspace for first-run bootstrap")
	fs.BoolVar(&opts.SkipCheckLarkCli, "skip-check-lark-cli", false, "skip lark-cli preflight")
	if err := fs.Parse(args); err != nil {
		return startOptions{}, err
	}
	if fs.NArg() > 0 {
		return startOptions{}, fmt.Errorf("unexpected start arguments: %v", fs.Args())
	}
	return opts, nil
}

func runServiceStart(args []string, stdout, stderr io.Writer) error {
	opts, err := parseStartOptions(args, stderr)
	if err != nil {
		return err
	}
	ctx := context.Background()
	controller, err := buildServiceController(ctx, opts)
	if err != nil {
		return err
	}
	result, err := controller.Start(ctx)
	if err != nil {
		if errors.Is(err, errServiceStartCancelled) {
			return nil
		}
		return err
	}
	if result.Process != nil {
		agent := agentDisplay(configstore.AgentKind(result.Process.AgentKind))
		fmt.Fprintf(stdout, "✓ 已启动 bot: %s (%s) agent: %s (%s) 进程: %s\n", result.Process.BotName, result.Process.AppID, agent.displayName, agent.id, result.Process.ID)
		return nil
	}
	fmt.Fprintf(stdout, "⚠ 已下发启动指令, 但等待窗口内未观察到 bot 连接成功。\n")
	fmt.Fprintf(stdout, "  日志: %s\n", result.Service.StderrPath)
	return nil
}

var errServiceStartCancelled = errors.New("service start cancelled")

func runServiceStop(args []string, stdout, stderr io.Writer) error {
	controller, err := buildServiceProfileController(args, "stop", stderr)
	if err != nil {
		return err
	}
	result, err := controller.Stop(context.Background())
	if err != nil {
		return err
	}
	switch result.Message {
	case "not-installed":
		fmt.Fprintln(stdout, "bot 还没在后台运行过,无需停止。")
	case "not-running":
		fmt.Fprintln(stdout, "bot 当前没在后台运行。")
	default:
		fmt.Fprintln(stdout, "✓ bot 已停止运行")
		fmt.Fprintln(stdout, "  通过 `start` 可再次重启")
	}
	return nil
}

func runServiceRestart(args []string, stdout, stderr io.Writer) error {
	controller, err := buildServiceProfileController(args, "restart", stderr)
	if err != nil {
		return err
	}
	result, err := controller.Restart(context.Background())
	if err != nil {
		return err
	}
	if result.Process != nil {
		agent := agentDisplay(configstore.AgentKind(result.Process.AgentKind))
		fmt.Fprintf(stdout, "✓ 已重启 bot: %s (%s) agent: %s (%s) 进程: %s\n", result.Process.BotName, result.Process.AppID, agent.displayName, agent.id, result.Process.ID)
		return nil
	}
	fmt.Fprintf(stdout, "⚠ 已下发重启指令, 但等待窗口内未观察到 bot 连接成功。\n")
	fmt.Fprintf(stdout, "  日志: %s\n", result.Service.StderrPath)
	return nil
}

func runServiceStatus(args []string, stdout, stderr io.Writer) error {
	controller, err := buildServiceProfileController(args, "status", stderr)
	if err != nil {
		return err
	}
	status := controller.Status(context.Background())
	if !status.Installed {
		fmt.Fprintln(stdout, "bot 当前没在后台运行(从未启动过)")
		fmt.Fprintln(stdout, "  通过 `start` 启动 bot")
		return nil
	}
	if !status.Running {
		fmt.Fprintln(stdout, "bot 当前没在后台运行")
		fmt.Fprintln(stdout, "  通过 `start` 重新启动")
		return nil
	}
	if status.Process != nil {
		fmt.Fprintf(stdout, "✓ bot %s (%s) 正在后台运行\n", status.Process.BotName, status.Process.AppID)
	} else {
		fmt.Fprintln(stdout, "✓ bot 正在后台运行")
	}
	if status.PID != "" {
		fmt.Fprintf(stdout, "  进程 ID: %s\n", status.PID)
	}
	fmt.Fprintln(stdout, "  日志:")
	fmt.Fprintf(stdout, "    %s\n", status.StdoutPath)
	fmt.Fprintf(stdout, "    %s\n", status.StderrPath)
	if status.LastExit != "" && status.LastExit != "-1" {
		fmt.Fprintf(stdout, "  上次退出码: %s\n", status.LastExit)
	}
	return nil
}

func runServiceUnregister(args []string, stdout, stderr io.Writer) error {
	controller, err := buildServiceProfileController(args, "unregister", stderr)
	if err != nil {
		return err
	}
	result, err := controller.Unregister(context.Background())
	if err != nil {
		return err
	}
	if !result.Removed {
		fmt.Fprintln(stdout, "bot 还没在后台运行过,无需清理。")
		return nil
	}
	fmt.Fprintln(stdout, "✓ 已清除后台运行注册")
	fmt.Fprintf(stdout, "  (配置 / 日志 / 会话保留在 %s)\n", controller.RootDir())
	return nil
}

func buildServiceController(ctx context.Context, opts startOptions) (bridge.ServiceController, error) {
	info, err := resolveServiceRuntimeInfo(opts)
	if err != nil {
		return bridge.ServiceController{}, err
	}
	adapter, err := bridge.NewPlatformServiceAdapter(bridge.ServiceAdapterOptions{
		Profile:    info.Profile,
		RootDir:    info.RootDir,
		Executable: currentExecutable(),
		EnvPath:    os.Getenv("PATH"),
	})
	if err != nil {
		return bridge.ServiceController{}, err
	}
	preflight := func(ctx context.Context) error {
		if err := materializeEnvSecretForService(ctx, info.ConfigPath, info.Profile); err != nil {
			return err
		}
		if !opts.SkipCheckLarkCli {
			instance, _, err := buildStartBridge(ctx, opts)
			if instance != nil {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
				_ = instance.Shutdown(shutdownCtx)
				cancel()
			}
			return err
		}
		return nil
	}
	return bridge.NewServiceController(bridge.ServiceControllerOptions{
		Adapter:     adapter,
		RootDir:     info.RootDir,
		Profile:     info.Profile,
		AppID:       info.AppID,
		AgentKind:   bridge.RuntimeAgentKind(info.AgentKind),
		Preflight:   preflight,
		LockHandler: serviceLockHandler{},
	}), nil
}

func buildServiceProfileController(args []string, command string, stderr io.Writer) (bridge.ServiceController, error) {
	opts, err := parseServiceProfileOptions(args, command, stderr)
	if err != nil {
		return bridge.ServiceController{}, err
	}
	info, err := resolveServiceProfileInfo(opts)
	if err != nil {
		return bridge.ServiceController{}, err
	}
	adapter, err := bridge.NewPlatformServiceAdapter(bridge.ServiceAdapterOptions{
		Profile:    info.Profile,
		RootDir:    info.RootDir,
		Executable: currentExecutable(),
		EnvPath:    os.Getenv("PATH"),
	})
	if err != nil {
		return bridge.ServiceController{}, err
	}
	return bridge.NewServiceController(bridge.ServiceControllerOptions{
		Adapter:   adapter,
		RootDir:   info.RootDir,
		Profile:   info.Profile,
		AppID:     info.AppID,
		AgentKind: bridge.RuntimeAgentKind(info.AgentKind),
	}), nil
}

type serviceRuntimeInfo struct {
	RootDir    string
	Profile    string
	ConfigPath string
	AppID      string
	AgentKind  configstore.AgentKind
}

func resolveServiceRuntimeInfo(opts startOptions) (serviceRuntimeInfo, error) {
	home := opts.Home
	if home == "" && opts.Config != "" {
		home = filepath.Dir(opts.Config)
	}
	profile := opts.Profile
	if profile == "" && opts.Agent != "" {
		profile = opts.Agent
	}
	initialPaths, err := apppaths.Resolve(apppaths.Options{RootDir: home, Profile: profile})
	if err != nil {
		return serviceRuntimeInfo{}, err
	}
	configPath := opts.Config
	if configPath == "" {
		configPath = initialPaths.ConfigFile
	}
	if err := ensureStartConfigMigrated(initialPaths, configPath, opts); err != nil {
		return serviceRuntimeInfo{}, err
	}
	loadOptions := configstore.LoadOptions{Profile: profile}
	if opts.Agent != "" {
		loadOptions.AgentKind = configstore.AgentKind(opts.Agent)
	}
	snapshot, err := configstore.Load(configPath, loadOptions)
	if os.IsNotExist(err) {
		bootstrapOpts := opts
		bootstrapPaths := initialPaths
		if opts.Profile == "" && opts.Agent == "" {
			var detectErr error
			bootstrapOpts, bootstrapPaths, detectErr = selectFirstRunBootstrapAgent(opts, home)
			if detectErr != nil {
				return serviceRuntimeInfo{}, detectErr
			}
			loadOptions.Profile = bootstrapPaths.Profile
			loadOptions.AgentKind = configstore.AgentKind(bootstrapOpts.Agent)
		}
		if bootstrapErr := bootstrapStartConfig(bootstrapOpts, bootstrapPaths, configPath); bootstrapErr != nil {
			return serviceRuntimeInfo{}, bootstrapErr
		}
		snapshot, err = configstore.Load(configPath, loadOptions)
	}
	if err != nil {
		return serviceRuntimeInfo{}, err
	}
	appID := snapshot.Runtime.Accounts.App.ID
	if opts.AppID != "" {
		if err := assertStartAppMatchesExistingProfile(opts, snapshot.ProfileName, larkcli.AppConfig{Accounts: snapshot.Runtime.Accounts, Secrets: snapshot.Runtime.Secrets}); err != nil {
			return serviceRuntimeInfo{}, err
		}
		appID = opts.AppID
	}
	return serviceRuntimeInfo{
		RootDir:    initialPaths.RootDir,
		Profile:    snapshot.ProfileName,
		ConfigPath: configPath,
		AppID:      appID,
		AgentKind:  snapshot.Runtime.AgentKind,
	}, nil
}

func resolveServiceProfileInfo(opts serviceProfileOptions) (serviceRuntimeInfo, error) {
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: opts.Home, Profile: firstNonEmpty(opts.Profile, "claude")})
	if err != nil {
		return serviceRuntimeInfo{}, err
	}
	snapshot, err := configstore.Load(paths.ConfigFile, configstore.LoadOptions{Profile: opts.Profile})
	if err != nil {
		if opts.Profile != "" {
			return serviceRuntimeInfo{
				RootDir:    paths.RootDir,
				Profile:    opts.Profile,
				ConfigPath: paths.ConfigFile,
			}, nil
		}
		return serviceRuntimeInfo{}, err
	}
	return serviceRuntimeInfo{
		RootDir:    paths.RootDir,
		Profile:    snapshot.ProfileName,
		ConfigPath: paths.ConfigFile,
		AppID:      snapshot.Runtime.Accounts.App.ID,
		AgentKind:  snapshot.Runtime.AgentKind,
	}, nil
}

func parseServiceProfileOptions(args []string, command string, stderr io.Writer) (serviceProfileOptions, error) {
	var opts serviceProfileOptions
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Home, "home", "", "bridge home directory")
	fs.StringVar(&opts.Profile, "profile", "", "profile name")
	if err := fs.Parse(args); err != nil {
		return serviceProfileOptions{}, err
	}
	if fs.NArg() > 0 {
		return serviceProfileOptions{}, fmt.Errorf("unexpected %s arguments: %v", command, fs.Args())
	}
	return opts, nil
}

func ensureStartConfigMigrated(paths apppaths.Paths, configPath string, opts startOptions) error {
	raw, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if isRootConfigV2(raw) {
		return nil
	}
	migrateOpts := migrateOptions{
		Home:    paths.RootDir,
		Config:  configPath,
		Profile: paths.Profile,
		Agent:   opts.Agent,
	}
	return configstore.WithConfigFileLock(configPath, func() error {
		_, err := migrateV1ToV2(paths, configPath, migrateOpts)
		return err
	})
}

var envBackedSecretTemplateRE = regexp.MustCompile(`^\$\{[A-Z][A-Z0-9_]{0,127}\}$`)

func materializeEnvSecretForService(ctx context.Context, configPath string, profile string) error {
	if configPath == "" {
		return fmt.Errorf("config path is required")
	}
	return configstore.WithConfigFileLock(configPath, func() error {
		snapshot, err := configstore.Load(configPath, configstore.LoadOptions{Profile: profile})
		if err != nil {
			return err
		}
		root := snapshot.Root
		profileConfig, ok := root.Profiles[snapshot.ProfileName]
		if !ok {
			return fmt.Errorf("profile not found: %s", snapshot.ProfileName)
		}
		cfg := larkcli.AppConfig{
			Accounts: snapshot.Runtime.Accounts,
			Secrets:  snapshot.Runtime.Secrets,
		}
		if !isEnvBackedStartSecret(cfg.Accounts.App.Secret) {
			return nil
		}
		paths, err := apppaths.Resolve(apppaths.Options{RootDir: filepath.Dir(configPath), Profile: snapshot.ProfileName})
		if err != nil {
			return err
		}
		plaintext, err := resolveStartAppSecret(ctx, cfg, paths)
		if err != nil {
			return err
		}
		secretID := secretstore.SecretKeyForApp(cfg.Accounts.App.ID)
		if err := storeStartAppSecret(paths, secretID, plaintext); err != nil {
			return err
		}
		profileConfig.Accounts.App.Secret = larkcli.SecretRef{
			Source:   secretstore.SourceExec,
			Provider: "bridge",
			ID:       secretID,
		}
		if profileConfig.Secrets != nil {
			profileConfig.Secrets = ensureBridgeSecretsConfig(profileConfig.Secrets, paths)
		} else {
			root.Secrets = ensureBridgeSecretsConfig(root.Secrets, paths)
		}
		root.Profiles[snapshot.ProfileName] = profileConfig
		return configstore.SaveRoot(configPath, root)
	})
}

func isEnvBackedStartSecret(secret any) bool {
	switch value := secret.(type) {
	case string:
		return envBackedSecretTemplateRE.MatchString(value)
	case larkcli.SecretRef:
		return value.Source == secretstore.SourceEnv
	case *larkcli.SecretRef:
		return value != nil && value.Source == secretstore.SourceEnv
	case map[string]any:
		source, _ := value["source"].(string)
		return source == secretstore.SourceEnv
	case map[string]string:
		return value["source"] == secretstore.SourceEnv
	default:
		return false
	}
}

func ensureBridgeSecretsConfig(secrets *larkcli.SecretsConfig, paths apppaths.Paths) *larkcli.SecretsConfig {
	if secrets == nil {
		secrets = &larkcli.SecretsConfig{}
	}
	if secrets.Providers == nil {
		secrets.Providers = map[string]larkcli.ProviderConfig{}
	}
	bridgeSecrets := bootstrapSecretsConfig(paths)
	secrets.Providers["bridge"] = bridgeSecrets.Providers["bridge"]
	if secrets.Defaults == nil {
		secrets.Defaults = map[string]string{}
	}
	secrets.Defaults[secretstore.SourceExec] = "bridge"
	return secrets
}

type serviceLockHandler struct{}

func (serviceLockHandler) HandleServiceRuntimeLockConflict(ctx context.Context, meta bridge.ServiceRuntimeLockMeta) (bool, error) {
	app := ""
	if meta.AppID != "" {
		app = " app=" + meta.AppID
	}
	kind := "profile"
	if meta.Kind == string(runtimecoord.LockApp) {
		kind = "app"
	}
	fmt.Fprintf(os.Stderr, "✗ 当前 %s 已有 bridge 进程占用。\n", kind)
	fmt.Fprintf(os.Stderr, "  holder: profile=%s%s agent=%s pid=%d startedAt=%s\n", meta.Profile, app, meta.AgentKind, meta.PID, meta.StartedAt)
	if !processAlive(meta.PID) {
		if err := clearDeadServiceRuntimeLock(meta); err != nil {
			return false, err
		}
		fmt.Fprintf(os.Stdout, "✓ 旧进程 pid %d 已不在运行,重试启动。\n", meta.PID)
		return true, nil
	}
	if !stdioInteractive() {
		return false, fmt.Errorf("当前 %s 已有 bridge 进程占用；非交互模式无法确认停止，请先用 `lark-channel-bridge ps` 查看并用 `lark-channel-bridge kill <bot id>` 停止后重试", kind)
	}
	fmt.Fprint(os.Stdout, "是否停止旧进程并继续启动后台服务? [y/N]: ")
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	normalized := strings.ToLower(strings.TrimSpace(answer))
	if normalized != "y" && normalized != "yes" {
		fmt.Fprintln(os.Stdout, "已取消启动。")
		return false, errServiceStartCancelled
	}
	result, stillAlive, err := stopProcessEntry(ctx, meta.PID, defaultProcessStopTimeout)
	if err != nil {
		return false, err
	}
	if stillAlive {
		return false, fmt.Errorf("process pid=%d is still alive", meta.PID)
	}
	if result == stopProcessKilled {
		fmt.Fprintf(os.Stdout, "✓ 已强制停止 pid %d\n", meta.PID)
	} else {
		fmt.Fprintf(os.Stdout, "✓ 已停止 pid %d\n", meta.PID)
	}
	return true, nil
}

func clearDeadServiceRuntimeLock(meta bridge.ServiceRuntimeLockMeta) error {
	return clearDeadRuntimeLock(runtimecoord.RuntimeLockMeta{
		Kind:      runtimecoord.LockKind(meta.Kind),
		Target:    meta.Target,
		Profile:   meta.Profile,
		AgentKind: runtimecoord.AgentKind(meta.AgentKind),
		AppID:     meta.AppID,
		PID:       meta.PID,
		StartedAt: meta.StartedAt,
	})
}

func stdioInteractive() bool {
	stdin, err := os.Stdin.Stat()
	if err != nil || stdin.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	stdout, err := os.Stdout.Stat()
	return err == nil && stdout.Mode()&os.ModeCharDevice != 0
}

func promptHiddenSecret(stdout io.Writer, prompt string) (string, error) {
	fmt.Fprint(stdout, prompt)
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(stdout)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(password)), nil
}

func clearDeadRuntimeLock(meta runtimecoord.RuntimeLockMeta) error {
	if meta.Target == "" || meta.PID <= 0 || processAlive(meta.PID) {
		return fmt.Errorf("runtime lock holder is still alive or invalid: pid=%d target=%s", meta.PID, meta.Target)
	}
	data, err := os.ReadFile(runtimecoord.RuntimeLockMetaFile(meta.Target))
	if err != nil {
		return err
	}
	var current runtimecoord.RuntimeLockMeta
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	if current.PID != meta.PID || current.StartedAt != meta.StartedAt || current.Target != meta.Target || current.Kind != meta.Kind || current.Profile != meta.Profile || current.AppID != meta.AppID {
		return fmt.Errorf("runtime lock changed while handling stale holder")
	}
	if err := os.Remove(runtimecoord.RuntimeLockMetaFile(meta.Target)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(meta.Target + ".lock"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func bootstrapStartConfig(opts startOptions, paths apppaths.Paths, configPath string) error {
	profile, secrets, err := buildBootstrapProfileConfig(opts, paths)
	if err != nil {
		return err
	}
	root := configstore.RootConfig{
		SchemaVersion: 2,
		ActiveProfile: paths.Profile,
		Preferences:   map[string]any{},
		Secrets:       secrets,
		Migrations:    &configstore.RootMigrations{PermissionDefaultsV1: []string{paths.Profile}},
		Profiles: map[string]configstore.ProfileConfig{
			paths.Profile: profile,
		},
	}
	if err := configstore.SaveRoot(configPath, root); err != nil {
		return err
	}
	return configstore.WriteActiveProfile(paths.RootDir, paths.Profile)
}

func buildBootstrapProfileConfig(opts startOptions, paths apppaths.Paths) (configstore.ProfileConfig, *larkcli.SecretsConfig, error) {
	agentKind, err := bootstrapAgentKind(opts.Agent, paths.Profile)
	if err != nil {
		return configstore.ProfileConfig{}, nil, err
	}
	appID, appSecret, tenant, err := resolveBootstrapAppCredentials(context.Background(), opts)
	if err != nil {
		return configstore.ProfileConfig{}, nil, err
	}
	defaultWorkspace, err := bootstrapWorkspace(opts.Workspace, paths)
	if err != nil {
		return configstore.ProfileConfig{}, nil, err
	}
	normalized, err := permissions.NormalizePermissions(permissions.NormalizeInput{})
	if err != nil {
		return configstore.ProfileConfig{}, nil, err
	}
	sandbox, err := permissions.PermissionsToLegacySandbox(normalized.Permissions)
	if err != nil {
		return configstore.ProfileConfig{}, nil, err
	}
	secretID := secretstore.SecretKeyForApp(appID)
	if err := storeStartAppSecret(paths, secretID, appSecret); err != nil {
		return configstore.ProfileConfig{}, nil, err
	}
	secretRef := larkcli.SecretRef{Source: secretstore.SourceExec, Provider: "bridge", ID: secretID}
	secrets := bootstrapSecretsConfig(paths)
	profile := configstore.ProfileConfig{
		SchemaVersion: 2,
		AgentKind:     agentKind,
		Accounts: larkcli.AccountsConfig{App: larkcli.AppCredentials{
			ID:     appID,
			Secret: secretRef,
			Tenant: tenant,
		}},
		Preferences: map[string]any{},
		Access: configstore.ProfileAccess{
			RequireMentionInGroup: true,
		},
		Workspaces:       configstore.Workspaces{Default: defaultWorkspace},
		Sandbox:          sandbox,
		Permissions:      normalized.Permissions,
		PermissionSource: normalized.Source,
		Attachments:      configstore.DefaultAttachmentConfig(),
		Comments:         map[string]any{},
		LarkCli:          configstore.LarkCliConfig{IdentityPreset: configstore.LarkCliIdentityBotOnly},
	}
	if agentKind == configstore.AgentCodex {
		binary, err := bootstrapCodexBinary()
		if err != nil {
			return configstore.ProfileConfig{}, nil, err
		}
		profile.Codex = &configstore.CodexConfig{
			BinaryPath:       binary,
			InheritCodexHome: true,
			IgnoreRules:      true,
		}
	}
	return profile, secrets, nil
}

func resolveBootstrapAppCredentials(ctx context.Context, opts startOptions) (string, string, larkcli.TenantBrand, error) {
	if opts.AppID == "" {
		if !stdioInteractive() {
			return "", "", "", fmt.Errorf("当前没有配置，非交互模式无法完成扫码创建应用。请先在终端运行 `lark-channel-bridge run` 完成首次初始化，或传入 --app-id 和 --app-secret")
		}
		return runRegistrationWizard(ctx)
	}
	appSecret := opts.AppSecret
	if appSecret == "" {
		if !stdioInteractive() {
			return "", "", "", fmt.Errorf("非交互模式缺少 App Secret: %s。请传入 --app-secret <secret>，或在终端中重新运行命令后按提示输入", opts.AppID)
		}
		answer, err := promptHiddenSecret(os.Stdout, fmt.Sprintf("输入 %s 的 App Secret: ", opts.AppID))
		if err != nil {
			return "", "", "", err
		}
		appSecret = strings.TrimSpace(answer)
	}
	if appSecret == "" {
		return "", "", "", fmt.Errorf("app secret is required")
	}
	tenant, err := bootstrapTenant(opts.Tenant)
	if err != nil {
		return "", "", "", err
	}
	if err := validateBootstrapAppCredentials(ctx, opts.AppID, appSecret, tenant); err != nil {
		return "", "", "", err
	}
	return opts.AppID, appSecret, tenant, nil
}

var bootstrapAppCredentialValidator = validateStartAppCredentials

func validateBootstrapAppCredentials(ctx context.Context, appID string, appSecret string, tenant larkcli.TenantBrand) error {
	result, err := bootstrapAppCredentialValidator(ctx, appID, appSecret, string(tenant))
	if err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("app credentials validation failed: %s", firstNonEmpty(result.Reason, "unknown"))
	}
	if result.BotName != "" {
		fmt.Fprintf(os.Stdout, "✓ 应用凭证校验通过: %s\n", result.BotName)
	} else {
		fmt.Fprintln(os.Stdout, "✓ 应用凭证校验通过")
	}
	return nil
}

func runRegistrationWizard(ctx context.Context) (string, string, larkcli.TenantBrand, error) {
	fmt.Fprintln(os.Stdout, "\n未检测到飞书应用配置，进入扫码创建向导。")
	result, err := registration.RegisterApp(ctx, &registration.Options{
		Source: "lark-channel-bridge",
		OnQRCode: func(info *registration.QRCodeInfo) {
			mins := max(1, (info.ExpireIn+59)/60)
			fmt.Fprintln(os.Stdout, "\n请用飞书/Lark 打开以下链接完成应用创建：")
			fmt.Fprintf(os.Stdout, "%s\n", info.URL)
			fmt.Fprintf(os.Stdout, "链接有效期：约 %d 分钟\n\n", mins)
		},
		OnStatusChange: func(info *registration.StatusChangeInfo) {
			switch info.Status {
			case registration.StatusDomainSwitched:
				fmt.Fprintln(os.Stdout, "识别到国际版租户，已切换到 larksuite.com 域名。")
			case registration.StatusSlowDown:
				fmt.Fprintln(os.Stdout, "轮询速度过快，已自动降速。")
			}
		},
	})
	if err != nil {
		return "", "", "", err
	}
	if result == nil || result.ClientID == "" || result.ClientSecret == "" {
		return "", "", "", fmt.Errorf("registration did not return app credentials")
	}
	tenant := larkcli.TenantFeishu
	if result.UserInfo != nil && result.UserInfo.TenantBrand == string(larkcli.TenantLark) {
		tenant = larkcli.TenantLark
	}
	fmt.Fprintln(os.Stdout, "✓ 应用创建成功")
	fmt.Fprintf(os.Stdout, "  App ID:  %s\n", result.ClientID)
	fmt.Fprintf(os.Stdout, "  Tenant:  %s\n", tenant)
	if result.UserInfo != nil && result.UserInfo.OpenID != "" {
		fmt.Fprintf(os.Stdout, "  Creator: %s (Lark 应用 owner，自动豁免访问控制)\n", result.UserInfo.OpenID)
	} else {
		fmt.Fprintln(os.Stdout, "  未拿到扫码用户 open_id；启动后会通过应用 owner API 解析创建者。")
	}
	return result.ClientID, result.ClientSecret, tenant, nil
}

type detectedLocalAgent struct {
	Kind       configstore.AgentKind
	BinaryPath string
}

func selectFirstRunBootstrapAgent(opts startOptions, rootDir string) (startOptions, apppaths.Paths, error) {
	detected := detectInstalledAgents()
	if len(detected) == 0 {
		return startOptions{}, apppaths.Paths{}, fmt.Errorf("no supported local agent found; install claude or codex first")
	}
	var selected configstore.AgentKind
	if len(detected) == 1 {
		selected = detected[0].Kind
	} else {
		var err error
		selected, err = promptSelectDetectedAgent(detected)
		if err != nil {
			return startOptions{}, apppaths.Paths{}, err
		}
	}
	opts.Agent = string(selected)
	opts.Profile = string(selected)
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: rootDir, Profile: opts.Profile})
	return opts, paths, err
}

func detectInstalledAgents() []detectedLocalAgent {
	candidates := []struct {
		kind configstore.AgentKind
		env  string
		cmd  string
	}{
		{kind: configstore.AgentClaude, env: "LARK_CHANNEL_CLAUDE_BIN", cmd: "claude"},
		{kind: configstore.AgentCodex, env: "LARK_CHANNEL_CODEX_BIN", cmd: "codex"},
	}
	out := make([]detectedLocalAgent, 0, len(candidates))
	for _, candidate := range candidates {
		command := firstNonEmpty(os.Getenv(candidate.env), candidate.cmd)
		path, err := exec.LookPath(command)
		if err != nil {
			continue
		}
		out = append(out, detectedLocalAgent{Kind: candidate.kind, BinaryPath: path})
	}
	return out
}

func promptSelectDetectedAgent(detected []detectedLocalAgent) (configstore.AgentKind, error) {
	if !stdioInteractive() {
		return "", fmt.Errorf("%s", formatAmbiguousAgentSelectionError(detected))
	}
	fmt.Fprintln(os.Stdout, "检测到多个本地 agent，本次要初始化哪一个？")
	for i, agent := range detected {
		fmt.Fprintf(os.Stdout, "  %d. %s (%s)\n", i+1, agent.Kind, agent.BinaryPath)
	}
	fmt.Fprint(os.Stdout, "请选择序号 [1]: ")
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	choice := strings.TrimSpace(answer)
	if choice == "" {
		return detected[0].Kind, nil
	}
	for i, agent := range detected {
		if choice == fmt.Sprint(i+1) || strings.EqualFold(choice, string(agent.Kind)) {
			return agent.Kind, nil
		}
	}
	return "", fmt.Errorf("invalid agent selection: %s", choice)
}

func formatAmbiguousAgentSelectionError(detected []detectedLocalAgent) string {
	lines := []string{"检测到多个本地 agent，请使用 --agent <claude|codex> 指定要初始化哪一个。", "已检测到："}
	for _, agent := range detected {
		lines = append(lines, fmt.Sprintf("  - %s: %s", agent.Kind, agent.BinaryPath))
	}
	return strings.Join(lines, "\n")
}

func bootstrapAgentKind(raw string, profileName string) (configstore.AgentKind, error) {
	switch configstore.AgentKind(raw) {
	case configstore.AgentClaude:
		return configstore.AgentClaude, nil
	case configstore.AgentCodex:
		return configstore.AgentCodex, nil
	case "":
		if profileName == string(configstore.AgentCodex) {
			return configstore.AgentCodex, nil
		}
		return configstore.AgentClaude, nil
	default:
		return "", fmt.Errorf("unsupported agent kind %q", raw)
	}
}

func bootstrapTenant(raw string) (larkcli.TenantBrand, error) {
	switch larkcli.TenantBrand(raw) {
	case "", larkcli.TenantFeishu:
		return larkcli.TenantFeishu, nil
	case larkcli.TenantLark:
		return larkcli.TenantLark, nil
	default:
		return "", fmt.Errorf("unsupported tenant %q", raw)
	}
}

func bootstrapWorkspace(raw string, paths apppaths.Paths) (string, error) {
	if raw != "" {
		result := appworkspace.ResolveWorkingDirectory(raw)
		if !result.OK {
			return "", fmt.Errorf("%s", result.UserVisible)
		}
		return result.CWDRealpath, nil
	}
	if err := os.MkdirAll(paths.DefaultWorkspaceDir, 0o700); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(paths.DefaultWorkspaceDir)
}

func bootstrapCodexBinary() (string, error) {
	command := os.Getenv("LARK_CHANNEL_CODEX_BIN")
	if command == "" {
		command = "codex"
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf("codex binary not found: %s", command)
	}
	return path, nil
}

func bootstrapSecretsConfig(paths apppaths.Paths) *larkcli.SecretsConfig {
	return &larkcli.SecretsConfig{
		Providers: map[string]larkcli.ProviderConfig{
			"bridge": {
				"source":  secretstore.SourceExec,
				"command": larkcli.SecretsGetterWrapperPath(paths.SecretsGetterScript),
				"args":    []string{},
				"env": map[string]string{
					"LARK_CHANNEL_HOME": paths.RootDir,
				},
			},
		},
		Defaults: map[string]string{
			secretstore.SourceExec: "bridge",
		},
	}
}

func runMigrate(args []string, stdout, stderr io.Writer) error {
	opts, err := parseMigrateOptions(args, stderr)
	if err != nil {
		return err
	}
	if opts.Home == "" && opts.Config == "" {
		if err := migrateLegacyPaths(stdout); err != nil {
			return err
		}
	}
	rootDir := opts.Home
	if rootDir == "" && opts.Config != "" {
		rootDir = filepath.Dir(opts.Config)
	}
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: rootDir, Profile: opts.Profile})
	if err != nil {
		return err
	}
	configPath := opts.Config
	if configPath == "" {
		configPath = paths.ConfigFile
	}
	var result migrateResult
	err = configstore.WithConfigFileLock(configPath, func() error {
		var migrateErr error
		result, migrateErr = migrateV1ToV2(paths, configPath, opts)
		return migrateErr
	})
	if err != nil {
		return err
	}
	if result.migrated {
		fmt.Fprintf(stdout, "✓ 已升级 profile 目录结构：%s\n", result.profile)
	} else {
		fmt.Fprintf(stdout, "✓ profile 目录结构已是最新：%s\n", result.profile)
	}
	return nil
}

type migrateResult struct {
	migrated bool
	profile  string
}

func migrateV1ToV2(paths apppaths.Paths, configPath string, opts migrateOptions) (migrateResult, error) {
	rawConfig, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return migrateResult{profile: paths.Profile}, nil
	}
	if err != nil {
		return migrateResult{}, err
	}
	if isRootConfigV2(rawConfig) {
		var probe struct {
			ActiveProfile string `json:"activeProfile"`
		}
		_ = json.Unmarshal(rawConfig, &probe)
		return migrateResult{profile: firstNonEmpty(probe.ActiveProfile, paths.Profile)}, nil
	}
	if active, err := activeLegacyBridgeProcesses(paths); err != nil {
		return migrateResult{}, err
	} else if len(active) > 0 {
		return migrateResult{}, fmt.Errorf("active bridge process blocks v2 migration: %s", formatActiveLegacyProcesses(active))
	}
	loadOptions := configstore.LoadOptions{Profile: paths.Profile}
	if opts.Agent != "" {
		agentKind, err := requestedProfileAgentKind(opts.Agent)
		if err != nil {
			return migrateResult{}, err
		}
		loadOptions.AgentKind = agentKind
	}
	if loadOptions.AgentKind == "" && paths.Profile == string(configstore.AgentCodex) {
		loadOptions.AgentKind = configstore.AgentCodex
	}
	if loadOptions.AgentKind == configstore.AgentCodex {
		binary, err := bootstrapCodexBinary()
		if err != nil {
			return migrateResult{}, err
		}
		loadOptions.Codex = &configstore.CodexConfig{BinaryPath: binary, InheritCodexHome: true, IgnoreRules: true}
	}
	root, err := configstore.NormalizeRootOrLegacy(rawConfig, loadOptions)
	if err != nil {
		return migrateResult{}, err
	}
	if workspace := collectLegacyDefaultWorkspace(paths.RootDir); workspace != "" {
		profile := root.Profiles[paths.Profile]
		profile.Workspaces.Default = workspace
		root.Profiles[paths.Profile] = profile
	}

	moved := []movedPath{}
	if err := os.MkdirAll(paths.ProfileDir, 0o700); err != nil {
		return migrateResult{}, err
	}
	if err := configstore.WriteFileAtomic(configPath+".bak", rawConfig, 0o600); err != nil {
		return migrateResult{}, err
	}
	if err := moveLegacyStateEntries(paths.RootDir, paths.ProfileDir, &moved); err != nil {
		rollbackLegacyMigration(configPath, rawConfig, paths.ActiveProfileFile, moved)
		return migrateResult{}, err
	}
	if err := configstore.SaveRoot(configPath, root); err != nil {
		rollbackLegacyMigration(configPath, rawConfig, paths.ActiveProfileFile, moved)
		return migrateResult{}, err
	}
	if err := configstore.WriteActiveProfile(paths.RootDir, paths.Profile); err != nil {
		rollbackLegacyMigration(configPath, rawConfig, paths.ActiveProfileFile, moved)
		return migrateResult{}, err
	}
	return migrateResult{migrated: true, profile: paths.Profile}, nil
}

func isRootConfigV2(raw []byte) bool {
	var probe struct {
		SchemaVersion int                        `json:"schemaVersion"`
		Profiles      map[string]json.RawMessage `json:"profiles"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.SchemaVersion == 2 && probe.Profiles != nil
}

type activeLegacyProcess struct {
	ID          string `json:"id,omitempty"`
	PID         int    `json:"pid"`
	AppID       string `json:"appId,omitempty"`
	ProfileName string `json:"profileName,omitempty"`
	BotName     string `json:"botName,omitempty"`
}

func activeLegacyBridgeProcesses(paths apppaths.Paths) ([]activeLegacyProcess, error) {
	files := []string{paths.UserRegistryFile, filepath.Join(paths.RootDir, "processes.json")}
	var active []activeLegacyProcess
	seen := map[string]struct{}{}
	for _, file := range files {
		entries, err := readLegacyProcessEntries(file)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.PID <= 0 || entry.PID == os.Getpid() || !processAlive(entry.PID) {
				continue
			}
			key := fmt.Sprintf("%d:%s", entry.PID, entry.ID)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			active = append(active, entry)
		}
	}
	return active, nil
}

func readLegacyProcessEntries(path string) ([]activeLegacyProcess, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var registry struct {
		Entries []activeLegacyProcess `json:"entries"`
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, err
	}
	return registry.Entries, nil
}

func formatActiveLegacyProcesses(processes []activeLegacyProcess) string {
	parts := make([]string, 0, len(processes))
	for _, active := range processes {
		label := "bridge"
		if active.BotName != "" {
			label = "bot " + active.BotName
		} else if active.AppID != "" {
			label = "app " + active.AppID
		}
		id := ""
		if active.ID != "" {
			id = " id=" + active.ID
		}
		profile := ""
		if active.ProfileName != "" {
			profile = " profile=" + active.ProfileName
		}
		parts = append(parts, fmt.Sprintf("%s%s%s pid=%d", label, id, profile, active.PID))
	}
	return strings.Join(parts, ", ")
}

type movedPath struct {
	from string
	to   string
}

func moveLegacyStateEntries(rootDir string, profileDir string, moved *[]movedPath) error {
	for _, name := range []string{"sessions.json", "workspaces.json", "secrets.enc", ".keystore.salt", "media", "logs"} {
		from := filepath.Join(rootDir, name)
		to := filepath.Join(profileDir, name)
		if _, err := os.Stat(from); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if _, err := os.Stat(to); err == nil {
			return fmt.Errorf("profile state already exists: %s", to)
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return err
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
		*moved = append(*moved, movedPath{from: from, to: to})
	}
	return nil
}

func rollbackLegacyMigration(configPath string, rawConfig []byte, activeProfileFile string, moved []movedPath) {
	for i := len(moved) - 1; i >= 0; i-- {
		item := moved[i]
		if _, err := os.Stat(item.to); err != nil {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(item.from), 0o700)
		_ = os.Rename(item.to, item.from)
	}
	_ = configstore.WriteFileAtomic(configPath, rawConfig, 0o600)
	_ = os.Remove(activeProfileFile)
}

func collectLegacyDefaultWorkspace(rootDir string) string {
	data, err := os.ReadFile(filepath.Join(rootDir, "workspaces.json"))
	if err != nil {
		return ""
	}
	var parsed struct {
		Chats map[string]struct {
			CWD string `json:"cwd"`
		} `json:"chats"`
		Named map[string]string `json:"named"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ""
	}
	var candidates []string
	for _, chat := range parsed.Chats {
		if chat.CWD != "" {
			candidates = append(candidates, chat.CWD)
		}
	}
	for _, cwd := range parsed.Named {
		if cwd != "" {
			candidates = append(candidates, cwd)
		}
	}
	sort.Strings(candidates)
	for _, candidate := range candidates {
		resolved := appworkspace.ResolveWorkingDirectory(candidate)
		if resolved.OK {
			return resolved.CWDRealpath
		}
	}
	return ""
}

func migrateLegacyPaths(stdout io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	legacyConfig := filepath.Join(firstNonEmpty(os.Getenv("XDG_CONFIG_HOME"), filepath.Join(home, ".config")), "lark-channel-bridge")
	legacyCache := filepath.Join(firstNonEmpty(os.Getenv("XDG_CACHE_HOME"), filepath.Join(home, ".cache")), "lark-channel-bridge")
	current, err := apppaths.Resolve(apppaths.Options{})
	if err != nil {
		return err
	}
	if moved, err := moveDirContents(legacyConfig, current.RootDir); err != nil {
		return err
	} else if moved {
		fmt.Fprintf(stdout, "✓ 已搬迁配置：%s → %s\n", legacyConfig, current.RootDir)
	}
	legacyMedia := filepath.Join(legacyCache, "media")
	if moved, err := moveDirContents(legacyMedia, current.MediaDir); err != nil {
		return err
	} else if moved {
		_ = removeDirIfEmpty(legacyMedia)
	}
	if moved, err := moveDirContents(legacyCache, current.RootDir); err != nil {
		return err
	} else if moved {
		fmt.Fprintf(stdout, "✓ 已搬迁缓存：%s → %s\n", legacyCache, current.RootDir)
	}
	_ = removeDirIfEmpty(legacyConfig)
	_ = removeDirIfEmpty(legacyCache)
	return nil
}

func moveDirContents(from string, to string) (bool, error) {
	entries, err := os.ReadDir(from)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(entries) == 0 {
		return false, nil
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return false, err
	}
	moved := false
	for _, entry := range entries {
		src := filepath.Join(from, entry.Name())
		dst := filepath.Join(to, entry.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		} else if err != nil && !os.IsNotExist(err) {
			return moved, err
		}
		if err := os.Rename(src, dst); err != nil {
			return moved, err
		}
		moved = true
	}
	return moved, nil
}

func removeDirIfEmpty(path string) error {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return os.Remove(path)
	}
	return nil
}

func runProfile(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printProfileHelp(stdout)
		return 0
	}
	switch args[0] {
	case "create":
		if err := runProfileCreate(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "profile create failed: %v\n", err)
			return 1
		}
		return 0
	case "list":
		if err := runProfileList(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "profile list failed: %v\n", err)
			return 1
		}
		return 0
	case "use":
		if err := runProfileUse(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "profile use failed: %v\n", err)
			return 1
		}
		return 0
	case "export":
		if err := runProfileExport(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "profile export failed: %v\n", err)
			return 1
		}
		return 0
	case "remove":
		if err := runProfileRemove(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "profile remove failed: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "unknown profile command: %s\n\n", args[0])
		printProfileHelp(stderr)
		return 2
	}
}

func runProfileCreate(args []string, stdout, stderr io.Writer) error {
	opts, err := parseProfileCreateOptions(args, stderr)
	if err != nil {
		return err
	}
	if opts.Profile == "" {
		return fmt.Errorf("usage: profile create [--home DIR] [--agent codex|claude] [--workspace DIR] [--app-id ID [--app-secret SECRET]] <name>")
	}
	rootPaths, err := apppaths.Resolve(apppaths.Options{RootDir: opts.Home})
	if err != nil {
		return err
	}
	return configstore.WithConfigFileLock(rootPaths.ConfigFile, func() error {
		rootPaths, root, ok, err := loadProfileRoot(rootPaths.RootDir)
		if err != nil {
			return err
		}
		profilePaths, err := apppaths.Resolve(apppaths.Options{RootDir: rootPaths.RootDir, Profile: opts.Profile})
		if err != nil {
			return err
		}
		startOpts := startOptions{
			Home:      rootPaths.RootDir,
			Profile:   opts.Profile,
			Agent:     opts.Agent,
			AppID:     opts.AppID,
			AppSecret: opts.AppSecret,
			Tenant:    opts.Tenant,
			Workspace: opts.Workspace,
		}
		if !ok {
			if err := bootstrapStartConfig(startOpts, profilePaths, rootPaths.ConfigFile); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "已创建 profile: %s\n", opts.Profile)
			return nil
		}
		if existing, exists := root.Profiles[opts.Profile]; exists {
			requested, err := requestedProfileAgentKind(opts.Agent)
			if err != nil {
				return err
			}
			if requested != "" && existing.AgentKind != requested {
				return fmt.Errorf(
					"profile %s already exists with agentKind %s, but profile create requested --agent %s. Profile names are labels; use the existing %s profile, choose another name, or remove profile %s before creating a %s profile",
					opts.Profile,
					existing.AgentKind,
					requested,
					existing.AgentKind,
					opts.Profile,
					requested,
				)
			}
			return fmt.Errorf("profile already exists: %s", opts.Profile)
		}
		profile, secrets, err := buildBootstrapProfileConfig(startOpts, profilePaths)
		if err != nil {
			return err
		}
		if root.Profiles == nil {
			root.Profiles = map[string]configstore.ProfileConfig{}
		}
		if root.Secrets == nil {
			root.Secrets = secrets
		}
		root.Profiles[opts.Profile] = profile
		markPermissionDefaultsMigration(&root, opts.Profile)
		if err := configstore.SaveRoot(rootPaths.ConfigFile, root); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "已创建 profile: %s\n", opts.Profile)
		return nil
	})
}

func runProfileList(args []string, stdout, stderr io.Writer) error {
	opts, err := parseProfileOptions("profile list", args, stderr)
	if err != nil {
		return err
	}
	paths, root, ok, err := loadProfileRoot(opts.Home)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(stdout, "暂无 profile。")
		return nil
	}
	running, err := listRuntimeProcesses(paths.RootDir)
	if err != nil {
		return err
	}
	names := sortedProfileNames(root.Profiles)
	rows := make([][4]string, 0, len(names)+1)
	rows = append(rows, [4]string{"ACTIVE", "PROFILE", "AGENT", "STATUS"})
	for _, name := range names {
		active := ""
		if name == root.ActiveProfile {
			active = "*"
		}
		rows = append(rows, [4]string{active, name, string(root.Profiles[name].AgentKind), profileStatus(name, running)})
	}
	widths := profileListWidths(rows)
	for _, row := range rows {
		fmt.Fprintf(stdout, "%-*s  %-*s  %-*s  %s\n", widths[0], row[0], widths[1], row[1], widths[2], row[2], row[3])
	}
	return nil
}

func runProfileUse(args []string, stdout, stderr io.Writer) error {
	opts, err := parseProfileOptions("profile use", args, stderr)
	if err != nil {
		return err
	}
	if opts.Profile == "" {
		return fmt.Errorf("usage: profile use [--home DIR] <name>")
	}
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: opts.Home})
	if err != nil {
		return err
	}
	return configstore.WithConfigFileLock(paths.ConfigFile, func() error {
		paths, root, ok, err := loadProfileRoot(paths.RootDir)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("config not initialized")
		}
		if _, ok := root.Profiles[opts.Profile]; !ok {
			return fmt.Errorf("profile not found: %s", opts.Profile)
		}
		root.ActiveProfile = opts.Profile
		if err := configstore.SaveRoot(paths.ConfigFile, root); err != nil {
			return err
		}
		if err := configstore.WriteActiveProfile(paths.RootDir, opts.Profile); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "已切换到 profile: %s\n", opts.Profile)
		return nil
	})
}

func runProfileRemove(args []string, stdout, stderr io.Writer) error {
	opts, err := parseProfileRemoveOptions(args, stderr)
	if err != nil {
		return err
	}
	if opts.Profile == "" {
		return fmt.Errorf("usage: profile remove [--home DIR] [--purge --yes] <name>")
	}
	if opts.Purge && !opts.Yes {
		return fmt.Errorf("profile remove --purge requires --yes")
	}
	rootPaths, err := apppaths.Resolve(apppaths.Options{RootDir: opts.Home})
	if err != nil {
		return err
	}
	return configstore.WithConfigFileLock(rootPaths.ConfigFile, func() error {
		rootPaths, root, ok, err := loadProfileRoot(rootPaths.RootDir)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("config not initialized")
		}
		profile, exists := root.Profiles[opts.Profile]
		if !exists {
			return fmt.Errorf("profile not found: %s", opts.Profile)
		}
		if root.ActiveProfile != "" {
			if _, activeExists := root.Profiles[root.ActiveProfile]; !activeExists {
				return fmt.Errorf("active profile not found: %s; run profile use <name> to repair", root.ActiveProfile)
			}
		}
		lock, err := acquireRemoveProfileLock(rootPaths.RootDir, opts.Profile, profile.AgentKind)
		if err != nil {
			return err
		}
		defer func() { _ = lock.Release() }()

		next := root
		next.Profiles = cloneProfileMap(root.Profiles)
		delete(next.Profiles, opts.Profile)
		if root.ActiveProfile == opts.Profile {
			next.ActiveProfile = firstProfileName(next.Profiles)
		}
		result, err := moveRemovedProfileState(rootPaths.RootDir, opts.Profile, opts.Purge, time.Now())
		if err != nil {
			return err
		}
		restore := func(cause error) error {
			if restoreErr := restoreRemovedProfile(rootPaths, root, result); restoreErr != nil {
				return fmt.Errorf(
					"profile remove failed after moving %s; state is at %s. restore failed: %v. root config error: %v",
					opts.Profile,
					result.archivedTo,
					restoreErr,
					cause,
				)
			}
			return cause
		}
		if len(next.Profiles) == 0 {
			if err := os.Remove(rootPaths.ConfigFile); err != nil && !os.IsNotExist(err) {
				return restore(err)
			}
			if err := os.Remove(rootPaths.ActiveProfileFile); err != nil && !os.IsNotExist(err) {
				return restore(err)
			}
		} else {
			if err := configstore.SaveRoot(rootPaths.ConfigFile, next); err != nil {
				return restore(err)
			}
			if err := configstore.WriteActiveProfile(rootPaths.RootDir, next.ActiveProfile); err != nil {
				return restore(err)
			}
		}
		if result.cleanup != nil {
			if err := result.cleanup(); err != nil {
				return err
			}
		}
		if opts.Purge {
			fmt.Fprintf(stdout, "已永久删除 profile: %s\n", opts.Profile)
			return nil
		}
		fmt.Fprintf(stdout, "已归档 profile: %s -> %s\n", opts.Profile, result.archivedTo)
		return nil
	})
}

func restoreRemovedProfile(paths apppaths.Paths, root configstore.RootConfig, removed removedProfileState) error {
	if removed.restore != nil {
		if err := removed.restore(); err != nil {
			return err
		}
	}
	if err := configstore.SaveRoot(paths.ConfigFile, root); err != nil {
		return err
	}
	if root.ActiveProfile != "" {
		if err := configstore.WriteActiveProfile(paths.RootDir, root.ActiveProfile); err != nil {
			return err
		}
	} else if err := os.Remove(paths.ActiveProfileFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func runProfileExport(args []string, stdout, stderr io.Writer) error {
	opts, err := parseProfileOptions("profile export", args, stderr)
	if err != nil {
		return err
	}
	if opts.Profile == "" {
		return fmt.Errorf("usage: profile export [--home DIR] [--output FILE] [--force] [--include-secrets --yes] <name>")
	}
	if opts.IncludeSecrets && !opts.Yes {
		return fmt.Errorf("profile export --include-secrets requires --yes")
	}
	rootPaths, root, ok, err := loadProfileRoot(opts.Home)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("config not initialized")
	}
	profile, ok := root.Profiles[opts.Profile]
	if !ok {
		return fmt.Errorf("profile not found: %s", opts.Profile)
	}
	if opts.IncludeSecrets {
		profilePaths, err := apppaths.Resolve(apppaths.Options{RootDir: rootPaths.RootDir, Profile: opts.Profile})
		if err != nil {
			return err
		}
		secrets := profile.Secrets
		if secrets == nil {
			secrets = root.Secrets
		}
		plaintext, err := resolveStartAppSecret(context.Background(), larkcli.AppConfig{
			Accounts: profile.Accounts,
			Secrets:  secrets,
		}, profilePaths)
		if err != nil {
			return err
		}
		profile.Accounts.App.Secret = plaintext
	} else {
		profile.Secrets = nil
		profile.Accounts.App.Secret = "[REDACTED]"
	}
	exported := configstore.RootConfig{
		SchemaVersion: 2,
		ActiveProfile: opts.Profile,
		Preferences:   map[string]any{},
		Profiles: map[string]configstore.ProfileConfig{
			opts.Profile: profile,
		},
	}
	if opts.IncludeSecrets && root.Secrets != nil {
		exported.Secrets = root.Secrets
	}
	if root.Migrations != nil {
		for _, name := range root.Migrations.PermissionDefaultsV1 {
			if name == opts.Profile {
				exported.Migrations = &configstore.RootMigrations{PermissionDefaultsV1: []string{opts.Profile}}
				break
			}
		}
	}
	body, err := configstore.FormatRootConfig(exported)
	if err != nil {
		return err
	}
	if opts.Output == "" {
		_, err := stdout.Write(body)
		return err
	}
	if _, err := os.Stat(opts.Output); err == nil && !opts.Force {
		return fmt.Errorf("output already exists; use --force")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(opts.Output), 0o700); err != nil {
		return err
	}
	if err := configstore.WriteFileAtomic(opts.Output, body, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "已导出 profile: %s -> %s\n", opts.Profile, opts.Output)
	return nil
}

func runSecrets(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printSecretsHelp(stdout)
		return 0
	}
	switch args[0] {
	case "get":
		if err := runSecretsGet(args[1:], stdin, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "secrets get failed: %v\n", err)
			return 2
		}
		return 0
	case "set":
		if err := runSecretsSet(args[1:], stdin, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "secrets set failed: %v\n", err)
			return 1
		}
		return 0
	case "list":
		if err := runSecretsList(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "secrets list failed: %v\n", err)
			return 1
		}
		return 0
	case "remove":
		if err := runSecretsRemove(args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "secrets remove failed: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "unknown secrets command: %s\n\n", args[0])
		printSecretsHelp(stderr)
		return 2
	}
}

func runSecretsGet(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	opts, err := parseSecretsOptions(args, stderr)
	if err != nil {
		return err
	}
	profile := opts.Profile
	if profile == "" {
		profile = os.Getenv("LARK_CHANNEL_PROFILE")
	}
	secretPaths, err := secretLookupPaths(opts.Home, profile)
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	var req execSecretRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return fmt.Errorf("invalid stdin JSON: %w", err)
		}
	}
	resp := execSecretResponse{
		ProtocolVersion: 1,
		Values:          map[string]string{},
	}
	for _, id := range req.IDs {
		value, ok, err := lookupSecret(id, secretPaths, stderr)
		if err != nil {
			if resp.Errors == nil {
				resp.Errors = map[string]execSecretError{}
			}
			resp.Errors[id] = execSecretError{Message: err.Error()}
			continue
		}
		if !ok {
			if resp.Errors == nil {
				resp.Errors = map[string]execSecretError{}
			}
			resp.Errors[id] = execSecretError{Message: "not found"}
			continue
		}
		resp.Values[id] = value
	}
	return json.NewEncoder(stdout).Encode(resp)
}

func runSecretsSet(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	opts, err := parseSecretsManageOptions("secrets set", args, stderr)
	if err != nil {
		return err
	}
	if opts.AppID == "" {
		return fmt.Errorf("usage: secrets set [--home DIR] [--profile NAME] --app-id ID [--value SECRET]")
	}
	value := opts.Value
	if value == "" {
		if file, ok := stdin.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
			value, err = promptHiddenSecret(stdout, fmt.Sprintf("输入 %s 的 App Secret: ", opts.AppID))
			if err != nil {
				return err
			}
		} else {
			raw, err := io.ReadAll(stdin)
			if err != nil {
				return err
			}
			value = strings.TrimRight(string(raw), "\r\n")
		}
	}
	if value == "" {
		return fmt.Errorf("secret value is empty; pass --value or pipe the secret on stdin")
	}
	paths, err := resolveSecretProfilePaths(opts.Home, opts.Profile)
	if err != nil {
		return err
	}
	store, err := newKeystoreForPaths(paths)
	if err != nil {
		return err
	}
	id := secretstore.SecretKeyForApp(opts.AppID)
	if err := store.SetSecret(id, value); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "✓ 已加密存储 %s\n", id)
	return nil
}

func runSecretsList(args []string, stdout, stderr io.Writer) error {
	opts, err := parseSecretsManageOptions("secrets list", args, stderr)
	if err != nil {
		return err
	}
	paths, err := resolveSecretProfilePaths(opts.Home, opts.Profile)
	if err != nil {
		return err
	}
	store, err := newKeystoreForPaths(paths)
	if err != nil {
		return err
	}
	ids, err := store.ListSecretIDs()
	if err != nil {
		return err
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		fmt.Fprintln(stdout, "当前没有加密存储的 secret。")
		return nil
	}
	fmt.Fprintf(stdout, "# 当前共 %d 个 secret 在加密存储里\n\n", len(ids))
	for _, id := range ids {
		fmt.Fprintf(stdout, "  - %s\n", id)
	}
	return nil
}

func runSecretsRemove(args []string, stdout, stderr io.Writer) error {
	opts, err := parseSecretsManageOptions("secrets remove", args, stderr)
	if err != nil {
		return err
	}
	if opts.AppID == "" {
		return fmt.Errorf("usage: secrets remove [--home DIR] [--profile NAME] --app-id ID")
	}
	paths, err := resolveSecretProfilePaths(opts.Home, opts.Profile)
	if err != nil {
		return err
	}
	store, err := newKeystoreForPaths(paths)
	if err != nil {
		return err
	}
	id := secretstore.SecretKeyForApp(opts.AppID)
	removed, err := store.RemoveSecret(id)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("secret not found: %s", id)
	}
	fmt.Fprintf(stdout, "✓ 已删除 %s\n", id)
	return nil
}

func secretLookupPaths(rootDir string, profile string) ([]apppaths.Paths, error) {
	if profile != "" {
		paths, err := apppaths.Resolve(apppaths.Options{RootDir: rootDir, Profile: profile})
		if err != nil {
			return nil, err
		}
		return []apppaths.Paths{paths}, nil
	}
	rootPaths, root, ok, err := loadProfileRoot(rootDir)
	if err != nil {
		return nil, err
	}
	if ok {
		active := root.ActiveProfile
		if active == "" {
			active = rootPaths.Profile
		}
		if _, exists := root.Profiles[active]; !exists {
			return nil, fmt.Errorf("active profile not found: %s", active)
		}
		names := sortedProfileNames(root.Profiles)
		ordered := make([]string, 0, len(names))
		ordered = append(ordered, active)
		for _, name := range names {
			if name != active {
				ordered = append(ordered, name)
			}
		}
		paths := make([]apppaths.Paths, 0, len(ordered))
		for _, name := range ordered {
			profilePaths, err := apppaths.Resolve(apppaths.Options{RootDir: rootPaths.RootDir, Profile: name})
			if err != nil {
				return nil, err
			}
			paths = append(paths, profilePaths)
		}
		return paths, nil
	}
	entries, err := os.ReadDir(filepath.Join(rootPaths.RootDir, "profiles"))
	if err != nil {
		if os.IsNotExist(err) {
			return []apppaths.Paths{rootPaths}, nil
		}
		return nil, err
	}
	paths := make([]apppaths.Paths, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		profilePaths, err := apppaths.Resolve(apppaths.Options{RootDir: rootPaths.RootDir, Profile: entry.Name()})
		if err != nil {
			continue
		}
		paths = append(paths, profilePaths)
	}
	if len(paths) == 0 {
		paths = append(paths, rootPaths)
	}
	return paths, nil
}

func lookupSecret(id string, paths []apppaths.Paths, stderr io.Writer) (string, bool, error) {
	type match struct {
		profile string
		value   string
	}
	var matches []match
	for _, p := range paths {
		store, err := newKeystoreForPaths(p)
		if err != nil {
			return "", false, err
		}
		value, ok, err := store.GetSecret(id)
		if err != nil {
			return "", false, err
		}
		if ok {
			matches = append(matches, match{profile: p.Profile, value: value})
		}
	}
	if len(matches) == 0 {
		return "", false, nil
	}
	if len(matches) > 1 {
		fmt.Fprintf(stderr, "secrets get: secret %s exists in multiple profiles; using %s\n", id, matches[0].profile)
	}
	return matches[0].value, true, nil
}

func newKeystoreForPaths(paths apppaths.Paths) (*secretstore.Keystore, error) {
	return secretstore.NewKeystore(secretstore.KeystoreOptions{
		Paths: secretstore.KeystorePaths{
			SecretsFile:      paths.SecretsFile,
			KeystoreSaltFile: paths.KeystoreSaltFile,
		},
	})
}

func parseSecretsOptions(args []string, stderr io.Writer) (secretsOptions, error) {
	var opts secretsOptions
	fs := flag.NewFlagSet("secrets get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Home, "home", "", "bridge home directory")
	fs.StringVar(&opts.Profile, "profile", "", "profile name")
	if err := fs.Parse(args); err != nil {
		return secretsOptions{}, err
	}
	if fs.NArg() > 0 {
		return secretsOptions{}, fmt.Errorf("unexpected secrets get arguments: %v", fs.Args())
	}
	return opts, nil
}

func parseSecretsManageOptions(name string, args []string, stderr io.Writer) (secretsOptions, error) {
	var opts secretsOptions
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Home, "home", "", "bridge home directory")
	fs.StringVar(&opts.Profile, "profile", "", "profile name")
	fs.StringVar(&opts.AppID, "app-id", "", "app id")
	fs.StringVar(&opts.Value, "value", "", "secret value")
	if err := fs.Parse(args); err != nil {
		return secretsOptions{}, err
	}
	if fs.NArg() > 0 {
		return secretsOptions{}, fmt.Errorf("unexpected %s arguments: %v", name, fs.Args())
	}
	return opts, nil
}

func parseMigrateOptions(args []string, stderr io.Writer) (migrateOptions, error) {
	var opts migrateOptions
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Home, "home", "", "bridge home directory")
	fs.StringVar(&opts.Config, "config", "", "config.json path")
	fs.StringVar(&opts.Profile, "profile", "", "target profile name")
	fs.StringVar(&opts.Agent, "agent", "", "agent kind: codex or claude")
	if err := fs.Parse(args); err != nil {
		return migrateOptions{}, err
	}
	if fs.NArg() > 0 {
		return migrateOptions{}, fmt.Errorf("unexpected migrate arguments: %v", fs.Args())
	}
	return opts, nil
}

func parseProfileCreateOptions(args []string, stderr io.Writer) (profileCreateOptions, error) {
	var opts profileCreateOptions
	fs := flag.NewFlagSet("profile create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Home, "home", "", "bridge home directory")
	fs.StringVar(&opts.Agent, "agent", "", "agent kind: codex or claude")
	fs.StringVar(&opts.Workspace, "workspace", "", "default workspace")
	fs.StringVar(&opts.AppID, "app-id", "", "app id")
	fs.StringVar(&opts.AppSecret, "app-secret", "", "app secret")
	fs.StringVar(&opts.Tenant, "tenant", "", "tenant: feishu or lark")
	if err := fs.Parse(args); err != nil {
		return profileCreateOptions{}, err
	}
	if fs.NArg() > 1 {
		return profileCreateOptions{}, fmt.Errorf("unexpected profile create arguments: %v", fs.Args()[1:])
	}
	if fs.NArg() == 1 {
		opts.Profile = fs.Arg(0)
	}
	return opts, nil
}

func parseProfileRemoveOptions(args []string, stderr io.Writer) (profileRemoveOptions, error) {
	var opts profileRemoveOptions
	fs := flag.NewFlagSet("profile remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Home, "home", "", "bridge home directory")
	fs.BoolVar(&opts.Purge, "purge", false, "permanently delete archived profile state")
	fs.BoolVar(&opts.Yes, "yes", false, "confirm unsafe operation")
	if err := fs.Parse(args); err != nil {
		return profileRemoveOptions{}, err
	}
	if fs.NArg() > 1 {
		return profileRemoveOptions{}, fmt.Errorf("unexpected profile remove arguments: %v", fs.Args()[1:])
	}
	if fs.NArg() == 1 {
		opts.Profile = fs.Arg(0)
	}
	return opts, nil
}

func parseProfileOptions(name string, args []string, stderr io.Writer) (profileOptions, error) {
	var opts profileOptions
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Home, "home", "", "bridge home directory")
	fs.StringVar(&opts.Output, "output", "", "output file")
	fs.BoolVar(&opts.Force, "force", false, "overwrite output file")
	fs.BoolVar(&opts.IncludeSecrets, "include-secrets", false, "include plaintext app secrets in profile export")
	fs.BoolVar(&opts.Yes, "yes", false, "confirm unsafe operation")
	if err := fs.Parse(args); err != nil {
		return profileOptions{}, err
	}
	if fs.NArg() > 1 {
		return profileOptions{}, fmt.Errorf("unexpected %s arguments: %v", name, fs.Args()[1:])
	}
	if fs.NArg() == 1 {
		opts.Profile = fs.Arg(0)
	}
	return opts, nil
}

func requestedProfileAgentKind(raw string) (configstore.AgentKind, error) {
	switch configstore.AgentKind(raw) {
	case "":
		return "", nil
	case configstore.AgentClaude:
		return configstore.AgentClaude, nil
	case configstore.AgentCodex:
		return configstore.AgentCodex, nil
	default:
		return "", fmt.Errorf("unsupported agent: %s", raw)
	}
}

func loadProfileRoot(rootDir string) (apppaths.Paths, configstore.RootConfig, bool, error) {
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: rootDir})
	if err != nil {
		return apppaths.Paths{}, configstore.RootConfig{}, false, err
	}
	data, err := os.ReadFile(paths.ConfigFile)
	if os.IsNotExist(err) {
		return paths, configstore.RootConfig{}, false, nil
	}
	if err != nil {
		return apppaths.Paths{}, configstore.RootConfig{}, false, err
	}
	root, err := configstore.NormalizeRootOrLegacy(data, configstore.LoadOptions{})
	if err != nil {
		return apppaths.Paths{}, configstore.RootConfig{}, false, err
	}
	if active := readActiveProfileFile(paths.ActiveProfileFile); active != "" {
		root.ActiveProfile = active
	}
	return paths, root, true, nil
}

func readActiveProfileFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func sortedProfileNames(profiles map[string]configstore.ProfileConfig) []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func profileStatus(name string, entries []runtimecoord.ProcessEntry) string {
	var holders []string
	for _, entry := range entries {
		if entry.ProfileName == name {
			holders = append(holders, fmt.Sprintf("pid=%d agent=%s", entry.PID, entry.AgentKind))
		}
	}
	if len(holders) == 0 {
		return "-"
	}
	return strings.Join(holders, ", ")
}

func profileListWidths(rows [][4]string) [3]int {
	widths := [3]int{}
	for _, row := range rows {
		for i := 0; i < 3; i++ {
			if len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}
	return widths
}

func markPermissionDefaultsMigration(root *configstore.RootConfig, profile string) {
	if root.Migrations == nil {
		root.Migrations = &configstore.RootMigrations{}
	}
	for _, existing := range root.Migrations.PermissionDefaultsV1 {
		if existing == profile {
			return
		}
	}
	root.Migrations.PermissionDefaultsV1 = append(root.Migrations.PermissionDefaultsV1, profile)
	sort.Strings(root.Migrations.PermissionDefaultsV1)
}

func cloneProfileMap(input map[string]configstore.ProfileConfig) map[string]configstore.ProfileConfig {
	out := make(map[string]configstore.ProfileConfig, len(input))
	for name, profile := range input {
		out[name] = profile
	}
	return out
}

func firstProfileName(profiles map[string]configstore.ProfileConfig) string {
	names := sortedProfileNames(profiles)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func acquireRemoveProfileLock(rootDir string, profileName string, agentKind configstore.AgentKind) (*runtimecoord.AcquiredLock, error) {
	coord, err := runtimecoord.New(runtimecoord.Options{
		RootDir:   rootDir,
		Profile:   profileName,
		AgentKind: runtimecoord.AgentKind(agentKind),
	})
	if err != nil {
		return nil, err
	}
	lock, err := coord.AcquireProfileLock()
	if err == nil {
		return lock, nil
	}
	var conflict *runtimecoord.RuntimeLockConflictError
	if errors.As(err, &conflict) {
		holder := ""
		if conflict.Meta != nil {
			holder = fmt.Sprintf(" pid=%d", conflict.Meta.PID)
		}
		return nil, fmt.Errorf("profile is locked/running: %s%s", profileName, holder)
	}
	return nil, err
}

type removedProfileState struct {
	archivedTo string
	restore    func() error
	cleanup    func() error
}

func moveRemovedProfileState(rootDir string, profileName string, purge bool, now time.Time) (removedProfileState, error) {
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: rootDir, Profile: profileName})
	if err != nil {
		return removedProfileState{}, err
	}
	if purge {
		if _, err := os.Stat(paths.ProfileDir); os.IsNotExist(err) {
			return removedProfileState{}, nil
		} else if err != nil {
			return removedProfileState{}, err
		}
	}
	trashDir := filepath.Join(rootDir, ".trash")
	if err := os.MkdirAll(trashDir, 0o700); err != nil {
		return removedProfileState{}, err
	}
	archivedTo, err := nextArchivePath(trashDir, profileName, now)
	if err != nil {
		return removedProfileState{}, err
	}
	if err := os.Rename(paths.ProfileDir, archivedTo); err != nil {
		return removedProfileState{}, err
	}
	result := removedProfileState{
		archivedTo: archivedTo,
		restore: func() error {
			return os.Rename(archivedTo, paths.ProfileDir)
		},
	}
	if purge {
		result.cleanup = func() error {
			if err := os.RemoveAll(archivedTo); err != nil {
				return err
			}
			_ = os.Remove(trashDir)
			return nil
		}
	}
	return result, nil
}

func nextArchivePath(trashDir string, profileName string, now time.Time) (string, error) {
	base := filepath.Join(trashDir, fmt.Sprintf("%s-%s", profileName, archiveTimestamp(now)))
	for suffix := 0; ; suffix++ {
		candidate := base
		if suffix > 0 {
			candidate = fmt.Sprintf("%s-%d", base, suffix)
		}
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
}

func archiveTimestamp(now time.Time) string {
	return now.UTC().Format("20060102T150405Z")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func resolveSecretProfilePaths(rootDir string, profile string) (apppaths.Paths, error) {
	rootPaths, root, ok, err := loadProfileRoot(rootDir)
	if err != nil {
		return apppaths.Paths{}, err
	}
	selected := profile
	if selected == "" {
		selected = root.ActiveProfile
	}
	if selected == "" {
		selected = rootPaths.Profile
	}
	if ok {
		if _, exists := root.Profiles[selected]; !exists {
			return apppaths.Paths{}, fmt.Errorf("profile not found: %s", selected)
		}
	}
	return apppaths.Resolve(apppaths.Options{RootDir: rootPaths.RootDir, Profile: selected})
}

func runPS(args []string, stdout, stderr io.Writer) error {
	opts, err := parsePSOptions(args, stderr)
	if err != nil {
		return err
	}
	entries, err := listRuntimeProcesses(opts.Home)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "No bridge processes are running.")
		return nil
	}
	fmt.Fprintln(stdout, "ID\tPID\tPROFILE\tAGENT\tAPP\tBOT\tSTARTED")
	for _, entry := range entries {
		fmt.Fprintf(stdout, "%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
			entry.ID,
			entry.PID,
			entry.ProfileName,
			entry.AgentKind,
			entry.AppID,
			entry.BotName,
			entry.StartedAt,
		)
	}
	return nil
}

func parsePSOptions(args []string, stderr io.Writer) (psOptions, error) {
	var opts psOptions
	fs := flag.NewFlagSet("ps", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Home, "home", "", "bridge home directory")
	if err := fs.Parse(args); err != nil {
		return psOptions{}, err
	}
	if fs.NArg() > 0 {
		return psOptions{}, fmt.Errorf("unexpected ps arguments: %v", fs.Args())
	}
	return opts, nil
}

func runKill(args []string, stdout, stderr io.Writer) error {
	opts, err := parseKillOptions(args, stderr)
	if err != nil {
		return err
	}
	entries, err := listRuntimeProcesses(opts.Home)
	if err != nil {
		return err
	}
	entry, ok := findRuntimeProcess(entries, opts.Target)
	if !ok {
		return fmt.Errorf("process not found: %s", opts.Target)
	}
	result, stillAlive, err := stopProcessEntry(context.Background(), entry.PID, defaultProcessStopTimeout)
	if err != nil {
		return err
	}
	if stillAlive {
		return fmt.Errorf("process %s pid=%d is still alive", entry.ID, entry.PID)
	}
	switch result {
	case stopProcessKilled:
		fmt.Fprintf(stdout, "force-killed %s pid=%d\n", entry.ID, entry.PID)
	default:
		fmt.Fprintf(stdout, "terminated %s pid=%d\n", entry.ID, entry.PID)
	}
	return nil
}

func parseKillOptions(args []string, stderr io.Writer) (killOptions, error) {
	var opts killOptions
	fs := flag.NewFlagSet("kill", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.Home, "home", "", "bridge home directory")
	if err := fs.Parse(args); err != nil {
		return killOptions{}, err
	}
	if fs.NArg() != 1 {
		return killOptions{}, fmt.Errorf("usage: kill [--home DIR] <id|pid>")
	}
	opts.Target = fs.Arg(0)
	return opts, nil
}

func listRuntimeProcesses(root string) ([]runtimecoord.ProcessEntry, error) {
	coord, err := runtimecoord.New(runtimecoord.Options{RootDir: root})
	if err != nil {
		return nil, err
	}
	return coord.ReadAndPruneProcesses()
}

func findRuntimeProcess(entries []runtimecoord.ProcessEntry, target string) (runtimecoord.ProcessEntry, bool) {
	for _, entry := range entries {
		if entry.ID == target || fmt.Sprint(entry.PID) == target {
			return entry, true
		}
	}
	index, err := strconv.Atoi(target)
	if err == nil && index >= 1 && index <= len(entries) {
		return entries[index-1], true
	}
	return runtimecoord.ProcessEntry{}, false
}

func buildStartBridge(ctx context.Context, opts startOptions) (*bridge.Bridge, string, error) {
	home := opts.Home
	if home == "" && opts.Config != "" {
		home = filepath.Dir(opts.Config)
	}
	profile := opts.Profile
	if profile == "" && opts.Agent != "" {
		profile = opts.Agent
	}
	initialPaths, err := apppaths.Resolve(apppaths.Options{RootDir: home, Profile: profile})
	if err != nil {
		return nil, "", err
	}
	configPath := opts.Config
	if configPath == "" {
		configPath = initialPaths.ConfigFile
	}
	if err := ensureStartConfigMigrated(initialPaths, configPath, opts); err != nil {
		return nil, "", err
	}
	loadOptions := configstore.LoadOptions{Profile: profile}
	if opts.Agent != "" {
		loadOptions.AgentKind = configstore.AgentKind(opts.Agent)
	}
	snapshot, err := configstore.Load(configPath, loadOptions)
	if os.IsNotExist(err) {
		bootstrapOpts := opts
		bootstrapPaths := initialPaths
		if opts.Profile == "" && opts.Agent == "" {
			var detectErr error
			bootstrapOpts, bootstrapPaths, detectErr = selectFirstRunBootstrapAgent(opts, home)
			if detectErr != nil {
				return nil, "", detectErr
			}
			loadOptions.Profile = bootstrapPaths.Profile
			loadOptions.AgentKind = configstore.AgentKind(bootstrapOpts.Agent)
		}
		if bootstrapErr := bootstrapStartConfig(bootstrapOpts, bootstrapPaths, configPath); bootstrapErr != nil {
			return nil, "", bootstrapErr
		}
		snapshot, err = configstore.Load(configPath, loadOptions)
	}
	if err != nil {
		return nil, "", err
	}
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: home, Profile: snapshot.ProfileName})
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(paths.DefaultWorkspaceDir, 0o700); err != nil {
		return nil, "", err
	}

	runtimeConfig := snapshot.Runtime
	if opts.Agent != "" {
		runtimeConfig.AgentKind = configstore.AgentKind(opts.Agent)
	}
	appConfig := larkcli.AppConfig{
		Accounts: runtimeConfig.Accounts,
		Secrets:  runtimeConfig.Secrets,
	}
	if err := assertStartAppMatchesExistingProfile(opts, snapshot.ProfileName, appConfig); err != nil {
		return nil, "", err
	}
	if opts.AppID != "" {
		appConfig.Accounts.App.ID = opts.AppID
	}
	if opts.Tenant != "" {
		appConfig.Accounts.App.Tenant = larkcli.TenantBrand(opts.Tenant)
	}
	var appSecret string
	if opts.AppSecret != "" {
		appSecret = opts.AppSecret
		secretID := secretstore.SecretKeyForApp(appConfig.Accounts.App.ID)
		if err := storeStartAppSecret(paths, secretID, opts.AppSecret); err != nil {
			return nil, "", err
		}
		appConfig.Accounts.App.Secret = larkcli.SecretRef{Source: "exec", Provider: "bridge", ID: secretID}
	} else {
		var err error
		appSecret, err = resolveStartAppSecret(ctx, appConfig, paths)
		if err != nil {
			return nil, "", err
		}
	}

	transport, err := bridge.NewOAPILarkTransport(bridge.OAPILarkTransportOptions{
		AppID:     appConfig.Accounts.App.ID,
		AppSecret: appSecret,
		Tenant:    string(appConfig.Accounts.App.Tenant),
	})
	if err != nil {
		return nil, "", err
	}

	larkEnv := bridge.BuildLarkChannelEnv(bridge.LarkCliEnvContext{
		Profile:                 paths.Profile,
		RootDir:                 paths.RootDir,
		ConfigPath:              configPath,
		LarkCliConfigDir:        paths.LarkCliConfigDir,
		LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
	})
	client, agentKind, err := buildStartClient(runtimeConfig, paths, larkEnv)
	if err != nil {
		return nil, "", err
	}
	availability, err := client.CheckAvailability(ctx)
	if err != nil {
		return nil, "", err
	}
	if !availability.OK {
		return nil, "", formatAvailabilityFailure(availability)
	}
	projectionPaths := bridge.LarkCliProjectionPaths{
		RootDir:                 paths.RootDir,
		Profile:                 paths.Profile,
		LarkCliSourceDir:        paths.LarkCliSourceDir,
		LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
		SecretsGetterScript:     paths.SecretsGetterScript,
		SecretsGetterCommand:    currentExecutable(),
	}
	projectionEnv := bridge.LarkCliEnvContext{
		Profile:                 paths.Profile,
		RootDir:                 paths.RootDir,
		ConfigPath:              configPath,
		LarkCliConfigDir:        paths.LarkCliConfigDir,
		LarkCliSourceConfigFile: paths.LarkCliSourceConfigFile,
	}
	identityPreset := bridge.LarkCliIdentityPreset(runtimeConfig.LarkCli.IdentityPreset)
	bridgeAppConfig, err := toBridgeLarkCLIAppConfig(appConfig)
	if err != nil {
		return nil, "", err
	}
	if !opts.SkipCheckLarkCli {
		if ensureStartLarkCLIAvailable(ctx, paths.Profile) {
			preflightResult, err := preflightStartLarkCLI(ctx, bridgeAppConfig, projectionPaths, projectionEnv, identityPreset, &runtimeConfig.ProfileConfig)
			if err != nil {
				return nil, "", err
			}
			if preflightResult.BindFailed {
				printLarkCLIConfigurationWarning(paths.Profile, preflightResult.BindDiagnostic)
			}
			if preflightResult.IdentityPreset != "" {
				identityPreset = preflightResult.IdentityPreset
			}
		}
	}
	projection := bridge.NewLarkCLIProjectionHook(bridge.LarkCLIProjectionHookOptions{
		Config:              bridgeAppConfig,
		Paths:               projectionPaths,
		Env:                 projectionEnv,
		IdentityPreset:      identityPreset,
		ApplyIdentityPolicy: true,
	})
	callbackAuth, err := bridge.NewCallbackAuth(bridge.CallbackAuthOptions{
		Keys:           []bridge.CallbackKey{{Version: 1, Secret: appSecret}},
		NonceStorePath: filepath.Join(paths.ProfileDir, "callback-nonces.json"),
	})
	if err != nil {
		return nil, "", err
	}
	var instance *bridge.Bridge
	commandOptions, err := startCommandOptions(runtimeConfig, paths, configPath, projectionEnv, func(ctx context.Context) error {
		if instance == nil {
			return fmt.Errorf("bridge is not initialized")
		}
		refreshed, err := configstore.Load(configPath, configstore.LoadOptions{Profile: paths.Profile})
		if err != nil {
			return err
		}
		next := refreshed.Runtime.Accounts.App
		if next.ID == "" {
			return fmt.Errorf("app id is empty")
		}
		return instance.Reconnect(ctx, bridge.RuntimeReconnectOptions{
			AppID:      next.ID,
			Tenant:     bridge.RuntimeTenant(next.Tenant),
			ConfigPath: configPath,
		})
	})
	if err != nil {
		return nil, "", err
	}
	processHooks := startProcessHooks{instance: func() *bridge.Bridge { return instance }}
	commandOptions.ProcessIDFunc = processHooks.CurrentID
	commandOptions.Processes = processHooks
	commandOptions.ProcessController = processHooks
	logger, telemetry := startObservability(ctx, paths, appConfig)

	instance, err = bridge.New(bridge.Options{
		Home:                  paths.RootDir,
		Profile:               paths.Profile,
		Logger:                logger,
		Telemetry:             telemetry,
		Client:                client,
		LarkTransport:         transport,
		LarkProfileProjection: projection,
		LarkManaged: bridge.LarkManagedOptions{
			MessageReplyMode: startReplyMode(runtimeConfig.Preferences),
			ShowToolCalls:    boolPtr(startShowToolCalls(runtimeConfig.Preferences)),
			CotMessages:      startCotMessages(runtimeConfig.Preferences),
			CommandOptions:   commandOptions,
			CallbackAuth:     callbackAuth,
		},
		AppID:      appConfig.Accounts.App.ID,
		Tenant:     bridge.RuntimeTenant(appConfig.Accounts.App.Tenant),
		AgentKind:  agentKind,
		Version:    version,
		ConfigPath: configPath,
	})
	if err != nil {
		return nil, "", err
	}
	return instance, paths.Profile, nil
}

func startObservability(ctx context.Context, paths apppaths.Paths, appConfig larkcli.AppConfig) (*bridge.JSONLLogger, bridge.TelemetryAdapter) {
	hostname, _ := os.Hostname()
	telemetry, err := bridge.LoadTelemetryAdapterFromEnv(ctx, bridge.AdapterMeta{
		Version:  version,
		AppID:    appConfig.Accounts.App.ID,
		Tenant:   string(appConfig.Accounts.App.Tenant),
		Hostname: hostname,
	}, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[telemetry.load_fail] %v\n", err)
	}
	logger := bridge.NewJSONLLogger(bridge.JSONLLoggerOptions{
		Dir:       paths.LogsDir,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Telemetry: telemetry,
	})
	if _, err := logger.GC(); err != nil {
		logger.Warn("logger.gc-failed", map[string]any{"err": err.Error()})
	}
	return logger, telemetry
}

func assertStartAppMatchesExistingProfile(opts startOptions, profile string, cfg larkcli.AppConfig) error {
	if opts.AppID == "" || opts.AppID == cfg.Accounts.App.ID {
		return nil
	}
	return fmt.Errorf("profile already exists: %s; it uses app %s. omit --app-id or create another profile", profile, cfg.Accounts.App.ID)
}

func formatAvailabilityFailure(availability bridge.AgentAvailability) error {
	if availability.Diagnostic == nil {
		return fmt.Errorf("agent preflight failed: %s", availability.Error)
	}
	return fmt.Errorf("agent preflight failed (%s): %s", availability.Diagnostic.Code, availability.Error)
}

type agentDisplayInfo struct {
	id          string
	displayName string
}

func agentDisplay(kind configstore.AgentKind) agentDisplayInfo {
	if kind == configstore.AgentCodex {
		return agentDisplayInfo{id: "codex", displayName: "Codex CLI"}
	}
	return agentDisplayInfo{id: "claude", displayName: "Claude Code"}
}

func startReplyMode(preferences map[string]any) bridge.LarkReplyMode {
	raw, _ := preferences["messageReply"].(string)
	if raw == string(bridge.LarkReplyText) && preferences["messageReplyMigrated"] != true {
		return bridge.LarkReplyMarkdown
	}
	switch raw {
	case string(bridge.LarkReplyCard):
		return bridge.LarkReplyCard
	case string(bridge.LarkReplyText):
		return bridge.LarkReplyText
	default:
		return bridge.LarkReplyMarkdown
	}
}

func startShowToolCalls(preferences map[string]any) bool {
	raw, ok := preferences["showToolCalls"].(bool)
	if ok {
		return raw
	}
	return true
}

func startCotMessages(preferences map[string]any) bridge.LarkCotMessagesMode {
	raw, _ := preferences["cotMessages"].(string)
	switch raw {
	case "brief", "simple":
		return bridge.LarkCotMessagesBrief
	case "detailed", "on":
		return bridge.LarkCotMessagesDetailed
	default:
		return bridge.LarkCotMessagesOff
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func startCommandOptions(cfg configstore.RuntimeConfig, paths apppaths.Paths, configPath string, larkEnv bridge.LarkCliEnvContext, restart func(context.Context) error) (bridge.CommandOptions, error) {
	keystore, err := bridge.NewKeystore(bridge.KeystoreOptions{
		Paths: bridge.KeystorePaths{
			SecretsFile:      paths.SecretsFile,
			KeystoreSaltFile: paths.KeystoreSaltFile,
		},
	})
	if err != nil {
		return bridge.CommandOptions{}, err
	}
	workspaces, err := bridge.NewFileWorkspaceStore(paths.WorkspacesFile)
	if err != nil {
		return bridge.CommandOptions{}, err
	}
	options := bridge.CommandOptions{
		ProfileName:       paths.Profile,
		ConfigPath:        configPath,
		Keystore:          keystore,
		Workspaces:        workspaces,
		ProcessID:         fmt.Sprint(os.Getpid()),
		GlobalIdleTimeout: startGlobalIdleTimeout(cfg.Preferences),
		AccountValidator:  bridge.CommandAccountValidatorFunc(validateStartAppCredentials),
		LarkCLIIdentity: bridge.CommandLarkCLIIdentityPolicyApplierFunc(func(ctx context.Context, identity string) bool {
			return bridge.ApplyLarkCliIdentityPolicy(ctx, larkEnv, bridge.LarkCliIdentityPreset(identity), bridge.LarkCliIdentityPolicyOptions{})
		}),
	}
	if restart != nil {
		options.Reconnector = bridge.CommandReconnectorFunc(func(ctx context.Context, _ bool) error {
			return restart(ctx)
		})
	}
	return options, nil
}

type startProcessHooks struct {
	instance func() *bridge.Bridge
}

func (h startProcessHooks) CurrentID() string {
	status, ok := h.status()
	if !ok || status.Runtime == nil || status.Runtime.Entry == nil {
		return fmt.Sprint(os.Getpid())
	}
	return status.Runtime.Entry.ID
}

func (h startProcessHooks) ListProcesses() []bridge.CommandProcessEntry {
	status, ok := h.status()
	if !ok || status.Runtime == nil {
		return nil
	}
	out := make([]bridge.CommandProcessEntry, 0, len(status.Runtime.Processes))
	for _, entry := range status.Runtime.Processes {
		startedAt, _ := time.Parse(time.RFC3339Nano, entry.StartedAt)
		out = append(out, bridge.CommandProcessEntry{
			ID:        entry.ID,
			PID:       entry.PID,
			AppID:     entry.AppID,
			BotName:   entry.BotName,
			StartedAt: startedAt,
		})
	}
	return out
}

func (h startProcessHooks) ExitSelf(context.Context) error {
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}
	return process.Signal(os.Interrupt)
}

func (h startProcessHooks) Terminate(ctx context.Context, entry bridge.CommandProcessEntry) (bool, error) {
	_, stillAlive, err := stopProcessEntry(ctx, entry.PID, defaultProcessStopTimeout)
	return stillAlive, err
}

func (h startProcessHooks) status() (bridge.Status, bool) {
	if h.instance == nil || h.instance() == nil {
		return bridge.Status{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, err := h.instance().Status(ctx)
	if err != nil {
		return bridge.Status{}, false
	}
	return status, true
}

func validateStartAppCredentials(ctx context.Context, appID, appSecret, tenant string) (bridge.CommandAccountValidationResult, error) {
	if appID == "" || appSecret == "" {
		return bridge.CommandAccountValidationResult{OK: false, Reason: "App ID 或 App Secret 为空"}, nil
	}
	client := larksdk.NewClient(appID, appSecret, larksdk.WithOpenBaseUrl(startOAPIDomain(tenant)))
	resp, err := client.GetTenantAccessTokenBySelfBuiltApp(ctx, &larkcore.SelfBuiltTenantAccessTokenReq{
		AppID:     appID,
		AppSecret: appSecret,
	})
	if err != nil {
		return bridge.CommandAccountValidationResult{}, err
	}
	if resp == nil || !resp.Success() {
		reason := "App 凭据校验失败"
		if resp != nil && resp.Msg != "" {
			reason = resp.Msg
		}
		return bridge.CommandAccountValidationResult{OK: false, Reason: reason}, nil
	}
	return bridge.CommandAccountValidationResult{OK: true}, nil
}

func startOAPIDomain(tenant string) string {
	if tenant == string(larkcli.TenantLark) {
		return larksdk.LarkBaseUrl
	}
	return larksdk.FeishuBaseUrl
}

func startGlobalIdleTimeout(preferences map[string]any) time.Duration {
	minutes, ok := preferenceInt(preferences["runIdleTimeoutMinutes"])
	if !ok || minutes <= 0 {
		return 0
	}
	if minutes > 120 {
		minutes = 120
	}
	return time.Duration(minutes) * time.Minute
}

func preferenceInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		n, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

func buildStartClient(cfg configstore.RuntimeConfig, paths apppaths.Paths, larkEnv map[string]string) (*bridge.Client, bridge.RuntimeAgentKind, error) {
	requireMention := cfg.Access.RequireMentionInGroup
	defaultWorkingDir := cfg.Workspaces.Default
	if defaultWorkingDir == "" {
		defaultWorkingDir = paths.DefaultWorkspaceDir
	}
	defaultAccess := bridge.AccessMode(cfg.Permissions.DefaultAccess)
	maxAccess := bridge.AccessMode(cfg.Permissions.MaxAccess)
	switch cfg.AgentKind {
	case configstore.AgentCodex:
		codex := cfg.Codex
		if codex == nil {
			return nil, "", fmt.Errorf("codex profile requires codex configuration")
		}
		inheritCodexHome := codex.InheritCodexHome
		ignoreRules := codex.IgnoreRules
		client, err := bridge.NewCodexClient(bridge.CodexClientOptions{
			Binary:             codex.BinaryPath,
			ProfileStateDir:    paths.ProfileDir,
			DefaultWorkingDir:  defaultWorkingDir,
			SessionStorePath:   paths.SessionsFile,
			SessionCatalogPath: paths.SessionsFile + ".catalog.json",
			DefaultAccess:      defaultAccess,
			MaxAccess:          maxAccess,
			AllowedUsers:       cfg.Access.AllowedUsers,
			AllowedChats:       cfg.Access.AllowedChats,
			Admins:             cfg.Access.Admins,
			RequireMention:     &requireMention,
			CodexHome:          codex.CodexHome,
			InheritCodexHome:   &inheritCodexHome,
			IgnoreUserConfig:   codex.IgnoreUserConfig,
			IgnoreRules:        &ignoreRules,
			LarkChannelEnv:     larkEnv,
		})
		return client, bridge.RuntimeAgentCodex, err
	case configstore.AgentClaude:
		var permissionMode bridge.ClaudePermissionMode
		if cfg.Permissions.Claude != nil {
			permissionMode = bridge.ClaudePermissionMode(cfg.Permissions.Claude.PermissionMode)
		}
		client, err := bridge.NewClaudeClient(bridge.ClaudeClientOptions{
			DefaultWorkingDir:  defaultWorkingDir,
			SessionStorePath:   paths.SessionsFile,
			SessionCatalogPath: paths.SessionsFile + ".catalog.json",
			DefaultAccess:      defaultAccess,
			MaxAccess:          maxAccess,
			AllowedUsers:       cfg.Access.AllowedUsers,
			AllowedChats:       cfg.Access.AllowedChats,
			Admins:             cfg.Access.Admins,
			RequireMention:     &requireMention,
			PermissionMode:     permissionMode,
			LarkChannelEnv:     larkEnv,
		})
		return client, bridge.RuntimeAgentClaude, err
	default:
		return nil, "", fmt.Errorf("unsupported agent kind %q", cfg.AgentKind)
	}
}

func resolveStartAppSecret(ctx context.Context, cfg larkcli.AppConfig, paths apppaths.Paths) (string, error) {
	secretConfig, err := toSecretStoreAppConfig(cfg)
	if err != nil {
		return "", err
	}
	return secretstore.ResolveAppSecret(ctx, secretConfig, secretstore.ResolverOptions{
		Paths: secretstore.KeystorePaths{
			SecretsFile:         paths.SecretsFile,
			KeystoreSaltFile:    paths.KeystoreSaltFile,
			SecretsGetterScript: paths.SecretsGetterScript,
		},
	})
}

func storeStartAppSecret(paths apppaths.Paths, id string, value string) error {
	store, err := secretstore.NewKeystore(secretstore.KeystoreOptions{
		Paths: secretstore.KeystorePaths{
			SecretsFile:      paths.SecretsFile,
			KeystoreSaltFile: paths.KeystoreSaltFile,
		},
	})
	if err != nil {
		return err
	}
	return store.SetSecret(id, value)
}

func toSecretStoreAppConfig(cfg larkcli.AppConfig) (secretstore.AppConfig, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return secretstore.AppConfig{}, err
	}
	var out secretstore.AppConfig
	if err := json.Unmarshal(data, &out); err != nil {
		return secretstore.AppConfig{}, err
	}
	return out, nil
}

func toBridgeLarkCLIAppConfig(cfg larkcli.AppConfig) (bridge.LarkCLIAppConfig, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return bridge.LarkCLIAppConfig{}, err
	}
	var out bridge.LarkCLIAppConfig
	if err := json.Unmarshal(data, &out); err != nil {
		return bridge.LarkCLIAppConfig{}, err
	}
	return out, nil
}

func toBridgeConfigProfilePtr(cfg *configstore.ProfileConfig) (*bridge.ConfigProfile, error) {
	if cfg == nil {
		return nil, nil
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var out bridge.ConfigProfile
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func preflightStartLarkCLI(ctx context.Context, cfg bridge.LarkCLIAppConfig, paths bridge.LarkCliProjectionPaths, env bridge.LarkCliEnvContext, identityPreset bridge.LarkCliIdentityPreset, profileConfig *configstore.ProfileConfig) (bridge.LarkCLIPreflightResult, error) {
	publicProfile, err := toBridgeConfigProfilePtr(profileConfig)
	if err != nil {
		return bridge.LarkCLIPreflightResult{}, err
	}
	return bridge.PreflightLarkCLI(ctx, bridge.LarkCLIPreflightOptions{
		Config:          cfg,
		ProjectionPaths: paths,
		Env:             env,
		IdentityPreset:  identityPreset,
		ProfileConfig:   publicProfile,
	})
}

func ensureStartLarkCLIAvailable(ctx context.Context, profile string) bool {
	if commandAvailable(ctx, "lark-cli", "--version") {
		return true
	}
	fmt.Fprintln(os.Stdout, "\nlark-cli is not installed")
	fmt.Fprintln(os.Stdout, "\nlark-cli is the Feishu/Lark command-line tool. After installation, the agent can:")
	fmt.Fprintln(os.Stdout, "  - send interactive cards and forms")
	fmt.Fprintln(os.Stdout, "  - query calendars, docs, tasks, OKRs, and attendance")
	fmt.Fprintln(os.Stdout, "  - use 200+ Feishu/Lark API commands")
	if !stdioInteractive() {
		printLarkCLIManualInstallHint(profile)
		return false
	}
	fmt.Fprintln(os.Stdout, "\nSetting up lark-cli...")
	installCtx, cancel := context.WithTimeout(ctx, larkCLIInstallTimeout)
	defer cancel()
	cmd := exec.CommandContext(installCtx, "npm", "install", "-g", "@larksuite/cli")
	output, err := cmd.CombinedOutput()
	if err != nil || !commandAvailable(ctx, "lark-cli", "--version") {
		fmt.Fprintln(os.Stderr, "lark-cli installation did not complete")
		if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
			fmt.Fprintln(os.Stderr, trimmed)
		}
		printLarkCLIManualInstallHint(profile)
		return false
	}
	fmt.Fprintln(os.Stdout, "✓ lark-cli installed")
	return true
}

func commandAvailable(ctx context.Context, command string, args ...string) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, command, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

func printLarkCLIManualInstallHint(profile string) {
	fmt.Fprintln(os.Stdout, "\n(non-interactive mode or install failed; skipping auto-install)")
	fmt.Fprintln(os.Stdout, "\nManual install command:")
	fmt.Fprintln(os.Stdout, "  npm install -g @larksuite/cli")
	fmt.Fprintln(os.Stdout, "\nRestart the current profile after installation; bridge will initialize lark-cli automatically.")
	if profile != "" {
		fmt.Fprintf(os.Stdout, "  lark-channel-bridge run --profile %s\n", profile)
	}
	fmt.Fprintln(os.Stdout, "\nDocs: https://github.com/larksuite/cli")
}

func printLarkCLIConfigurationWarning(profile string, diagnostic string) {
	fmt.Fprint(os.Stdout, larkCLIConfigurationWarning(profile, diagnostic))
}

func larkCLIConfigurationWarning(profile string, diagnostic string) string {
	var b strings.Builder
	tooOld := isUnsupportedLarkChannelSourceDiagnostic(diagnostic)
	if tooOld {
		b.WriteString("\nThe installed lark-cli does not support the lark-channel source required by bridge auto-configuration.\n")
		b.WriteString("Bridge will keep listening for messages, but the agent cannot use lark-cli to call Feishu/Lark APIs.\n")
	} else {
		b.WriteString("\nBridge will keep listening for messages, but this profile did not finish lark-cli configuration.\n")
		b.WriteString("Impact: the agent may be unable to send messages, send cards, or call Feishu/Lark APIs through lark-cli.\n")
	}
	b.WriteString("\nRecovery:\n")
	if profile != "" {
		if tooOld {
			b.WriteString("  1. Install a lark-cli build that supports the lark-channel source.\n")
			fmt.Fprintf(&b, "  2. Restart this profile: lark-channel-bridge run --profile %s\n", profile)
		} else {
			fmt.Fprintf(&b, "  1. Restart this profile: lark-channel-bridge run --profile %s\n", profile)
		}
	} else {
		if tooOld {
			b.WriteString("  1. Install a lark-cli build that supports the lark-channel source.\n")
			b.WriteString("  2. Restart the current profile.\n")
		} else {
			b.WriteString("  1. Restart the current profile.\n")
		}
	}
	if !tooOld {
		b.WriteString("  2. If it still fails, check that this profile has a valid App Secret and that the lark-cli config directory is writable.\n")
	}
	b.WriteString("\nDiagnostic details:\n")
	b.WriteString(formatLarkCLIDiagnosticOutput(diagnostic))
	b.WriteString("\n")
	return b.String()
}

func isUnsupportedLarkChannelSourceDiagnostic(output string) bool {
	return unsupportedLarkChannelSourceDiagnosticRE.MatchString(output)
}

func formatLarkCLIDiagnosticOutput(output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "(lark-cli did not print error details)"
	}
	if regexp.MustCompile(`(?i)unknown flag:\s*--source|unknown command ["']?bind["']?`).MatchString(trimmed) {
		return "lark-cli does not support `config bind --source lark-channel`."
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		cleaned := stripLarkCLINotices(parsed)
		if data, marshalErr := json.MarshalIndent(cleaned, "", "  "); marshalErr == nil {
			return string(data)
		}
	}
	lines := strings.Split(trimmed, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if !isLarkCLIUpdateNoticeLine(line) {
			out = append(out, line)
		}
	}
	trimmed = strings.TrimSpace(strings.Join(out, "\n"))
	if trimmed == "" {
		return "(lark-cli did not print error details)"
	}
	return trimmed
}

func stripLarkCLINotices(value any) any {
	switch typed := value.(type) {
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = stripLarkCLINotices(child)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if key == "_notice" {
				continue
			}
			out[key] = stripLarkCLINotices(child)
		}
		return out
	default:
		return value
	}
}

func isLarkCLIUpdateNoticeLine(line string) bool {
	return strings.Contains(strings.ToLower(line), "_notice") ||
		(regexp.MustCompile(`(?i)lark-cli`).MatchString(line) && regexp.MustCompile(`(?i)update|upgrade|latest|newer|npm\s+install`).MatchString(line)) ||
		regexp.MustCompile(`(?i)\b(current|latest)\s+version\b`).MatchString(line)
}

func currentExecutable() string {
	exe, err := os.Executable()
	if err == nil && exe != "" {
		if abs, absErr := filepath.Abs(exe); absErr == nil {
			return abs
		}
		return exe
	}
	if len(os.Args) > 0 {
		if abs, absErr := filepath.Abs(os.Args[0]); absErr == nil {
			return abs
		}
		return os.Args[0]
	}
	return "lark-channel-bridge"
}

func printHelp(w io.Writer) {
	fmt.Fprintf(w, `lark-channel-bridge %s

Usage:
  lark-channel-bridge --help
  lark-channel-bridge --version
  lark-channel-bridge run [--home DIR] [--config FILE] [--profile NAME] [--agent codex|claude] [--workspace DIR] [--skip-check-lark-cli]
  lark-channel-bridge start [--home DIR] [--config FILE] [--profile NAME] [--agent codex|claude] [--workspace DIR] [--skip-check-lark-cli]
  lark-channel-bridge stop [--home DIR] [--profile NAME]
  lark-channel-bridge restart [--home DIR] [--profile NAME]
  lark-channel-bridge status [--home DIR] [--profile NAME]
  lark-channel-bridge unregister [--home DIR] [--profile NAME]
  lark-channel-bridge migrate [--home DIR] [--config FILE] [--profile NAME] [--agent codex|claude]
  lark-channel-bridge ps [--home DIR]
  lark-channel-bridge kill [--home DIR] <id|pid>
  lark-channel-bridge profile create [--home DIR] [--agent codex|claude] [--workspace DIR] [--app-id ID [--app-secret SECRET]] <name>
  lark-channel-bridge profile list [--home DIR]
  lark-channel-bridge profile use [--home DIR] <name>
  lark-channel-bridge profile export [--home DIR] [--output FILE] [--force] [--include-secrets --yes] <name>
  lark-channel-bridge profile remove [--home DIR] [--purge --yes] <name>
  lark-channel-bridge secrets get [--home DIR] [--profile NAME]
  lark-channel-bridge secrets set [--home DIR] [--profile NAME] --app-id ID [--value SECRET]
  lark-channel-bridge secrets list [--home DIR] [--profile NAME]
  lark-channel-bridge secrets remove [--home DIR] [--profile NAME] --app-id ID

The Go CLI run command starts the bridge in the foreground. start installs and
starts the current profile as an OS-managed background service.
When config.json does not exist, run in an interactive terminal for QR app
registration or pass --app-id and --app-secret to use an existing app.
	`, version)
}

func printProfileHelp(w io.Writer) {
	fmt.Fprintf(w, `lark-channel-bridge profile

Usage:
  lark-channel-bridge profile create [--home DIR] [--agent codex|claude] [--workspace DIR] [--app-id ID [--app-secret SECRET]] <name>
  lark-channel-bridge profile list [--home DIR]
  lark-channel-bridge profile use [--home DIR] <name>
  lark-channel-bridge profile export [--home DIR] [--output FILE] [--force] [--include-secrets --yes] <name>
  lark-channel-bridge profile remove [--home DIR] [--purge --yes] <name>

Profile create uses QR registration in an interactive terminal when --app-id is
omitted. Profile export redacts app secrets by default. Use --include-secrets
--yes only when you intentionally need plaintext secrets in the exported config.
`)
}

func printSecretsHelp(w io.Writer) {
	fmt.Fprintf(w, `lark-channel-bridge secrets

Usage:
  lark-channel-bridge secrets get [--home DIR] [--profile NAME]
  lark-channel-bridge secrets set [--home DIR] [--profile NAME] --app-id ID [--value SECRET]
  lark-channel-bridge secrets list [--home DIR] [--profile NAME]
  lark-channel-bridge secrets remove [--home DIR] [--profile NAME] --app-id ID

The get command implements the lark-cli exec-provider protocol. set reads
--value or, when omitted, the secret from stdin.
`)
}
