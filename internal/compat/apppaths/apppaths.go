package apppaths

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const defaultProfile = "claude"

var profileNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Paths mirrors the JavaScript resolveAppPaths contract. Keep this package
// focused on path compatibility; filesystem creation and locking live elsewhere.
type Paths struct {
	RootDir                 string
	Profile                 string
	ProfileDir              string
	DefaultWorkspaceDir     string
	ConfigFile              string
	ActiveProfileFile       string
	SessionsFile            string
	WorkspacesFile          string
	SecretsFile             string
	KeystoreSaltFile        string
	SecretsGetterScript     string
	LarkCliConfigDir        string
	LarkCliSourceDir        string
	LarkCliSourceConfigFile string
	LarkCliTargetConfigFile string
	MediaDir                string
	LogsDir                 string
	RegistryDir             string
	UserRegistryFile        string
	UserLockDir             string
	ProfileLockFile         string
}

// Options controls path resolution. Empty RootDir keeps parity with the JS
// implementation: LARK_CHANNEL_HOME first, then ~/.lark-channel.
type Options struct {
	RootDir string
	Profile string
}

func Resolve(opts Options) (Paths, error) {
	rootDir, err := resolveRootDir(opts.RootDir)
	if err != nil {
		return Paths{}, err
	}
	profile, err := normalizeProfileName(firstNonEmpty(opts.Profile, defaultProfile))
	if err != nil {
		return Paths{}, err
	}

	profileDir := filepath.Join(rootDir, "profiles", profile)
	registryDir := filepath.Join(rootDir, "registry")
	userLockDir := filepath.Join(registryDir, "locks")
	larkCliSourceDir := filepath.Join(profileDir, "lark-cli-source")
	larkCliConfigDir := filepath.Join(profileDir, "lark-cli")

	return Paths{
		RootDir:                 rootDir,
		Profile:                 profile,
		ProfileDir:              profileDir,
		DefaultWorkspaceDir:     filepath.Join(rootDir+"-workspaces", profile, "default"),
		ConfigFile:              filepath.Join(rootDir, "config.json"),
		ActiveProfileFile:       filepath.Join(rootDir, "active-profile"),
		SessionsFile:            filepath.Join(profileDir, "sessions.json"),
		WorkspacesFile:          filepath.Join(profileDir, "workspaces.json"),
		SecretsFile:             filepath.Join(profileDir, "secrets.enc"),
		KeystoreSaltFile:        filepath.Join(profileDir, ".keystore.salt"),
		SecretsGetterScript:     filepath.Join(rootDir, "secrets-getter"),
		LarkCliConfigDir:        larkCliConfigDir,
		LarkCliSourceDir:        larkCliSourceDir,
		LarkCliSourceConfigFile: filepath.Join(larkCliSourceDir, "config.json"),
		LarkCliTargetConfigFile: filepath.Join(larkCliConfigDir, "lark-channel", "config.json"),
		MediaDir:                filepath.Join(profileDir, "media"),
		LogsDir:                 filepath.Join(profileDir, "logs"),
		RegistryDir:             registryDir,
		UserRegistryFile:        filepath.Join(registryDir, "processes.json"),
		UserLockDir:             userLockDir,
		ProfileLockFile:         filepath.Join(userLockDir, "profile", profile+".lock"),
	}, nil
}

func (p Paths) AppLockFile(appID string) string {
	return filepath.Join(p.UserLockDir, "app", lockSafeName(appID)+".lock")
}

func normalizeProfileName(profile string) (string, error) {
	trimmed := strings.TrimSpace(profile)
	if trimmed == "" {
		return "", fmt.Errorf("profile name is required")
	}
	if trimmed == "." || trimmed == ".." || !profileNameRE.MatchString(trimmed) {
		return "", fmt.Errorf("invalid profile name: %s", profile)
	}
	return trimmed, nil
}

func resolveRootDir(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if env := os.Getenv("LARK_CHANNEL_HOME"); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lark-channel"), nil
}

func lockSafeName(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') ||
			(r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-' {
			out.WriteRune(r)
			continue
		}
		out.WriteByte('_')
	}
	return out.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
