package secretstore

import (
	"encoding/json"
	"fmt"
)

const (
	SourceEnv    = "env"
	SourceFile   = "file"
	SourceInline = "inline"
	SourceExec   = "exec"

	DefaultProvider = "default"
)

type SecretRef struct {
	Source   string `json:"source"`
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id"`
}

type SecretInput struct {
	Plain *string
	Ref   *SecretRef
}

func PlainSecret(value string) SecretInput {
	return SecretInput{Plain: &value}
}

func SecretReference(ref SecretRef) SecretInput {
	return SecretInput{Ref: &ref}
}

func (s SecretInput) IsZero() bool {
	return s.Plain == nil && s.Ref == nil
}

func (s SecretInput) MarshalJSON() ([]byte, error) {
	if s.Ref != nil {
		return json.Marshal(s.Ref)
	}
	if s.Plain != nil {
		return json.Marshal(*s.Plain)
	}
	return []byte("null"), nil
}

func (s *SecretInput) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*s = SecretInput{}
		return nil
	}
	var plain string
	if err := json.Unmarshal(data, &plain); err == nil {
		*s = PlainSecret(plain)
		return nil
	}
	var ref SecretRef
	if err := json.Unmarshal(data, &ref); err == nil && ref.Source != "" {
		*s = SecretReference(ref)
		return nil
	}
	return fmt.Errorf("unsupported secret input JSON: %s", string(data))
}

type AppCredentials struct {
	ID     string      `json:"id"`
	Secret SecretInput `json:"secret"`
	Tenant string      `json:"tenant"`
}

type AccountsConfig struct {
	App AppCredentials `json:"app"`
}

type ProviderConfig struct {
	Source            string            `json:"source"`
	Allowlist         []string          `json:"allowlist,omitempty"`
	Path              string            `json:"path,omitempty"`
	Value             string            `json:"value,omitempty"`
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"args,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	PassEnv           []string          `json:"passEnv,omitempty"`
	NoOutputTimeoutMs int               `json:"noOutputTimeoutMs,omitempty"`
	MaxOutputBytes    int               `json:"maxOutputBytes,omitempty"`
}

type SecretsConfig struct {
	Providers map[string]ProviderConfig `json:"providers,omitempty"`
	Defaults  map[string]string         `json:"defaults,omitempty"`
}

type AppConfig struct {
	Accounts AccountsConfig `json:"accounts"`
	Secrets  *SecretsConfig `json:"secrets,omitempty"`
}

func SecretKeyForApp(appID string) string {
	return "app-" + appID
}
