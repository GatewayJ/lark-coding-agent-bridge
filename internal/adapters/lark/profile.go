package lark

import (
	"context"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/larkcli"
)

type LarkCLIProjectionHook struct {
	options LarkCLIProjectionHookOptions
}

func NewLarkCLIProjectionHook(options LarkCLIProjectionHookOptions) LarkCLIProjectionHook {
	return LarkCLIProjectionHook{options: options}
}

func (h LarkCLIProjectionHook) ProjectLarkProfile(ctx context.Context, req ProfileProjectionRequest) (ProfileProjectionResult, error) {
	path := h.options.Env.LarkCliSourceConfigFile
	if h.options.Paths.LarkCliSourceConfigFile != "" {
		var err error
		path, err = larkcli.WriteLarkCliSourceProjection(h.options.Config, h.options.Paths)
		if err != nil {
			return ProfileProjectionResult{}, err
		}
	}

	envContext := h.options.Env
	if envContext.LarkCliSourceConfigFile == "" {
		envContext.LarkCliSourceConfigFile = path
	}
	if envContext.RootDir == "" {
		envContext.RootDir = h.options.Paths.RootDir
	}
	if envContext.Profile == "" {
		envContext.Profile = h.options.Paths.Profile
	}
	env := larkcli.BuildLarkChannelEnv(envContext)

	applied := false
	if h.options.ApplyIdentityPolicy {
		applied = larkcli.ApplyLarkCliIdentityPolicy(
			ctx,
			envContext,
			h.options.IdentityPreset,
			h.options.IdentityOptions,
		)
	}

	return ProfileProjectionResult{
		LarkCliSourceConfigFile: path,
		LarkChannelEnv:          env,
		IdentityPolicyApplied:   applied,
		BotIdentity:             req.BotIdentity,
	}, nil
}
