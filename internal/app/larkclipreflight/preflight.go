package larkclipreflight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/configstore"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
)

const defaultTimeout = 30 * time.Second

var configPathLineRE = regexp.MustCompile(`(?im)^Config file path:\s*(.+?)\s*$`)
var unsupportedLarkChannelSourceRE = regexp.MustCompile(`(?i)(unknown flag:\s*--source|unknown command ["']?bind["']?|invalid --source[^-\n]*lark-channel|unsupported source:\s*lark-channel)`)
var legacyOverlayFailureRE = regexp.MustCompile(`(?i)(accounts\.app\.id missing in |cannot read .*config\.json|no such file or directory)`)

type Options struct {
	Config          larkcli.AppConfig
	ProjectionPaths larkcli.ProjectionPaths
	Env             larkcli.EnvContext
	IdentityPreset  larkcli.IdentityPreset
	ProfileConfig   *configstore.ProfileConfig
	Command         string
	BaseEnv         map[string]string
	Timeout         time.Duration
	Runner          Runner
}

type Result struct {
	Bound             bool
	BindFailed        bool
	BindDiagnostic    string
	IdentityPreset    larkcli.IdentityPreset
	LocalUserImported bool
	LocalUserStatus   configstore.LarkCliUserImportStatus
	LocalUserReason   string
}

type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func (r CommandResult) OK() bool {
	return r.ExitCode == 0
}

func (r CommandResult) Output() string {
	if r.Stderr == "" {
		return r.Stdout
	}
	if r.Stdout == "" {
		return r.Stderr
	}
	return r.Stdout + "\n" + r.Stderr
}

type Runner interface {
	RunLarkCLICommand(ctx context.Context, invocation larkcli.CommandInvocation) (CommandResult, error)
}

type RunnerFunc func(ctx context.Context, invocation larkcli.CommandInvocation) (CommandResult, error)

func (f RunnerFunc) RunLarkCLICommand(ctx context.Context, invocation larkcli.CommandInvocation) (CommandResult, error) {
	return f(ctx, invocation)
}

func Run(ctx context.Context, opts Options) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := larkcli.WriteLarkCliSourceProjection(opts.Config, opts.ProjectionPaths); err != nil {
		return Result{}, err
	}
	state := newState(opts)
	target := state.readPrivateTarget()
	if target.sameApp {
		result, handled := state.handleExistingPrivateTarget(ctx, target)
		if handled {
			return result, nil
		}
		if state.run(ctx, []string{"config", "show"}, state.privateEnv()).OK() {
			return Result{IdentityPreset: target.identityPreset}, nil
		}
	}
	if !target.sameApp && !state.shouldAttemptLocalUserImport() {
		if state.run(ctx, []string{"config", "show"}, state.privateEnv()).OK() {
			return Result{IdentityPreset: state.requestedIdentityPreset()}, nil
		}
	}

	local := localUserResult{status: configstore.LarkCliUserImportNotNeeded, reason: "not-private-binding"}
	if state.shouldAttemptLocalUserImport() {
		local = state.detectLocalSameAppUser(ctx)
	}
	if !state.bind(ctx, larkcli.IdentityBotOnly) {
		_ = state.persistLarkCliConfig(larkcli.IdentityBotOnly, importFailureStatus(local.status), "bind-failed")
		return Result{IdentityPreset: larkcli.IdentityBotOnly, BindFailed: true, BindDiagnostic: state.lastBindOutput, LocalUserStatus: importFailureStatus(local.status), LocalUserReason: "bind-failed"}, nil
	}
	result := Result{Bound: true, IdentityPreset: larkcli.IdentityBotOnly, LocalUserStatus: local.status, LocalUserReason: local.reason}
	if local.status == configstore.LarkCliUserImportImported {
		_ = state.copyLocalUsersToPrivateTarget(local.users)
		if state.applyIdentity(ctx, larkcli.IdentityUserDefault) && state.privateSameAppUserReady(ctx) {
			_ = state.persistLarkCliConfig(larkcli.IdentityUserDefault, configstore.LarkCliUserImportImported, "same-app-local-user")
			result.IdentityPreset = larkcli.IdentityUserDefault
			result.LocalUserImported = true
			result.LocalUserStatus = configstore.LarkCliUserImportImported
			result.LocalUserReason = "same-app-local-user"
			return result, nil
		}
		_ = state.applyIdentity(ctx, larkcli.IdentityBotOnly)
		_ = state.persistLarkCliConfig(larkcli.IdentityBotOnly, configstore.LarkCliUserImportFailed, "private-user-missing-after-switch")
		result.LocalUserStatus = configstore.LarkCliUserImportFailed
		result.LocalUserReason = "private-user-missing-after-switch"
		return result, nil
	}
	_ = state.applyIdentity(ctx, larkcli.IdentityBotOnly)
	_ = state.persistLarkCliConfig(larkcli.IdentityBotOnly, local.status, local.reason)
	return result, nil
}

