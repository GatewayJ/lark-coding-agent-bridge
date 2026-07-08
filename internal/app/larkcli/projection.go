package larkcli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
)

type TenantBrand string

const (
	TenantFeishu TenantBrand = "feishu"
	TenantLark   TenantBrand = "lark"
)

type SecretRef struct {
	Source   string `json:"source"`
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id"`
}

type AppCredentials struct {
	ID     string      `json:"id"`
	Secret any         `json:"secret"`
	Tenant TenantBrand `json:"tenant"`
}

type AccountsConfig struct {
	App AppCredentials `json:"app"`
}

type ProviderConfig map[string]any

type SecretsConfig struct {
	Providers map[string]ProviderConfig `json:"providers,omitempty"`
	Defaults  map[string]string         `json:"defaults,omitempty"`
}

type AppConfig struct {
	Accounts AccountsConfig `json:"accounts"`
	Secrets  *SecretsConfig `json:"secrets,omitempty"`
}

type ProjectionPaths struct {
	RootDir                  string
	Profile                  string
	LarkCliSourceDir         string
	LarkCliSourceConfigFile  string
	SecretsGetterScript      string
	SecretsGetterCommand     string
	SecretsGetterNodePath    string
	SecretsGetterBridgeEntry string
}

type SourceProjection struct {
	Accounts AccountsConfig `json:"accounts"`
	Secrets  *SecretsConfig `json:"secrets,omitempty"`
}

