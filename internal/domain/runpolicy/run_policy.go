package runpolicy

import (
	"fmt"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/access"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
)

const DefaultTTL = 5 * time.Minute

func Evaluate(input Input) (Result, error) {
	if !input.Access.OK {
		return reject(RejectAccessDenied, "当前用户无权发起运行。"), nil
	}

	for _, binding := range input.Scope.ResourceBindings {
		if binding.Kind == ResourceBindingFolder && !binding.Verified {
			return reject(RejectFolderAllowlistUnverified, "暂不支持 folder allowlist，已拒绝运行。"), nil
		}
	}

	for _, attachment := range input.Attachments {
		if attachment.Requiredness == AttachmentRequired && attachment.Decision != AttachmentAccepted {
			return reject(RejectRequiredAttachment, "必需附件未通过校验，已拒绝运行。"), nil
		}
	}

	accessMode := permissions.ClampAccess(
		input.ProfileConfig.Permissions.DefaultAccess,
		input.ProfileConfig.Permissions.MaxAccess,
		input.Capability.Permissions.MaxAccess,
	)
	sandbox, err := permissions.AccessToCodexSandbox(accessMode)
	if err != nil {
		return Result{}, err
	}
	permissionMode := permissions.AccessToClaudePermissionMode(accessMode, &input.ProfileConfig.Permissions)

	resourceDigest, err := ResourceScopeDigest(input.Scope)
	if err != nil {
		return Result{}, fmt.Errorf("resource scope digest: %w", err)
	}
	attachmentDigest, err := AttachmentPolicyConfigDigest(input.ProfileConfig.Attachments)
	if err != nil {
		return Result{}, fmt.Errorf("attachment policy digest: %w", err)
	}
	accessDigest := CommentMentionAccessDigest(input.Access)
	if accessDigest == "" || input.Scope.Source != SourceComment {
		accessDigest, err = AccessPolicyDigest(input.ProfileConfig.Access)
		if err != nil {
			return Result{}, fmt.Errorf("access policy digest: %w", err)
		}
	}

	codexHome := input.CodexHome
	if codexHome == "" && input.ProfileConfig.Codex != nil {
		codexHome = input.ProfileConfig.Codex.CodexHome
	}
	inheritCodexHome := input.InheritCodexHome
	if !inheritCodexHome && input.ProfileConfig.Codex != nil {
		inheritCodexHome = input.ProfileConfig.Codex.InheritCodexHome
	}
	fingerprint, err := PolicyFingerprint(FingerprintInput{
		CWDRealpath:                 input.CWDRealpath,
		Sandbox:                     sandbox,
		AccessPolicyDigest:          accessDigest,
		ResourceScopeDigest:         resourceDigest,
		AttachmentPolicyShapeDigest: attachmentDigest,
		CodexHome:                   codexHome,
		InheritCodexHome:            inheritCodexHome,
	})
	if err != nil {
		return Result{}, fmt.Errorf("policy fingerprint: %w", err)
	}

	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	ttl := input.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return Result{
		OK: true,
		Allow: Allow{
			Prompt:            input.Prompt,
			RequestedCWD:      input.RequestedCWD,
			CWDRealpath:       input.CWDRealpath,
			AccessMode:        accessMode,
			Sandbox:           sandbox,
			PermissionMode:    permissionMode,
			Access:            input.Access,
			Attachments:       append([]AgentAttachment(nil), input.Attachments...),
			ExpiresAt:         now.Add(ttl),
			PolicyFingerprint: fingerprint,
		},
	}, nil
}

func reject(code RejectCode, userVisible string) Result {
	return Result{
		OK: false,
		RejectReason: RejectReason{
			Code:        code,
			UserVisible: userVisible,
		},
	}
}

func AllowedAccess(reason access.Reason) access.Decision {
	return access.Decision{OK: true, Reason: reason}
}
