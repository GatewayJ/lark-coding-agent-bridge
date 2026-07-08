package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

type codexJsonlCompatFixture struct {
	Name  string                 `json:"name"`
	Steps []codexJsonlCompatStep `json:"steps"`
}

type codexJsonlCompatStep struct {
	Kind            string             `json:"kind"`
	Raw             json.RawMessage    `json:"raw"`
	Reason          CodexFinishReason  `json:"reason"`
	Events          []agent.AgentEvent `json:"events"`
	TerminalEmitted bool               `json:"terminalEmitted"`
	ProtocolDrift   ProtocolDriftState `json:"protocolDrift"`
}

func TestCodexJsonlCompatFixtures(t *testing.T) {
	fixtures := []string{
		"happy-path",
		"raw-error-recovers",
		"turn-failed-terminal",
		"eof-after-error",
		"stop-and-timeout",
		"timeout",
	}
	for _, name := range fixtures {
		fixture := readCodexJsonlCompatFixture(t, name)
		t.Run(fixture.Name, func(t *testing.T) {
			translator := NewCodexJsonlTranslator()
			for idx, step := range fixture.Steps {
				var got []agent.AgentEvent
				switch step.Kind {
				case "translate":
					var raw any
					if err := json.Unmarshal(step.Raw, &raw); err != nil {
						t.Fatalf("step %d raw unmarshal: %v", idx, err)
					}
					got = translator.Translate(raw)
				case "finish":
					got = translator.Finish(step.Reason)
				default:
					t.Fatalf("step %d unknown kind %q", idx, step.Kind)
				}

				assertEvents(t, got, step.Events)
				if translator.TerminalEmitted() != step.TerminalEmitted {
					t.Fatalf("step %d TerminalEmitted = %v, want %v", idx, translator.TerminalEmitted(), step.TerminalEmitted)
				}
				if gotDrift := translator.ProtocolDrift(); gotDrift != step.ProtocolDrift {
					t.Fatalf("step %d ProtocolDrift = %#v, want %#v", idx, gotDrift, step.ProtocolDrift)
				}
			}
		})
	}
}

func readCodexJsonlCompatFixture(t *testing.T, name string) codexJsonlCompatFixture {
	t.Helper()

	path := filepath.Join("..", "..", "..", "testdata", "compat", "codex-jsonl", name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var fixture codexJsonlCompatFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", path, err)
	}
	return fixture
}

func eventsEqual(got []agent.AgentEvent, want []agent.AgentEvent) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	return reflect.DeepEqual(got, want)
}