func WriteLarkCliSourceProjection(cfg AppConfig, paths ProjectionPaths) (string, error) {
	if err := os.MkdirAll(paths.LarkCliSourceDir, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(paths.LarkCliSourceDir, 0o700)
	if _, ok := BridgeProviderName(cfg.Accounts.App.Secret); ok {
		if paths.SecretsGetterCommand == "" && (paths.SecretsGetterNodePath == "" || paths.SecretsGetterBridgeEntry == "") {
			return "", errors.New("lark-cli projection requires SecretsGetterCommand or SecretsGetterNodePath and SecretsGetterBridgeEntry for exec secrets")
		}
		if _, err := EnsureSecretsGetterWrapper(SecretsGetterWrapperOptions{
			RootDir:             paths.RootDir,
			SecretsGetterScript: paths.SecretsGetterScript,
			DirectCommand:       paths.SecretsGetterCommand,
			NodePath:            paths.SecretsGetterNodePath,
			BridgeEntry:         paths.SecretsGetterBridgeEntry,
		}); err != nil {
			return "", err
		}
	}

	projection := BuildLarkCliSourceProjection(cfg, paths)
	data, err := json.MarshalIndent(projection, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := writeFileAtomic(paths.LarkCliSourceConfigFile, data, 0o600); err != nil {
		return "", err
	}
	return paths.LarkCliSourceConfigFile, nil
}

func BuildLarkCliSourceProjection(cfg AppConfig, paths ProjectionPaths) SourceProjection {
	secrets := buildProjectionSecrets(cfg, paths)
	return SourceProjection{
		Accounts: AccountsConfig{
			App: AppCredentials{
				ID:     cfg.Accounts.App.ID,
				Secret: cfg.Accounts.App.Secret,
				Tenant: cfg.Accounts.App.Tenant,
			},
		},
		Secrets: secrets,
	}
}

func buildProjectionSecrets(cfg AppConfig, paths ProjectionPaths) *SecretsConfig {
	providers := map[string]ProviderConfig{}
	defaults := map[string]string{}
	if cfg.Secrets != nil {
		for name, provider := range cfg.Secrets.Providers {
			providers[name] = copyProvider(provider)
		}
		for name, value := range cfg.Secrets.Defaults {
			defaults[name] = value
		}
	}

	if providerName, ok := BridgeProviderName(cfg.Accounts.App.Secret); ok {
		existing := copyProvider(providers[providerName])
		existing["source"] = "exec"
		existing["command"] = SecretsGetterWrapperPath(paths.SecretsGetterScript)
		existing["args"] = []string{}

		env := providerEnv(existing["env"])
		env["LARK_CHANNEL_HOME"] = paths.RootDir
		env["LARK_CHANNEL_PROFILE"] = paths.Profile
		existing["env"] = env

		providers[providerName] = existing
	}

	if len(providers) == 0 && len(defaults) == 0 {
		return nil
	}
	secrets := &SecretsConfig{}
	if len(defaults) > 0 {
		secrets.Defaults = defaults
	}
	if len(providers) > 0 {
		secrets.Providers = providers
	}
	return secrets
}

func BridgeProviderName(secret any) (string, bool) {
	switch ref := secret.(type) {
	case SecretRef:
		return bridgeProviderNameFromFields(ref.Source, ref.Provider)
	case *SecretRef:
		if ref == nil {
			return "", false
		}
		return bridgeProviderNameFromFields(ref.Source, ref.Provider)
	case map[string]any:
		source, _ := ref["source"].(string)
		provider, _ := ref["provider"].(string)
		return bridgeProviderNameFromFields(source, provider)
	case map[string]string:
		return bridgeProviderNameFromFields(ref["source"], ref["provider"])
	default:
		return bridgeProviderNameReflect(secret)
	}
}

func SecretsGetterWrapperPath(script string) string {
	if runtime.GOOS == "windows" {
		return script + ".cmd"
	}
	return script
}

type SecretsGetterWrapperOptions struct {
	RootDir             string
	SecretsGetterScript string
	Platform            string
	DirectCommand       string
	NodePath            string
	BridgeEntry         string
}

func EnsureSecretsGetterWrapper(options SecretsGetterWrapperOptions) (string, error) {
	platform := options.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	wrapperPath := options.SecretsGetterScript
	if platform == "windows" {
		wrapperPath += ".cmd"
	}
	rootDir := options.RootDir
	if rootDir == "" {
		rootDir = filepath.Dir(options.SecretsGetterScript)
	}
	if options.DirectCommand != "" {
		return wrapperPath, writeDirectSecretsGetterWrapper(wrapperPath, rootDir, options)
	}
	node := options.NodePath
	if node == "" {
		node = "node"
	}
	bridgeEntry := options.BridgeEntry
	if bridgeEntry == "" {
		bridgeEntry = "lark-channel-bridge"
	}

	var content string
	mode := os.FileMode(0o700)
	if platform == "windows" {
		content = "@echo off\r\n" +
			"rem Auto-generated by lark-channel-bridge. Do not edit.\r\n" +
			`set "LARK_CHANNEL_HOME=` + strings.ReplaceAll(rootDir, `"`, `""`) + "\"\r\n" +
			dq(node) + " " + dq(bridgeEntry) + " secrets get %*\r\n"
		mode = 0o600
	} else {
		content = "#!/bin/sh\n" +
			"# Auto-generated by lark-channel-bridge. Do not edit.\n" +
			"# Forwards exec-provider requests to: node bridge secrets get\n" +
			"LARK_CHANNEL_HOME=" + sq(rootDir) + " exec " + sq(node) + " " + sq(bridgeEntry) + " secrets get \"$@\"\n"
	}
	return wrapperPath, writeFileAtomic(wrapperPath, []byte(content), mode)
}

func writeDirectSecretsGetterWrapper(wrapperPath string, rootDir string, options SecretsGetterWrapperOptions) error {
	platform := options.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	mode := os.FileMode(0o700)
	var content string
	if platform == "windows" {
		content = "@echo off\r\n" +
			"rem Auto-generated by lark-channel-bridge. Do not edit.\r\n" +
			`set "LARK_CHANNEL_HOME=` + strings.ReplaceAll(rootDir, `"`, `""`) + "\"\r\n" +
			dq(options.DirectCommand) + " secrets get %*\r\n"
		mode = 0o600
	} else {
		content = "#!/bin/sh\n" +
			"# Auto-generated by lark-channel-bridge. Do not edit.\n" +
			"# Forwards exec-provider requests to: bridge secrets get\n" +
			"LARK_CHANNEL_HOME=" + sq(rootDir) + " exec " + sq(options.DirectCommand) + " secrets get \"$@\"\n"
	}
	return writeFileAtomic(wrapperPath, []byte(content), mode)
}

func sq(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func dq(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func bridgeProviderNameFromFields(source string, provider string) (string, bool) {
	if source != "exec" {
		return "", false
	}
	if provider == "" {
		return "default", true
	}
	return provider, true
}

func bridgeProviderNameReflect(secret any) (string, bool) {
	value := reflect.ValueOf(secret)
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return "", false
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Map || value.Type().Key().Kind() != reflect.String {
		return "", false
	}
	var source, provider string
	iter := value.MapRange()
	for iter.Next() {
		key := iter.Key().String()
		item := iter.Value()
		for item.IsValid() && (item.Kind() == reflect.Interface || item.Kind() == reflect.Pointer) {
			if item.IsNil() {
				break
			}
			item = item.Elem()
		}
		if !item.IsValid() || item.Kind() != reflect.String {
			continue
		}
		switch key {
		case "source":
			source = item.String()
		case "provider":
			provider = item.String()
		}
	}
	return bridgeProviderNameFromFields(source, provider)
}

func copyProvider(provider ProviderConfig) ProviderConfig {
	out := ProviderConfig{}
	for key, value := range provider {
		out[key] = value
	}
	return out
}

func providerEnv(value any) map[string]string {
	env := map[string]string{}
	switch typed := value.(type) {
	case map[string]string:
		for key, item := range typed {
			env[key] = item
		}
	case map[string]any:
		for key, item := range typed {
			if text, ok := item.(string); ok {
				env[key] = text
			}
		}
	default:
		value := reflect.ValueOf(value)
		for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
			if value.IsNil() {
				return env
			}
			value = value.Elem()
		}
		if !value.IsValid() || value.Kind() != reflect.Map || value.Type().Key().Kind() != reflect.String {
			return env
		}
		iter := value.MapRange()
		for iter.Next() {
			item := iter.Value()
			for item.IsValid() && (item.Kind() == reflect.Interface || item.Kind() == reflect.Pointer) {
				if item.IsNil() {
					break
				}
				item = item.Elem()
			}
			if item.IsValid() && item.Kind() == reflect.String {
				env[iter.Key().String()] = item.String()
			}
		}
	}
	return env
}
