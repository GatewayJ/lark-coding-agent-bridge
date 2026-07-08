package larkcli

import (
	"path/filepath"
	"strings"
)

type EnvContext struct {
	Profile                 string
	RootDir                 string
	ConfigPath              string
	LarkCliConfigDir        string
	LarkCliSourceConfigFile string
}

func BuildLarkChannelEnv(context EnvContext) map[string]string {
	env := map[string]string{
		"LARK_CHANNEL": "1",
	}
	if profile, ok := nonEmpty(context.Profile); ok {
		env["LARK_CHANNEL_PROFILE"] = profile
	}

	rootDir, hasRootDir := nonEmpty(context.RootDir)
	if hasRootDir {
		env["LARK_CHANNEL_HOME"] = rootDir
	}

	if configPath, ok := nonEmpty(context.LarkCliSourceConfigFile); ok {
		env["LARK_CHANNEL_CONFIG"] = configPath
	} else if configPath, ok := nonEmpty(context.ConfigPath); ok {
		env["LARK_CHANNEL_CONFIG"] = configPath
	} else if hasRootDir {
		env["LARK_CHANNEL_CONFIG"] = filepath.Join(rootDir, "config.json")
	}

	if configDir, ok := nonEmpty(context.LarkCliConfigDir); ok {
		env["LARKSUITE_CLI_CONFIG_DIR"] = configDir
	}

	return env
}

func nonEmpty(value string) (string, bool) {
	if strings.TrimSpace(value) == "" {
		return "", false
	}
	return value, true
}