type state struct {
	opts           Options
	command        string
	timeout        time.Duration
	runner         Runner
	baseEnv        map[string]string
	targetConfig   string
	rootConfig     string
	sourceConfig   string
	profile        string
	lastBindOutput string
}

func newState(opts Options) *state {
	command := opts.Command
	if command == "" {
		command = "lark-cli"
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	runner := opts.Runner
	if runner == nil {
		runner = osRunner{}
	}
	baseEnv := opts.BaseEnv
	if baseEnv == nil {
		baseEnv = larkcli.EnvMapFromList(os.Environ())
	}
	return &state{
		opts:         opts,
		command:      command,
		timeout:      timeout,
		runner:       runner,
		baseEnv:      baseEnv,
		targetConfig: filepath.Join(opts.Env.LarkCliConfigDir, "lark-channel", "config.json"),
		rootConfig:   opts.Env.ConfigPath,
		sourceConfig: opts.Env.LarkCliSourceConfigFile,
		profile:      opts.Env.Profile,
	}
}

func (s *state) privateEnv() map[string]string {
	return larkcli.MergeProcessEnv(s.baseEnv, larkcli.BuildLarkChannelEnv(s.opts.Env))
}

func (s *state) legacyEnv() map[string]string {
	env := s.opts.Env
	env.LarkCliConfigDir = ""
	return larkcli.MergeProcessEnv(s.baseEnv, larkcli.BuildLarkChannelEnv(env))
}

func (s *state) requestedIdentityPreset() larkcli.IdentityPreset {
	if s.opts.IdentityPreset == larkcli.IdentityUserDefault {
		return larkcli.IdentityUserDefault
	}
	return larkcli.IdentityBotOnly
}

func (s *state) run(ctx context.Context, args []string, env map[string]string) CommandResult {
	runCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	result, err := s.runner.RunLarkCLICommand(runCtx, larkcli.CommandInvocation{
		Command: s.command,
		Args:    args,
		Env:     env,
	})
	if err != nil && result.ExitCode == 0 {
		result.ExitCode = 1
	}
	if runCtx.Err() != nil {
		result.ExitCode = 1
	}
	return result
}

func (s *state) bind(ctx context.Context, identity larkcli.IdentityPreset) bool {
	args := []string{"config", "bind", "--source", "lark-channel", "--identity", string(identity)}
	result := s.run(ctx, args, s.privateEnv())
	if result.OK() {
		s.lastBindOutput = ""
		return true
	}
	s.lastBindOutput = result.Output()
	if !s.shouldUseLegacyLarkCliSourceOverlay(result.Output()) {
		return false
	}
	err := larkcli.WithLegacyLarkCliSourceOverlay(s.rootConfig, s.sourceConfig, func() error {
		legacyResult := s.run(ctx, args, s.privateEnv())
		if legacyResult.OK() {
			s.lastBindOutput = ""
			return nil
		}
		s.lastBindOutput = legacyResult.Output()
		return errors.New("lark-cli bind failed")
	})
	return err == nil
}

func (s *state) shouldUseLegacyLarkCliSourceOverlay(output string) bool {
	if s.rootConfig == "" || s.sourceConfig == "" {
		return false
	}
	if unsupportedLarkChannelSourceRE.MatchString(output) {
		return false
	}
	return outputMentionsPath(output, s.rootConfig) && legacyOverlayFailureRE.MatchString(output)
}

func outputMentionsPath(output string, path string) bool {
	if strings.Contains(output, path) {
		return true
	}
	encoded, err := json.Marshal(path)
	if err != nil || len(encoded) < 2 {
		return false
	}
	return strings.Contains(output, string(encoded[1:len(encoded)-1]))
}

func (s *state) applyIdentity(ctx context.Context, identity larkcli.IdentityPreset) bool {
	strict, defaultAs := larkcli.IdentityPolicySettings(identity)
	if !s.run(ctx, []string{"config", "strict-mode", strict}, s.privateEnv()).OK() {
		return false
	}
	return s.run(ctx, []string{"config", "default-as", defaultAs}, s.privateEnv()).OK()
}

type privateTargetStatus struct {
	sameApp        bool
	identityPreset larkcli.IdentityPreset
	hasUserAuth    bool
}

func (s *state) readPrivateTarget() privateTargetStatus {
	raw, err := os.ReadFile(s.targetConfig)
	if err != nil {
		return privateTargetStatus{}
	}
	var parsed struct {
		Apps []map[string]any `json:"apps"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return privateTargetStatus{}
	}
	for _, app := range parsed.Apps {
		if app["appId"] != s.opts.Config.Accounts.App.ID || app["brand"] != string(s.opts.Config.Accounts.App.Tenant) {
			continue
		}
		if _, ok := app["users"].(string); ok {
			app["users"] = nil
			if body, err := json.MarshalIndent(parsed, "", "  "); err == nil {
				_ = configstore.WriteFileAtomic(s.targetConfig, append(body, '\n'), 0o600)
			}
		}
		return privateTargetStatus{
			sameApp:        true,
			identityPreset: identityPresetForTarget(app),
			hasUserAuth:    larkcli.HasStructuredLarkCliUserAuth(app["users"]),
		}
	}
	return privateTargetStatus{}
}

func identityPresetForTarget(app map[string]any) larkcli.IdentityPreset {
	if app["defaultAs"] == "auto" && app["strictMode"] == "off" {
		return larkcli.IdentityUserDefault
	}
	return larkcli.IdentityBotOnly
}

func (s *state) handleExistingPrivateTarget(ctx context.Context, target privateTargetStatus) (Result, bool) {
	if s.shouldSkipLocalUserImport() {
		if target.identityPreset != larkcli.IdentityBotOnly {
			_ = s.applyIdentity(ctx, larkcli.IdentityBotOnly)
		}
		_ = s.persistLarkCliConfig(larkcli.IdentityBotOnly, configstore.LarkCliUserImportNotNeeded, "manual-bot-only")
		return Result{IdentityPreset: larkcli.IdentityBotOnly, LocalUserStatus: configstore.LarkCliUserImportNotNeeded, LocalUserReason: "manual-bot-only"}, true
	}
	if target.hasUserAuth {
		if target.identityPreset != larkcli.IdentityUserDefault && !s.applyIdentity(ctx, larkcli.IdentityUserDefault) {
			_ = s.applyIdentity(ctx, larkcli.IdentityBotOnly)
			_ = s.persistLarkCliConfig(larkcli.IdentityBotOnly, configstore.LarkCliUserImportFailed, "private-user-policy-switch-failed")
			return Result{IdentityPreset: larkcli.IdentityBotOnly, LocalUserStatus: configstore.LarkCliUserImportFailed, LocalUserReason: "private-user-policy-switch-failed"}, true
		}
		_ = s.persistLarkCliConfig(larkcli.IdentityUserDefault, configstore.LarkCliUserImportSkippedExistingPrivate, "existing-private-user")
		return Result{IdentityPreset: larkcli.IdentityUserDefault, LocalUserStatus: configstore.LarkCliUserImportSkippedExistingPrivate, LocalUserReason: "existing-private-user"}, false
	}
	if s.shouldAttemptLocalUserImport() {
		local := s.detectLocalSameAppUser(ctx)
		if local.status == configstore.LarkCliUserImportImported {
			_ = s.copyLocalUsersToPrivateTarget(local.users)
			if s.applyIdentity(ctx, larkcli.IdentityUserDefault) && s.privateSameAppUserReady(ctx) {
				_ = s.persistLarkCliConfig(larkcli.IdentityUserDefault, configstore.LarkCliUserImportImported, "same-app-local-user")
				return Result{IdentityPreset: larkcli.IdentityUserDefault, LocalUserImported: true, LocalUserStatus: configstore.LarkCliUserImportImported, LocalUserReason: "same-app-local-user"}, true
			}
			_ = s.applyIdentity(ctx, larkcli.IdentityBotOnly)
			_ = s.persistLarkCliConfig(larkcli.IdentityBotOnly, configstore.LarkCliUserImportFailed, "local-user-policy-switch-failed")
			return Result{IdentityPreset: larkcli.IdentityBotOnly, LocalUserStatus: configstore.LarkCliUserImportFailed, LocalUserReason: "local-user-policy-switch-failed"}, true
		}
		_ = s.persistLarkCliConfig(target.identityPreset, local.status, local.reason)
	}
	return Result{}, false
}

func (s *state) shouldSkipLocalUserImport() bool {
	cfg := s.opts.ProfileConfig
	return cfg != nil && cfg.LarkCli.IdentityPreset == configstore.LarkCliIdentityBotOnly && cfg.LarkCli.LocalUserImport != nil && cfg.LarkCli.LocalUserImport.Reason == "manual-bot-only"
}

func (s *state) shouldAttemptLocalUserImport() bool {
	return s.opts.ProfileConfig != nil && !s.shouldSkipLocalUserImport()
}

type localUserResult struct {
	status configstore.LarkCliUserImportStatus
	reason string
	users  any
}

func (s *state) detectLocalSameAppUser(ctx context.Context) localUserResult {
	result := s.run(ctx, []string{"config", "show"}, s.legacyEnv())
	if !result.OK() {
		return localUserResult{status: configstore.LarkCliUserImportFailed, reason: "local-config-show-failed"}
	}
	var local map[string]any
	if err := json.Unmarshal([]byte(lastJSONLine(result.Output())), &local); err != nil {
		return localUserResult{status: configstore.LarkCliUserImportFailed, reason: "local-config-show-invalid-json"}
	}
	if local["appId"] != s.opts.Config.Accounts.App.ID || local["brand"] != string(s.opts.Config.Accounts.App.Tenant) {
		return localUserResult{status: configstore.LarkCliUserImportSkippedNoLocalUser, reason: "local-app-mismatch"}
	}
	if !larkcli.HasLarkCliUserAuth(local["users"]) {
		return localUserResult{status: configstore.LarkCliUserImportSkippedNoLocalUser, reason: "local-user-missing"}
	}
	users := s.readLocalSameAppUsers(result.Output())
	if users == nil && larkcli.HasStructuredLarkCliUserAuth(local["users"]) {
		users = local["users"]
	}
	if users == nil {
		return localUserResult{status: configstore.LarkCliUserImportSkippedNoLocalUser, reason: "local-user-unstructured"}
	}
	return localUserResult{status: configstore.LarkCliUserImportImported, reason: "same-app-local-user", users: users}
}

func (s *state) readLocalSameAppUsers(output string) any {
	matches := configPathLineRE.FindStringSubmatch(output)
	if len(matches) != 2 {
		return nil
	}
	data, err := os.ReadFile(strings.TrimSpace(matches[1]))
	if err != nil {
		return nil
	}
	var parsed struct {
		Apps []map[string]any `json:"apps"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	for _, app := range parsed.Apps {
		if app["appId"] == s.opts.Config.Accounts.App.ID && app["brand"] == string(s.opts.Config.Accounts.App.Tenant) && larkcli.HasStructuredLarkCliUserAuth(app["users"]) {
			return app["users"]
		}
	}
	return nil
}

func (s *state) copyLocalUsersToPrivateTarget(users any) bool {
	if !larkcli.HasStructuredLarkCliUserAuth(users) {
		return false
	}
	data, err := os.ReadFile(s.targetConfig)
	if err != nil {
		return false
	}
	var parsed struct {
		Apps []map[string]any `json:"apps"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return false
	}
	for _, app := range parsed.Apps {
		if app["appId"] != s.opts.Config.Accounts.App.ID || app["brand"] != string(s.opts.Config.Accounts.App.Tenant) {
			continue
		}
		if larkcli.HasStructuredLarkCliUserAuth(app["users"]) {
			return false
		}
		app["users"] = users
		body, err := json.MarshalIndent(parsed, "", "  ")
		if err != nil {
			return false
		}
		return configstore.WriteFileAtomic(s.targetConfig, append(body, '\n'), 0o600) == nil
	}
	return false
}

func (s *state) privateSameAppUserReady(ctx context.Context) bool {
	result := s.run(ctx, []string{"config", "show"}, s.privateEnv())
	if !result.OK() {
		return false
	}
	var app map[string]any
	if err := json.Unmarshal([]byte(lastJSONLine(result.Output())), &app); err != nil {
		return false
	}
	return app["appId"] == s.opts.Config.Accounts.App.ID && app["brand"] == string(s.opts.Config.Accounts.App.Tenant) && larkcli.HasLarkCliUserAuth(app["users"])
}

func (s *state) persistLarkCliConfig(identity larkcli.IdentityPreset, status configstore.LarkCliUserImportStatus, reason string) error {
	if s.rootConfig == "" || s.profile == "" {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	next := configstore.LarkCliConfig{
		IdentityPreset: configstore.LarkCliIdentityPreset(identity),
		LocalUserImport: &configstore.LarkCliLocalUserImport{
			Status:      status,
			AttemptedAt: now,
			Reason:      reason,
		},
	}
	if status == configstore.LarkCliUserImportImported {
		next.LocalUserImport.ImportedAt = now
	}
	err := configstore.WithConfigFileLock(s.rootConfig, func() error {
		snapshot, err := configstore.Load(s.rootConfig, configstore.LoadOptions{Profile: s.profile})
		if err != nil {
			return err
		}
		root := snapshot.Root
		profile, ok := root.Profiles[s.profile]
		if !ok {
			return nil
		}
		profile.LarkCli = next
		root.Profiles[s.profile] = profile
		return configstore.SaveRoot(s.rootConfig, root)
	})
	if err == nil && s.opts.ProfileConfig != nil {
		s.opts.ProfileConfig.LarkCli = next
	}
	return err
}

func importFailureStatus(status configstore.LarkCliUserImportStatus) configstore.LarkCliUserImportStatus {
	if status == configstore.LarkCliUserImportImported {
		return configstore.LarkCliUserImportFailed
	}
	return status
}

func lastJSONLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "{") {
			return line
		}
	}
	return strings.TrimSpace(output)
}

type osRunner struct{}

func (osRunner) RunLarkCLICommand(ctx context.Context, invocation larkcli.CommandInvocation) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, invocation.Command, invocation.Args...)
	cmd.Env = larkcli.EnvMapToList(invocation.Env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return result, nil
	}
	result.ExitCode = 1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	}
	return result, err
}
