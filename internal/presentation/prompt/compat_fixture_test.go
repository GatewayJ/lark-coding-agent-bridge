package prompt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type promptCompatFixture struct {
	Name                   string                `json:"name"`
	Input                  BuildAgentPromptInput `json:"input"`
	ExpectedPrompt         string                `json:"expectedPrompt"`
	ExpectedSystemPrompt   string                `json:"expectedSystemPrompt"`
	ExpectedIdentityPrompt string                `json:"expectedIdentityPrompt"`
	ExpectedPrefixedPrompt string                `json:"expectedPrefixedPrompt"`
}

func TestPromptCompatFixtures(t *testing.T) {
	for _, name := range []string{"complete", "minimal"} {
		fixture := readPromptCompatFixture(t, name)
		t.Run(fixture.Name, func(t *testing.T) {
			if got := BuildAgentPrompt(fixture.Input); got != fixture.ExpectedPrompt {
				t.Fatalf("BuildAgentPrompt mismatch\nwant:\n%s\n\ngot:\n%s", fixture.ExpectedPrompt, got)
			}
			if got := BRIDGE_SYSTEM_PROMPT; got != fixture.ExpectedSystemPrompt {
				t.Fatalf("BRIDGE_SYSTEM_PROMPT mismatch")
			}
			if fixture.ExpectedIdentityPrompt != "" {
				identity := &AgentBotIdentity{OpenID: "ou_bot_self", Name: "尼莫"}
				if got := BuildBridgeSystemPrompt(identity); got != fixture.ExpectedIdentityPrompt {
					t.Fatalf("BuildBridgeSystemPrompt mismatch")
				}
				if got := PrefixBridgeSystemPrompt(fixture.ExpectedPrompt, identity); got != fixture.ExpectedPrefixedPrompt {
					t.Fatalf("PrefixBridgeSystemPrompt mismatch")
				}
			}
		})
	}
}

func readPromptCompatFixture(t *testing.T, name string) promptCompatFixture {
	t.Helper()

	path := filepath.Join("..", "..", "..", "testdata", "compat", "prompt", name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var fixture promptCompatFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", path, err)
	}
	return fixture
}
