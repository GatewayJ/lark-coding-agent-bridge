package media

import (
	"strings"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/runpolicy"
	promptview "github.com/zarazhangrui/lark-coding-agent-bridge/internal/presentation/prompt"
)

var defaultPolicy = AttachmentPolicyOptions{
	MaxCount:      10,
	MaxBytes:      100 * 1024 * 1024,
	MaxFileBytes:  25 * 1024 * 1024,
	ImageMaxBytes: 25 * 1024 * 1024,
}

var imageMIMEExt = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/webp": "webp",
	"image/gif":  "gif",
}

var mimeExt = map[string]string{
	"image/jpeg":       "jpg",
	"image/png":        "png",
	"image/webp":       "webp",
	"image/gif":        "gif",
	"application/pdf":  "pdf",
	"application/zip":  "zip",
	"text/plain":       "txt",
	"text/markdown":    "md",
	"application/json": "json",
}

func NormalizeAttachments(candidates []AttachmentCandidate, options AttachmentPolicyOptions) []NormalizedAttachment {
	policy := normalizePolicy(options)
	out := make([]NormalizedAttachment, 0, len(candidates))
	acceptedCount := 0
	var acceptedBytes int64

	for _, candidate := range candidates {
		base := NormalizedAttachment{
			AttachmentCandidate: candidate,
			Path:                candidate.AbsPath,
			Requiredness:        normalizeRequiredness(candidate.Requiredness),
		}
		if decision, reason, ok := earlyDecision(candidate); ok {
			base.Decision = decision
			base.RejectionReason = reason
			out = append(out, base)
			continue
		}
		if acceptedCount >= policy.MaxCount {
			out = append(out, reject(base, "too-many-attachments"))
			continue
		}
		if candidate.Size > policy.MaxFileBytes {
			out = append(out, reject(base, "file-too-large"))
			continue
		}
		if candidate.Kind == AttachmentKindImage && candidate.Size > policy.ImageMaxBytes {
			out = append(out, reject(base, "image-too-large"))
			continue
		}
		if acceptedBytes+candidate.Size > policy.MaxBytes {
			out = append(out, reject(base, "run-too-large"))
			continue
		}

		acceptedCount++
		acceptedBytes += candidate.Size
		base.Decision = AttachmentAccepted
		out = append(out, base)
	}
	return out
}

func SafeExtensionForMIME(mime string) string {
	if ext, ok := mimeExt[strings.ToLower(mime)]; ok {
		return ext
	}
	return "bin"
}

func ToPolicyAttachment(attachment NormalizedAttachment) runpolicy.AgentAttachment {
	return runpolicy.AgentAttachment{
		Kind:            string(attachment.Kind),
		Requiredness:    runpolicy.AttachmentRequiredness(attachment.Requiredness),
		Decision:        runpolicy.AttachmentDecision(attachment.Decision),
		RejectionReason: attachment.RejectionReason,
		OriginalName:    attachment.OriginalName,
		Size:            attachment.Size,
		Hash:            attachment.Hash,
		Path:            attachment.AbsPath,
	}
}

func ToPolicyAttachments(attachments []NormalizedAttachment) []runpolicy.AgentAttachment {
	out := make([]runpolicy.AgentAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		out = append(out, ToPolicyAttachment(attachment))
	}
	return out
}

func ToPromptAttachment(attachment NormalizedAttachment) promptview.BridgePromptAttachment {
	return promptview.BridgePromptAttachment{
		Path:            attachment.AbsPath,
		Kind:            string(attachment.Kind),
		Hash:            attachment.Hash,
		Size:            attachment.Size,
		MIME:            attachment.MIME,
		SourceMessageID: attachment.SourceMessageID,
		Requiredness:    string(attachment.Requiredness),
		Decision:        string(attachment.Decision),
		RejectionReason: attachment.RejectionReason,
	}
}

func ToPromptAttachments(attachments []NormalizedAttachment) []promptview.BridgePromptAttachment {
	out := make([]promptview.BridgePromptAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		out = append(out, ToPromptAttachment(attachment))
	}
	return out
}

func normalizePolicy(options AttachmentPolicyOptions) AttachmentPolicyOptions {
	policy := defaultPolicy
	if options.MaxCount > 0 {
		policy.MaxCount = options.MaxCount
	}
	if options.MaxBytes > 0 {
		policy.MaxBytes = options.MaxBytes
	}
	if options.MaxFileBytes > 0 {
		policy.MaxFileBytes = options.MaxFileBytes
	}
	if options.ImageMaxBytes > 0 {
		policy.ImageMaxBytes = options.ImageMaxBytes
	}
	return policy
}

func earlyDecision(candidate AttachmentCandidate) (AttachmentDecision, string, bool) {
	switch candidate.Kind {
	case AttachmentKindSticker:
		return AttachmentSkipped, "sticker", true
	case AttachmentKindAudio, AttachmentKindVideo:
		return AttachmentSkipped, "unsupported-kind", true
	case AttachmentKindImage:
		if _, ok := imageMIMEExt[strings.ToLower(candidate.MIME)]; !ok {
			return AttachmentRejected, "unsupported-image-mime", true
		}
	}
	return "", "", false
}

func reject(base NormalizedAttachment, reason string) NormalizedAttachment {
	base.Decision = AttachmentRejected
	base.RejectionReason = reason
	return base
}

func normalizeRequiredness(value AttachmentRequiredness) AttachmentRequiredness {
	if value == AttachmentRequired {
		return AttachmentRequired
	}
	return AttachmentOptional
}
