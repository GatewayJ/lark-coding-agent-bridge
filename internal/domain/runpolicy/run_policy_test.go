package runpolicy

import (
	"regexp"
	"testing"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/access"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/capability"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
)

func TestEvaluateRejectsDeniedAccess(t *testing.T) {
	result, err := Evaluate(baseInput())
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("base policy was rejected: %#v", result.RejectReason)
	}

	input := baseInput()
	input.Access = access.Decision{OK: false, Reason: access.ReasonDeniedUser}
	result, err = Evaluate(input)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if result.OK || result.RejectReason.Code != RejectAccessDenied {
		t.Fatalf("Evaluate = %#v, want access-denied", result)
	}
}

func TestEvaluateMapsAccessModes(t *testing.T) {
	tests := []struct {
		accessMode     permissions.AccessMode
		sandbox        permissions.CodexSandboxMode
		permissionMode permissions.ClaudePermissionMode
	}{
		{permissions.AccessFull, permissions.CodexSandboxDangerFullAccess, permissions.ClaudePermissionBypassPermissions},
		{permissions.AccessWorkspace, permissions.CodexSandboxWorkspaceWrite, permissions.ClaudePermissionAcceptEdits},
		{permissions.AccessReadOnly, permissions.CodexSandboxReadOnly, permissions.ClaudePermissionPlan},
	}

	for _, tt := range tests {
		t.Run(string(tt.accessMode), func(t *testing.T) {
			input := baseInput()
			input.ProfileConfig.Permissions = permissions.PermissionConfig{
				DefaultAccess: tt.accessMode,
				MaxAccess:     tt.accessMode,
			}
			input.Capability = capability.Claude(tt.accessMode, "")
			result, err := Evaluate(input)
			if err != nil {
				t.Fatalf("Evaluate returned error: %v", err)
			}
			if !result.OK {
				t.Fatalf("Evaluate rejected policy: %#v", result.RejectReason)
			}
			if result.Allow.AccessMode != tt.accessMode || result.Allow.Sandbox != tt.sandbox || result.Allow.PermissionMode != tt.permissionMode {
				t.Fatalf("Allow = %#v, want access=%q sandbox=%q permission=%q", result.Allow, tt.accessMode, tt.sandbox, tt.permissionMode)
			}
		})
	}
}

func TestEvaluateDoesNotRaiseAboveCapabilityMax(t *testing.T) {
	input := baseInput()
	input.ProfileConfig.Permissions = permissions.PermissionConfig{
		DefaultAccess: permissions.AccessWorkspace,
		MaxAccess:     permissions.AccessWorkspace,
	}
	input.Capability = capability.Codex(permissions.AccessReadOnly, "")

	result, err := Evaluate(input)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("Evaluate rejected policy: %#v", result.RejectReason)
	}
	if result.Allow.AccessMode != permissions.AccessReadOnly || result.Allow.Sandbox != permissions.CodexSandboxReadOnly {
		t.Fatalf("Allow = %#v, want read-only clamp", result.Allow)
	}
}

func TestEvaluateFingerprintStableAndAttachmentConfigScoped(t *testing.T) {
	input := baseInput()
	result, err := Evaluate(input)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("Evaluate rejected policy: %#v", result.RejectReason)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`).MatchString(result.Allow.PolicyFingerprint) {
		t.Fatalf("policy fingerprint has unexpected shape: %q", result.Allow.PolicyFingerprint)
	}

	withImage := input
	withImage.Attachments = []AgentAttachment{{
		Kind:         "image",
		Requiredness: AttachmentOptional,
		Decision:     AttachmentAccepted,
		OriginalName: "sensitive.png",
		Size:         123,
		Hash:         "hash-a",
		Path:         "/cache/hash-a.png",
	}}
	imageResult, err := Evaluate(withImage)
	if err != nil {
		t.Fatalf("Evaluate with image returned error: %v", err)
	}
	if imageResult.Allow.PolicyFingerprint != result.Allow.PolicyFingerprint {
		t.Fatalf("accepted concrete attachment changed fingerprint: %q != %q", imageResult.Allow.PolicyFingerprint, result.Allow.PolicyFingerprint)
	}

	stricter := input
	stricter.ProfileConfig.Attachments.MaxFileBytes = 1
	stricterResult, err := Evaluate(stricter)
	if err != nil {
		t.Fatalf("Evaluate stricter returned error: %v", err)
	}
	if stricterResult.Allow.PolicyFingerprint == result.Allow.PolicyFingerprint {
		t.Fatalf("attachment config change did not change fingerprint")
	}
}

func TestEvaluateFailsClosedForFolderAndRequiredAttachment(t *testing.T) {
	input := baseInput()
	input.Scope.ResourceBindings = []ResourceBinding{{Kind: ResourceBindingFolder, ID: "fld_secret", Verified: false}}
	result, err := Evaluate(input)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if result.OK || result.RejectReason.Code != RejectFolderAllowlistUnverified {
		t.Fatalf("Evaluate = %#v, want folder allowlist rejection", result)
	}

	input = baseInput()
	input.Attachments = []AgentAttachment{{Kind: "file", Requiredness: AttachmentRequired, Decision: AttachmentRejected}}
	result, err = Evaluate(input)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if result.OK || result.RejectReason.Code != RejectRequiredAttachment {
		t.Fatalf("Evaluate = %#v, want required attachment rejection", result)
	}
}

func baseInput() Input {
	cfg := profile.DefaultConfig(profile.AgentClaude)
	return Input{
		Scope: ScopeContext{
			Source:  SourceIM,
			ChatID:  "oc_chat",
			ActorID: "ou_user",
		},
		Prompt:        "hello",
		RequestedCWD:  "/repo/project",
		CWDRealpath:   "/repo/project",
		Access:        access.Decision{OK: true, Reason: access.ReasonAllowedUser},
		Capability:    capability.Claude(cfg.Permissions.MaxAccess, ""),
		ProfileConfig: cfg,
		Now:           time.UnixMilli(1000),
	}
}
