package media

import (
	"testing"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/access"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/capability"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/runpolicy"
)

func TestNormalizeAttachmentsAcceptsAllowedImagesAndFiles(t *testing.T) {
	out := NormalizeAttachments([]AttachmentCandidate{
		candidate(AttachmentCandidate{
			Kind:    AttachmentKindImage,
			MIME:    "image/png",
			Hash:    "abc",
			AbsPath: "/media/abc.png",
		}),
		candidate(AttachmentCandidate{
			Kind:    AttachmentKindFile,
			MIME:    "application/zip",
			Hash:    "def",
			AbsPath: "/media/def.zip",
		}),
	}, AttachmentPolicyOptions{})

	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if got := out[0]; got.Kind != AttachmentKindImage || got.Path != "/media/abc.png" || got.Decision != AttachmentAccepted || got.Requiredness != AttachmentOptional {
		t.Fatalf("image attachment = %#v, want accepted optional image with path", got)
	}
	if got := out[1]; got.Kind != AttachmentKindFile || got.Decision != AttachmentAccepted {
		t.Fatalf("file attachment = %#v, want accepted file", got)
	}
}

func TestNormalizeAttachmentsRejectsAndSkipsUnsupportedKinds(t *testing.T) {
	out := NormalizeAttachments([]AttachmentCandidate{
		candidate(AttachmentCandidate{Kind: AttachmentKindImage, MIME: "image/svg+xml", Hash: "svg"}),
		candidate(AttachmentCandidate{Kind: AttachmentKindImage, MIME: "application/octet-stream", Hash: "unknown"}),
		candidate(AttachmentCandidate{Kind: AttachmentKindSticker, MIME: "image/webp", Hash: "sticker"}),
		candidate(AttachmentCandidate{Kind: AttachmentKindAudio, MIME: "audio/ogg", Hash: "audio"}),
		candidate(AttachmentCandidate{Kind: AttachmentKindVideo, MIME: "video/mp4", Hash: "video"}),
	}, AttachmentPolicyOptions{})

	want := []struct {
		kind     AttachmentKind
		decision AttachmentDecision
		reason   string
	}{
		{AttachmentKindImage, AttachmentRejected, "unsupported-image-mime"},
		{AttachmentKindImage, AttachmentRejected, "unsupported-image-mime"},
		{AttachmentKindSticker, AttachmentSkipped, "sticker"},
		{AttachmentKindAudio, AttachmentSkipped, "unsupported-kind"},
		{AttachmentKindVideo, AttachmentSkipped, "unsupported-kind"},
	}
	for i, item := range want {
		if got := out[i]; got.Kind != item.kind || got.Decision != item.decision || got.RejectionReason != item.reason {
			t.Fatalf("out[%d] = %#v, want %#v", i, got, item)
		}
	}
}

func TestNormalizeAttachmentsEnforcesSizeLimits(t *testing.T) {
	out := NormalizeAttachments([]AttachmentCandidate{
		candidate(AttachmentCandidate{Kind: AttachmentKindImage, MIME: "image/png", Hash: "image", Size: 5}),
		candidate(AttachmentCandidate{Kind: AttachmentKindFile, MIME: "text/plain", Hash: "big-file", Size: 9}),
		candidate(AttachmentCandidate{Kind: AttachmentKindFile, MIME: "text/plain", Hash: "ok", Size: 6}),
		candidate(AttachmentCandidate{Kind: AttachmentKindFile, MIME: "text/plain", Hash: "run", Size: 5}),
	}, AttachmentPolicyOptions{
		MaxCount:      10,
		MaxBytes:      10,
		MaxFileBytes:  8,
		ImageMaxBytes: 4,
	})

	gotReasons := make([]string, 0, len(out))
	for _, attachment := range out {
		gotReasons = append(gotReasons, attachment.RejectionReason)
	}
	wantReasons := []string{"image-too-large", "file-too-large", "", "run-too-large"}
	for i := range wantReasons {
		if gotReasons[i] != wantReasons[i] {
			t.Fatalf("rejection reasons = %#v, want %#v", gotReasons, wantReasons)
		}
	}
}

func TestNormalizeAttachmentsEnforcesTooMany(t *testing.T) {
	out := NormalizeAttachments([]AttachmentCandidate{
		candidate(AttachmentCandidate{Kind: AttachmentKindFile, MIME: "text/plain", Hash: "1", Size: 1}),
		candidate(AttachmentCandidate{Kind: AttachmentKindFile, MIME: "text/plain", Hash: "2", Size: 1}),
		candidate(AttachmentCandidate{Kind: AttachmentKindFile, MIME: "text/plain", Hash: "3", Size: 1}),
	}, AttachmentPolicyOptions{
		MaxCount:      2,
		MaxBytes:      100,
		MaxFileBytes:  100,
		ImageMaxBytes: 100,
	})

	if got := out[2]; got.Decision != AttachmentRejected || got.RejectionReason != "too-many-attachments" {
		t.Fatalf("third attachment = %#v, want too-many rejection", got)
	}
}

