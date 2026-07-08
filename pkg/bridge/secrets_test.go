package bridge

import (
	"context"
	"path/filepath"
	"testing"
)

func TestBridgeSecretsFacadeResolvesWithoutFeishuSDK(t *testing.T) {
	root := t.TempDir()
	paths := KeystorePaths{
		SecretsFile:         filepath.Join(root, "profiles", "claude", "secrets.enc"),
		KeystoreSaltFile:    filepath.Join(root, "profiles", "claude", ".keystore.salt"),
		SecretsGetterScript: filepath.Join(root, "secrets-getter"),
	}
	store, err := NewKeystore(KeystoreOptions{Paths: paths, Seed: "host|user"})
	if err != nil {
		t.Fatalf("NewKeystore returned error: %v", err)
	}
	if err := store.SetSecret(SecretKeyForApp("cli_facade"), "facade-secret"); err != nil {
		t.Fatalf("SetSecret returned error: %v", err)
	}
	cfg := SecretAppConfig{
		Accounts: SecretAccountsConfig{
			App: SecretAppCredentials{
				ID:     "cli_facade",
				Tenant: "feishu",
				Secret: SecretReference(SecretRef{
					Source:   SecretSourceExec,
					Provider: "bridge",
					ID:       SecretKeyForApp("cli_facade"),
				}),
			},
		},
		Secrets: &SecretSecretsConfig{Providers: map[string]SecretProviderConfig{
			"bridge": {Source: SecretSourceExec, Command: paths.SecretsGetterScript},
		}},
	}
	got, err := ResolveAppSecret(context.Background(), cfg, SecretResolverOptions{Keystore: store, Paths: paths})
	if err != nil {
		t.Fatalf("ResolveAppSecret returned error: %v", err)
	}
	if got != "facade-secret" {
		t.Fatalf("ResolveAppSecret = %q, want facade-secret", got)
	}
}
