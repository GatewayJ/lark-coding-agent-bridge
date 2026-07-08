package configstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNormalizeConfigMatchesTypeScriptOracle(t *testing.T) {
	tests := []struct {
		name    string
		options LoadOptions
	}{
		{name: "minimal-root"},
		{name: "codex-profile"},
		{name: "legacy-single-profile", options: LoadOptions{Profile: "claude"}},
		{name: "active-profile"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := readFixture(t, tt.name+".input.json")
			expected := readFixture(t, tt.name+".expected.json")

			snapshot, err := Normalize(input, tt.options)
			if err != nil {
				t.Fatalf("Normalize returned error: %v", err)
			}
			assertJSONEqual(t, mustJSON(t, snapshot.Root), expected)
		})
	}
}

func TestLoadConfigUsesActiveProfileSidecar(t *testing.T) {
	root := t.TempDir()
	configFile := filepath.Join(root, "config.json")
	if err := os.WriteFile(configFile, readFixture(t, "active-profile.input.json"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "active-profile"), []byte("codex\n"), 0o600); err != nil {
		t.Fatalf("write active-profile: %v", err)
	}

	snapshot, err := Load(configFile, LoadOptions{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if snapshot.ProfileName != "codex" {
		t.Fatalf("ProfileName = %q, want codex", snapshot.ProfileName)
	}
	if snapshot.Profile.AgentKind != AgentCodex {
		t.Fatalf("Profile.AgentKind = %q, want %q", snapshot.Profile.AgentKind, AgentCodex)
	}
	if snapshot.Runtime.Secrets == nil || snapshot.Runtime.Secrets.Defaults["env"] != "default" {
		t.Fatalf("Runtime.Secrets = %#v, want root secrets inherited", snapshot.Runtime.Secrets)
	}
}

func TestNormalizePreservesUnknownRootAndProfileFields(t *testing.T) {
	input := []byte(`{
  "schemaVersion": 2,
  "activeProfile": "claude",
  "futureRoot": {"keep": true},
  "profiles": {
    "claude": {
      "schemaVersion": 2,
      "agentKind": "claude",
      "futureProfile": ["keep"],
      "accounts": {"app": {"id": "cli_extra", "secret": "secret", "tenant": "feishu"}}
    }
  }
}`)

	snapshot, err := Normalize(input, LoadOptions{})
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	if string(snapshot.Root.Extra["futureRoot"]) != `{"keep": true}` {
		t.Fatalf("root extra = %s", snapshot.Root.Extra["futureRoot"])
	}
	if string(snapshot.Profile.Extra["futureProfile"]) != `["keep"]` {
		t.Fatalf("profile extra = %s", snapshot.Profile.Extra["futureProfile"])
	}
	encoded := mustJSON(t, snapshot.Root)
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("encoded JSON invalid: %v", err)
	}
	if _, ok := got["futureRoot"]; !ok {
		t.Fatalf("encoded root lost futureRoot: %s", encoded)
	}
	profile := got["profiles"].(map[string]any)["claude"].(map[string]any)
	if _, ok := profile["futureProfile"]; !ok {
		t.Fatalf("encoded profile lost futureProfile: %s", encoded)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "compat", "config", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func assertJSONEqual(t *testing.T, got []byte, want []byte) {
	t.Helper()
	var gotValue any
	var wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("got JSON invalid: %v\n%s", err, got)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("want JSON invalid: %v\n%s", err, want)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		gotPretty, _ := json.MarshalIndent(gotValue, "", "  ")
		wantPretty, _ := json.MarshalIndent(wantValue, "", "  ")
		t.Fatalf("JSON mismatch\ngot:\n%s\nwant:\n%s", gotPretty, wantPretty)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return data
}