func TestRequiredRejectedAttachmentCarriesDataAndFailsRunPolicy(t *testing.T) {
	normalized := NormalizeAttachments([]AttachmentCandidate{
		candidate(AttachmentCandidate{
			Kind:         AttachmentKindFile,
			MIME:         "text/plain",
			Hash:         "required",
			AbsPath:      "/media/required.txt",
			Size:         10,
			OriginalName: "required.txt",
			Requiredness: AttachmentRequired,
		}),
	}, AttachmentPolicyOptions{
		MaxCount:      10,
		MaxBytes:      100,
		MaxFileBytes:  5,
		ImageMaxBytes: 100,
	})
	if len(normalized) != 1 {
		t.Fatalf("len(normalized) = %d, want 1", len(normalized))
	}
	attachment := normalized[0]
	if attachment.Requiredness != AttachmentRequired || attachment.Decision != AttachmentRejected || attachment.RejectionReason != "file-too-large" {
		t.Fatalf("attachment = %#v, want required file-too-large rejection", attachment)
	}

	policyAttachment := ToPolicyAttachment(attachment)
	if policyAttachment.Requiredness != runpolicy.AttachmentRequired || policyAttachment.RejectionReason != "file-too-large" || policyAttachment.OriginalName != "required.txt" {
		t.Fatalf("policy attachment = %#v, want rejection data preserved", policyAttachment)
	}
	promptAttachment := ToPromptAttachment(attachment)
	if promptAttachment.Requiredness != "required" || promptAttachment.Decision != "rejected" || promptAttachment.RejectionReason != "file-too-large" {
		t.Fatalf("prompt attachment = %#v, want rejection data preserved", promptAttachment)
	}

	cfg := profile.DefaultConfig(profile.AgentCodex)
	result, err := runpolicy.Evaluate(runpolicy.Input{
		Scope: runpolicy.ScopeContext{
			Source:  runpolicy.SourceIM,
			ChatID:  "oc_1",
			ActorID: "ou_1",
		},
		Attachments:   []runpolicy.AgentAttachment{policyAttachment},
		Prompt:        "please inspect",
		RequestedCWD:  t.TempDir(),
		CWDRealpath:   t.TempDir(),
		Access:        access.Decision{OK: true, Reason: access.ReasonAllowedUser},
		Capability:    capability.Codex(cfg.Permissions.MaxAccess, ""),
		ProfileConfig: cfg,
		Now:           time.UnixMilli(1000),
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if result.OK || result.RejectReason.Code != runpolicy.RejectRequiredAttachment {
		t.Fatalf("result = %#v, want required attachment rejection", result)
	}
}

func TestSafeExtensionForMIME(t *testing.T) {
	cases := map[string]string{
		"image/jpeg":       "jpg",
		"IMAGE/PNG":        "png",
		"image/webp":       "webp",
		"image/gif":        "gif",
		"application/zip":  "zip",
		"application/x-sh": "bin",
	}
	for mime, want := range cases {
		if got := SafeExtensionForMIME(mime); got != want {
			t.Fatalf("SafeExtensionForMIME(%q) = %q, want %q", mime, got, want)
		}
	}
}

func candidate(overrides AttachmentCandidate) AttachmentCandidate {
	out := AttachmentCandidate{
		AbsPath:         "/media/hash.png",
		Kind:            AttachmentKindImage,
		Size:            100,
		MIME:            "image/png",
		Hash:            "hash",
		Source:          "lark",
		SourceMessageID: "om_1",
		SourceFileKey:   "file_key",
		OriginalName:    "secret original name.png",
	}
	if overrides.AbsPath != "" {
		out.AbsPath = overrides.AbsPath
	}
	if overrides.Kind != "" {
		out.Kind = overrides.Kind
	}
	if overrides.Size != 0 {
		out.Size = overrides.Size
	}
	if overrides.MIME != "" {
		out.MIME = overrides.MIME
	}
	if overrides.Hash != "" {
		out.Hash = overrides.Hash
		if overrides.AbsPath == "" {
			out.AbsPath = "/media/" + overrides.Hash + ".png"
		}
	}
	if overrides.Source != "" {
		out.Source = overrides.Source
	}
	if overrides.SourceMessageID != "" {
		out.SourceMessageID = overrides.SourceMessageID
	}
	if overrides.SourceFileKey != "" {
		out.SourceFileKey = overrides.SourceFileKey
	}
	if overrides.OriginalName != "" {
		out.OriginalName = overrides.OriginalName
	}
	if overrides.Requiredness != "" {
		out.Requiredness = overrides.Requiredness
	}
	return out
}
