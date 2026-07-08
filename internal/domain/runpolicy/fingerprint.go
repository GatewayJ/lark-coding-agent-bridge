package runpolicy

import (
	"crypto/sha256"
	"encoding/base64"
	"sort"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/access"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/permissions"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
)

type FingerprintInput struct {
	CWDRealpath                 string
	Sandbox                     permissions.CodexSandboxMode
	AccessPolicyDigest          string
	ResourceScopeDigest         string
	AttachmentPolicyShapeDigest string
	CodexHome                   string
	InheritCodexHome            bool
}

func PolicyFingerprint(input FingerprintInput) (string, error) {
	return digestCanonical(map[string]any{
		"version":                     2,
		"cwdRealpath":                 input.CWDRealpath,
		"sandbox":                     string(input.Sandbox),
		"accessPolicyDigest":          input.AccessPolicyDigest,
		"resourceScopeDigest":         input.ResourceScopeDigest,
		"attachmentPolicyShapeDigest": input.AttachmentPolicyShapeDigest,
		"codexHome":                   nullableString(input.CodexHome),
		"inheritCodexHome":            input.InheritCodexHome,
	})
}

func AccessPolicyDigest(input profile.Access) (string, error) {
	return digestCanonical(map[string]any{
		"admins":                sortedStrings(input.Admins),
		"allowedChats":          sortedStrings(input.AllowedChats),
		"allowedUsers":          sortedStrings(input.AllowedUsers),
		"requireMentionInGroup": input.RequireMentionInGroup,
	})
}

func ResourceScopeDigest(input ScopeContext) (string, error) {
	bindings := make([]string, 0, len(input.ResourceBindings))
	for _, binding := range input.ResourceBindings {
		bindings = append(bindings, binding.ID)
	}
	return digestCanonical(map[string]any{
		"source":           string(input.Source),
		"chatId":           nullableString(input.ChatID),
		"threadId":         nullableString(input.ThreadID),
		"commentScopeId":   nullableString(input.CommentScopeID),
		"resourceBindings": sortedStrings(bindings),
	})
}

func AttachmentPolicyShapeDigest(input []AgentAttachment) (string, error) {
	shape := make([]map[string]any, 0, len(input))
	for _, item := range input {
		shape = append(shape, map[string]any{
			"kind":            item.Kind,
			"requiredness":    nullableString(string(item.Requiredness)),
			"decision":        nullableString(string(item.Decision)),
			"rejectionReason": nullableString(item.RejectionReason),
		})
	}
	sort.Slice(shape, func(i, j int) bool {
		left, _ := canonicalizeJCS(shape[i])
		right, _ := canonicalizeJCS(shape[j])
		return left < right
	})
	return digestCanonical(shape)
}

func AttachmentPolicyConfigDigest(input profile.AttachmentConfig) (string, error) {
	return digestCanonical(map[string]any{
		"maxCount":      input.MaxCount,
		"maxBytes":      input.MaxBytes,
		"maxFileBytes":  input.MaxFileBytes,
		"imageMaxBytes": input.ImageMaxBytes,
	})
}

func CommentMentionAccessDigest(decision access.Decision) string {
	if decision.Reason == access.ReasonCommentMention {
		return "comment-mention"
	}
	return ""
}

func digestCanonical(value any) (string, error) {
	canonical, err := canonicalizeJCS(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:16]), nil
}

func sortedStrings(input []string) []string {
	out := append([]string(nil), input...)
	sort.Strings(out)
	return out
}

func nullableString(input string) any {
	if input == "" {
		return nil
	}
	return input
}
