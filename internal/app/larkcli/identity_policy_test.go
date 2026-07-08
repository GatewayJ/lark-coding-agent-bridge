package larkcli

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestHasLarkCliUserAuthMatchesStructuredAndDisplaySemantics(t *testing.T) {
	tests := []struct {
		name  string
		users any
		want  bool
	}{
		{
			name: "direct structured open id",
			users: map[string]any{
				"userOpenId": " ou_user ",
			},
			want: true,
		},
		{
			name: "nested structured open id",
			users: map[string]any{
				"tenant": []any{
					map[string]any{"open_id": ""},
					map[string]any{"openId": "ou_user"},
				},
			},
			want: true,
		},
		{name: "display user", users: "Jane Doe <ou_user>", want: true},
		{name: "blank display", users: "  ", want: false},
		{name: "none display", users: "(none)", want: false},
		{name: "localized none display", users: "无", want: false},
		{name: "no logged in users display", users: "(no logged-in users)", want: false},
		{
			name: "non user object",
			users: map[string]any{
				"openId": "  ",
				"items":  []any{map[string]any{"name": "Jane"}},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasLarkCliUserAuth(tt.users); got != tt.want {
				t.Fatalf("HasLarkCliUserAuth(%#v) = %v, want %v", tt.users, got, tt.want)
			}
		})
	}
}

func TestApplyLarkCliIdentityPolicyUserDefaultRunsTwoCommandsWithMergedEnv(t *testing.T) {
	var calls []CommandInvocation
	runner := CommandRunnerFunc(func(ctx context.Context, invocation CommandInvocation) error {
		calls = append(calls, copyInvocation(invocation))
		return nil
	})

	ok := ApplyLarkCliIdentityPolicy(context.Background(), EnvContext{
		Profile:                 "codex-dev",
		RootDir:                 "/tmp/lark-channel",
		LarkCliSourceConfigFile: "/tmp/lark-channel/profiles/codex-dev/lark-cli-source/config.json",
		LarkCliConfigDir:        "/tmp/lark-channel/profiles/codex-dev/lark-cli",
	}, IdentityUserDefault, IdentityPolicyOptions{
		BaseEnv: map[string]string{
			"PATH":         "/bin",
			"LARK_CHANNEL": "0",
		},
		Runner:  runner,
		Timeout: time.Second,
	})
	if !ok {
		t.Fatalf("ApplyLarkCliIdentityPolicy returned false")
	}
	if len(calls) != 2 {
		t.Fatalf("call count = %d, want 2", len(calls))
	}
	if !reflect.DeepEqual(calls[0].Args, []string{"config", "strict-mode", "off"}) {
		t.Fatalf("first args = %#v", calls[0].Args)
	}
	if !reflect.DeepEqual(calls[1].Args, []string{"config", "default-as", "auto"}) {
		t.Fatalf("second args = %#v", calls[1].Args)
	}
	for _, call := range calls {
		if call.Command != "lark-cli" {
			t.Fatalf("command = %q, want lark-cli", call.Command)
		}
		if call.Env["PATH"] != "/bin" {
			t.Fatalf("PATH not preserved in env: %#v", call.Env)
		}
		if call.Env["LARK_CHANNEL"] != "1" ||
			call.Env["LARK_CHANNEL_HOME"] != "/tmp/lark-channel" ||
			call.Env["LARK_CHANNEL_PROFILE"] != "codex-dev" ||
			call.Env["LARK_CHANNEL_CONFIG"] != "/tmp/lark-channel/profiles/codex-dev/lark-cli-source/config.json" ||
			call.Env["LARKSUITE_CLI_CONFIG_DIR"] != "/tmp/lark-channel/profiles/codex-dev/lark-cli" {
			t.Fatalf("lark channel env = %#v", call.Env)
		}
	}
}

func TestApplyLarkCliIdentityPolicyBotOnlyAndFailureShortCircuit(t *testing.T) {
	var calls []CommandInvocation
	runner := CommandRunnerFunc(func(ctx context.Context, invocation CommandInvocation) error {
		calls = append(calls, copyInvocation(invocation))
		return nil
	})

	ok := ApplyLarkCliIdentityPolicy(context.Background(), EnvContext{}, IdentityBotOnly, IdentityPolicyOptions{
		Runner:  runner,
		Timeout: time.Second,
	})
	if !ok {
		t.Fatalf("bot-only policy returned false")
	}
	if len(calls) != 2 {
		t.Fatalf("call count = %d, want 2", len(calls))
	}
	if !reflect.DeepEqual(calls[0].Args, []string{"config", "strict-mode", "bot"}) {
		t.Fatalf("first args = %#v", calls[0].Args)
	}
	if !reflect.DeepEqual(calls[1].Args, []string{"config", "default-as", "bot"}) {
		t.Fatalf("second args = %#v", calls[1].Args)
	}

	calls = nil
	failure := errors.New("exit 1")
	failRunner := CommandRunnerFunc(func(ctx context.Context, invocation CommandInvocation) error {
		calls = append(calls, copyInvocation(invocation))
		return failure
	})
	ok = ApplyLarkCliIdentityPolicy(context.Background(), EnvContext{}, IdentityUserDefault, IdentityPolicyOptions{
		Runner:  failRunner,
		Timeout: time.Second,
	})
	if ok {
		t.Fatalf("policy should return false when strict-mode command fails")
	}
	if len(calls) != 1 {
		t.Fatalf("call count after failure = %d, want 1", len(calls))
	}
}

func copyInvocation(invocation CommandInvocation) CommandInvocation {
	args := append([]string(nil), invocation.Args...)
	env := make(map[string]string, len(invocation.Env))
	for key, value := range invocation.Env {
		env[key] = value
	}
	return CommandInvocation{
		Command: invocation.Command,
		Args:    args,
		Env:     env,
	}
}
