package media

import (
	"context"
	"time"
)

type AttachmentKind string

const (
	AttachmentKindImage   AttachmentKind = "image"
	AttachmentKindFile    AttachmentKind = "file"
	AttachmentKindAudio   AttachmentKind = "audio"
	AttachmentKindVideo   AttachmentKind = "video"
	AttachmentKindSticker AttachmentKind = "sticker"
)

type AttachmentDecision string

const (
	AttachmentAccepted AttachmentDecision = "accepted"
	AttachmentRejected AttachmentDecision = "rejected"
	AttachmentSkipped  AttachmentDecision = "skipped"
)

type AttachmentRequiredness string

const (
	AttachmentRequired AttachmentRequiredness = "required"
	AttachmentOptional AttachmentRequiredness = "optional"
)

type AttachmentCandidate struct {
	AbsPath         string
	Kind            AttachmentKind
	Size            int64
	MIME            string
	Hash            string
	Source          string
	SourceMessageID string
	SourceFileKey   string
	OriginalName    string
	Requiredness    AttachmentRequiredness
}

type NormalizedAttachment struct {
	AttachmentCandidate
	Path            string
	Requiredness    AttachmentRequiredness
	Decision        AttachmentDecision
	RejectionReason string
}

type AttachmentPolicyOptions struct {
	MaxCount      int
	MaxBytes      int64
	MaxFileBytes  int64
	ImageMaxBytes int64
}

type ResolveOptions struct {
	AttachmentPolicyOptions
	CacheTTL      time.Duration
	CacheMaxBytes int64
}

type ResourceDescriptor struct {
	Type         AttachmentKind
	FileKey      string
	FileName     string
	Requiredness AttachmentRequiredness
}

type ResourceRequest struct {
	MessageID string
	Resource  ResourceDescriptor
}

type DownloadResourceType string

const (
	DownloadResourceImage DownloadResourceType = "image"
	DownloadResourceFile  DownloadResourceType = "file"
)

type DownloadRequest struct {
	MessageID       string
	FileKey         string
	Type            DownloadResourceType
	DestinationPath string
}

type DownloadResult struct {
	ContentType  string
	BytesWritten int64
}

type ResourceDownloader interface {
	DownloadResource(ctx context.Context, req DownloadRequest) (DownloadResult, error)
}
