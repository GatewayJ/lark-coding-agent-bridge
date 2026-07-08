package larkcli

import (
	"context"
	"io"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

type IdentityPreset string

const (
	IdentityBotOnly     IdentityPreset = "bot-only"
	IdentityUserDefault IdentityPreset = "user-default"
)

const defaultPolicyTimeout = 30 * time.Second

var (
	userOpenIDKeys     = map[string]struct{}{"userOpenId": {}, "openId": {}, "user_open_id": {}, "open_id": {}}
	emptyUserDisplayRE = regexp.MustCompile(`(?i)^(null|\(none\)|none|无|\(无\))$`)
	noLoggedInUsersRE  = regexp.MustCompile(`(?i)^\(?no\s+logged[-\s]?in\s+users\)?$`)
)

func HasLarkCliUserAuth(users any) bool {
	if HasStructuredLarkCliUserAuth(users) {
		return true
	}
	value, ok := users.(string)
	return ok && isLarkCliUserDisplayValue(value)
}

func HasStructuredLarkCliUserAuth(users any) bool {
	return hasStructuredLarkCliUserAuth(reflect.ValueOf(users))
}

func IdentityPolicySettings(identityPreset IdentityPreset) (strictMode string, defaultAs string) {
	if identityPreset == IdentityUserDefault {
		return "off", "auto"
	}
	return "bot", "bot"
}

type CommandInvocation struct {
	Command string
	Args    []string
	Env     map[string]string
}

type CommandRunner interface {
	RunLarkCliCommand(ctx context.Context, invocation CommandInvocation) error
}

type CommandRunnerFunc func(ctx context.Context, invocation CommandInvocation) error

func (f CommandRunnerFunc) RunLarkCliCommand(ctx context.Context, invocation CommandInvocation) error {
	return f(ctx, invocation)
}

type IdentityPolicyOptions struct {
	Command string
	Timeout time.Duration
	BaseEnv map[string]string
	Runner  CommandRunner
}

func ApplyLarkCliIdentityPolicy(ctx context.Context, envContext EnvContext, identityPreset IdentityPreset, opts IdentityPolicyOptions) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	command := opts.Command
	if command == "" {
		command = "lark-cli"
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultPolicyTimeout
	}
	runner := opts.Runner
	if runner == nil {
		runner = execCommandRunner{}
	}

	baseEnv := opts.BaseEnv
	if baseEnv == nil {
		baseEnv = envMapFromList(os.Environ())
	}
	env := MergeProcessEnv(baseEnv, BuildLarkChannelEnv(envContext))
	strictMode, defaultAs := IdentityPolicySettings(identityPreset)

	if !runQuiet(ctx, runner, CommandInvocation{
		Command: command,
		Args:    []string{"config", "strict-mode", strictMode},
		Env:     env,
	}, timeout) {
		return false
	}
	return runQuiet(ctx, runner, CommandInvocation{
		Command: command,
		Args:    []string{"config", "default-as", defaultAs},
		Env:     env,
	}, timeout)
}

func MergeProcessEnv(base map[string]string, overrides map[string]string) map[string]string {
	merged := make(map[string]string, len(base)+len(overrides))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range overrides {
		merged[key] = value
	}
	return merged
}

func EnvMapFromList(env []string) map[string]string {
	return envMapFromList(env)
}

func EnvMapToList(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	list := make([]string, 0, len(keys))
	for _, key := range keys {
		list = append(list, key+"="+env[key])
	}
	return list
}

func runQuiet(ctx context.Context, runner CommandRunner, invocation CommandInvocation, timeout time.Duration) bool {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := runner.RunLarkCliCommand(runCtx, invocation); err != nil {
		return false
	}
	return runCtx.Err() == nil
}

type execCommandRunner struct{}

func (execCommandRunner) RunLarkCliCommand(ctx context.Context, invocation CommandInvocation) error {
	cmd := exec.CommandContext(ctx, invocation.Command, invocation.Args...)
	cmd.Env = EnvMapToList(invocation.Env)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func envMapFromList(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		out[key] = value
	}
	return out
}

func hasStructuredLarkCliUserAuth(value reflect.Value) bool {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return false
	}

	switch value.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if hasStructuredLarkCliUserAuth(value.Index(i)) {
				return true
			}
		}
		return false
	case reflect.Map:
		if hasLarkCliUserRecord(value) {
			return true
		}
		iter := value.MapRange()
		for iter.Next() {
			if hasStructuredLarkCliUserAuth(iter.Value()) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func hasLarkCliUserRecord(value reflect.Value) bool {
	if value.Kind() != reflect.Map || value.Type().Key().Kind() != reflect.String {
		return false
	}
	iter := value.MapRange()
	for iter.Next() {
		if _, ok := userOpenIDKeys[iter.Key().String()]; !ok {
			continue
		}
		item := iter.Value()
		for item.IsValid() && (item.Kind() == reflect.Interface || item.Kind() == reflect.Pointer) {
			if item.IsNil() {
				break
			}
			item = item.Elem()
		}
		if item.IsValid() && item.Kind() == reflect.String && strings.TrimSpace(item.String()) != "" {
			return true
		}
	}
	return false
}

func isLarkCliUserDisplayValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if emptyUserDisplayRE.MatchString(trimmed) {
		return false
	}
	if noLoggedInUsersRE.MatchString(trimmed) {
		return false
	}
	return true
}
