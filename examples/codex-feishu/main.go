package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	bridge "github.com/zarazhangrui/lark-coding-agent-bridge/pkg/bridge"
)

const (
	defaultProfile = "codex"
	defaultTenant  = bridge.LarkCLITenantFeishu
)

func main() {
	log.SetFlags(0)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	appID := strings.TrimSpace(os.Getenv("LARK_APP_ID"))
	if appID == "" {
		return fmt.Errorf("missing LARK_APP_ID")
	}
	if strings.TrimSpace(os.Getenv("LARK_APP_SECRET")) == "" {
		return fmt.Errorf("missing LARK_APP_SECRET")
	}

	home := envOr("LARK_CHANNEL_EXAMPLE_HOME", filepath.Join(".", ".lark-channel-codex-feishu"))
	profile := envOr("LARK_CHANNEL_EXAMPLE_PROFILE", defaultProfile)
	workspace := envOr("LARK_CHANNEL_EXAMPLE_WORKSPACE", filepath.Join(home, "workspace"))
	logDir := strings.TrimSpace(os.Getenv("LARK_CHANNEL_EXAMPLE_LOG_DIR"))
	allowedUsers := envCSV("LARK_CHANNEL_EXAMPLE_ALLOWED_USERS")
	ownerOpenID := strings.TrimSpace(os.Getenv("LARK_CHANNEL_EXAMPLE_OWNER_OPEN_ID"))
	tenant, err := tenantFromEnv()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return err
	}

	configPath := filepath.Join(home, "config.json")
	if err := bootstrapIfNeeded(configPath, home, profile, workspace, appID, tenant, allowedUsers, ownerOpenID); err != nil {
		return err
	}
	if len(allowedUsers) == 0 && ownerOpenID == "" {
		log.Printf("warning: no allowed users were provided; DMs may be ignored unless owner lookup succeeds")
	}

	instance, info, err := bridge.NewProfileBridge(ctx, bridge.ProfileBridgeOptions{
		Home:                    home,
		Profile:                 profile,
		AppID:                   appID,
		Tenant:                  string(tenant),
		InitialOwnerOpenID:      ownerOpenID,
		LogDir:                  logDir,
		SkipCheckLarkCLI:        envBool("LARK_CHANNEL_EXAMPLE_SKIP_LARK_CLI"),
		DisableDefaultTelemetry: true,
	})
	if err != nil {
		return err
	}

	if err := instance.Start(ctx); err != nil {
		return err
	}
	defer shutdown(instance)

	status, err := instance.Status(ctx)
	if err != nil {
		return err
	}
	bot := firstNonEmpty(status.Lark.BotName, status.Lark.BotOpenID, "(unknown)")
	log.Printf("bridge running: profile=%s app=%s bot=%s", info.Profile, info.AppID, bot)
	if logDir != "" {
		log.Printf("bridge logs: %s", logDir)
	}
	log.Printf("send a DM to the bot, or @ it in an allowed group. Press Ctrl+C to stop.")

	<-ctx.Done()
	return nil
}

func bootstrapIfNeeded(configPath, home, profile, workspace, appID string, tenant bridge.LarkCLITenantBrand, allowedUsers []string, ownerOpenID string) error {
	if _, err := os.Stat(configPath); err == nil {
		log.Printf("using existing profile config: %s", configPath)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	_, err := bridge.BootstrapProfileConfig(bridge.BootstrapProfileOptions{
		RootDir:          home,
		Profile:          profile,
		AgentKind:        bridge.ConfigAgentCodex,
		AppID:            appID,
		AppSecret:        bridge.SecretReference(bridge.SecretRef{Source: bridge.SecretSourceEnv, ID: "LARK_APP_SECRET"}),
		Tenant:           tenant,
		DefaultWorkspace: workspace,
		Access: bridge.ConfigProfileAccess{
			AllowedUsers: allowedUsers,
			Admins:       envValueList(ownerOpenID),
		},
	})
	if err != nil {
		return err
	}
	log.Printf("created profile config: %s", configPath)
	return nil
}

func tenantFromEnv() (bridge.LarkCLITenantBrand, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LARK_TENANT"))) {
	case "", "feishu":
		return defaultTenant, nil
	case "lark":
		return bridge.LarkCLITenantLark, nil
	default:
		return "", fmt.Errorf("LARK_TENANT must be feishu or lark")
	}
}

func shutdown(instance *bridge.Bridge) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := instance.Shutdown(ctx); err != nil {
		log.Printf("shutdown failed: %v", err)
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envCSV(name string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func envValueList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
