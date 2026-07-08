package service

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/compat/apppaths"
)

const baseServiceName = "lark-channel-bridge.bot"

var serviceProfileRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func ServiceProfileID(profile string) (string, error) {
	trimmed := strings.TrimSpace(profile)
	if trimmed == "" {
		return "", fmt.Errorf("profile name is required for service id")
	}
	if trimmed == "." || trimmed == ".." || !serviceProfileRE.MatchString(trimmed) {
		return "", fmt.Errorf("invalid profile name: %s", profile)
	}
	return trimmed, nil
}

func ServiceNameForProfile(profile string) (string, error) {
	id, err := ServiceProfileID(profile)
	if err != nil {
		return "", err
	}
	return baseServiceName + "." + id, nil
}

func LaunchAgentLabel(profile string) (string, error) {
	name, err := ServiceNameForProfile(profile)
	if err != nil {
		return "", err
	}
	return "ai." + name, nil
}

func LaunchAgentPlistPath(profile string) (string, error) {
	label, err := LaunchAgentLabel(profile)
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

func SystemdUnitName(profile string) (string, error) {
	name, err := ServiceNameForProfile(profile)
	if err != nil {
		return "", err
	}
	return name + ".service", nil
}

func SystemdUnitPath(profile string) (string, error) {
	name, err := SystemdUnitName(profile)
	if err != nil {
		return "", err
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", name), nil
}

func WindowsTaskName(profile string) (string, error) {
	id, err := ServiceProfileID(profile)
	if err != nil {
		return "", err
	}
	return "LarkChannelBridge.Bot." + id, nil
}

func WindowsLauncherCmdPath(rootDir string, profile string) (string, error) {
	id, err := ServiceProfileID(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(rootDir, "daemon", id, "launcher.cmd"), nil
}

func DaemonLogDir(rootDir string, profile string) (string, error) {
	paths, err := apppaths.Resolve(apppaths.Options{RootDir: rootDir, Profile: profile})
	if err != nil {
		return "", err
	}
	return filepath.Join(paths.LogsDir, "daemon"), nil
}

func DaemonStdoutPath(rootDir string, profile string) (string, error) {
	dir, err := DaemonLogDir(rootDir, profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon-stdout.log"), nil
}

func DaemonStderrPath(rootDir string, profile string) (string, error) {
	dir, err := DaemonLogDir(rootDir, profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon-stderr.log"), nil
}
