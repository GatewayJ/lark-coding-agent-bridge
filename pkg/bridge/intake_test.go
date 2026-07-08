package bridge

import "testing"

func TestLarkIntakeFacadeNormalizesMessageScope(t *testing.T) {
	normalizer := NewLarkIntakeNormalizer(DefaultLarkSelfLoopPolicy("ou_bot"))
	event := normalizer.NormalizeMessage(LarkMessageInput{
		ChatID:       "oc_group",
		ChatType:     LarkChatTypeGroup,
		ResolvedMode: LarkChatModeGroup,
		ThreadID:     "omt_topic",
		Sender:       LarkActor{OpenID: "ou_user"},
	})

	if event.Kind != LarkEventMessage {
		t.Fatalf("kind = %q", event.Kind)
	}
	if event.Scope.Key != "oc_group:omt_topic" {
		t.Fatalf("scope = %q", event.Scope.Key)
	}
	if event.Self.Drop {
		t.Fatalf("unexpected self drop: %#v", event.Self)
	}
}
