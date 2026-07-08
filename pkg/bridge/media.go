package bridge

import (
	"context"
	"time"

	appmedia "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/media"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
)

type MediaAttachmentKind string

const (
	MediaAttachmentImage   MediaAttachmentKind = "image"
	MediaAttachmentFile    MediaAttachmentKind = "file"
	MediaAttachmentAudio   MediaAttachmentKind = "audio"
	MediaAttachmentVideo   MediaAttachmentKind = "video"
	MediaAttachmentSticker MediaAttachmentKind = "sticker"
)

type MediaAttachmentDecision string

const (
	MediaAttachmentAccepted MediaAttachmentDecision = "accepted"
	MediaAttachmentRejected MediaAttachmentDecision = "rejected"
	MediaAttachmentSkipped  MediaAttachmentDecision = "skipped"
)

type MediaAttachmentRequiredness string

const (
	MediaAttachmentRequired MediaAttachmentRequiredness = "required"
	MediaAttachmentOptional MediaAttachmentRequiredness = "optional"
)

type MediaAttachmentCandidate struct {
	AbsPath         string
	Kind            MediaAttachmentKind
	Size            int64
	MIME            string
	Hash            string
	Source          string
	SourceMessageID string
	SourceFileKey   string
	OriginalName    string
	Requiredness    MediaAttachmentRequiredness
}

type MediaAttachment struct {
	MediaAttachmentCandidate
	Path            string
	Requiredness    MediaAttachmentRequiredness
	Decision        MediaAttachmentDecision
	RejectionReason string
}

type MediaAttachmentPolicyOptions struct {
	MaxCount      int
	MaxBytes      int64
	MaxFileBytes  int64
	ImageMaxBytes int64
}

type MediaResolveOptions struct {
	MediaAttachmentPolicyOptions
	AttachmentPolicyOptions MediaAttachmentPolicyOptions
	CacheTTL                time.Duration
	CacheMaxBytes           int64
}

type MediaResource struct {
	Type         MediaAttachmentKind
	FileKey      string
	FileName     string
	Requiredness MediaAttachmentRequiredness
}

type MediaResourceRequest struct {
	MessageID string
	Resource  MediaResource
}

type MediaDownloadResourceType string

const (
	MediaDownloadResourceImage MediaDownloadResourceType = "image"
	MediaDownloadResourceFile  MediaDownloadResourceType = "file"
)

type MediaDownloadRequest struct {
	MessageID       string
	FileKey         string
	Type            MediaDownloadResourceType
	DestinationPath string
}

type MediaDownloadResult struct {
	ContentType  string
	BytesWritten int64
}

type MediaDownloader interface {
	DownloadResource(ctx context.Context, req MediaDownloadRequest) (MediaDownloadResult, error)
}

type MediaCache struct {
	inner *appmedia.Cache
}

func NewMediaCache(downloader MediaDownloader, rootDir string) *MediaCache {
	return &MediaCache{inner: appmedia.NewCache(wrapInternalMediaDownloader(downloader), rootDir)}
}

func (c *MediaCache) Resolve(ctx context.Context, items []MediaResourceRequest, options MediaResolveOptions) ([]MediaAttachment, error) {
	if c == nil || c.inner == nil {
		return nil, nil
	}
	resolved, err := c.inner.Resolve(ctx, toInternalMediaResourceRequests(items), toInternalMediaResolveOptions(options))
	if err != nil {
		return nil, err
	}
	return fromInternalMediaAttachments(resolved), nil
}

func NormalizeMediaAttachments(candidates []MediaAttachmentCandidate, options MediaAttachmentPolicyOptions) []MediaAttachment {
	return fromInternalMediaAttachments(appmedia.NormalizeAttachments(toInternalMediaAttachmentCandidates(candidates), toInternalMediaAttachmentPolicyOptions(options)))
}

func SafeMediaExtensionForMIME(mime string) string {
	return appmedia.SafeExtensionForMIME(mime)
}

func MediaResolveOptionsFromConfig(config ConfigAttachments) MediaResolveOptions {
	return mediaResolveOptionsFromProfile(profile.AttachmentConfig{
		MaxCount:      config.MaxCount,
		MaxBytes:      config.MaxBytes,
		MaxFileBytes:  config.MaxFileBytes,
		ImageMaxBytes: config.ImageMaxBytes,
		CacheTTLMS:    config.CacheTTLMS,
		CacheMaxBytes: config.CacheMaxBytes,
	})
}

func GCMediaCache(ctx context.Context, maxAge time.Duration, root string) error {
	return appmedia.GCMediaCache(ctx, maxAge, root)
}

func mediaResolveOptionsFromProfile(config profile.AttachmentConfig) MediaResolveOptions {
	return fromInternalMediaResolveOptions(appmedia.ResolveOptionsFromProfile(config))
}

func toInternalMediaDownloadRequest(req MediaDownloadRequest) appmedia.DownloadRequest {
	return appmedia.DownloadRequest{
		MessageID:       req.MessageID,
		FileKey:         req.FileKey,
		Type:            appmedia.DownloadResourceType(req.Type),
		DestinationPath: req.DestinationPath,
	}
}

func fromInternalMediaDownloadResult(result appmedia.DownloadResult) MediaDownloadResult {
	return MediaDownloadResult{ContentType: result.ContentType, BytesWritten: result.BytesWritten}
}

func wrapInternalMediaDownloader(downloader MediaDownloader) appmedia.ResourceDownloader {
	if downloader == nil {
		return nil
	}
	if oapi, ok := downloader.(*OAPILarkTransport); ok {
		return oapi.internalMediaDownloader()
	}
	if fake, ok := downloader.(*FakeLarkTransport); ok {
		return fake.internalMediaDownloader()
	}
	return mediaDownloaderAdapter{downloader: downloader}
}

func wrapInternalMediaDownloaderFromTransport(transport LarkTransport) appmedia.ResourceDownloader {
	if transport == nil {
		return nil
	}
	if provider, ok := transport.(interface {
		internalMediaDownloader() appmedia.ResourceDownloader
	}); ok {
		return provider.internalMediaDownloader()
	}
	if downloader, ok := transport.(MediaDownloader); ok {
		return wrapInternalMediaDownloader(downloader)
	}
	return nil
}

type mediaDownloaderAdapter struct {
	downloader MediaDownloader
}

func (a mediaDownloaderAdapter) DownloadResource(ctx context.Context, req appmedia.DownloadRequest) (appmedia.DownloadResult, error) {
	result, err := a.downloader.DownloadResource(ctx, MediaDownloadRequest{
		MessageID:       req.MessageID,
		FileKey:         req.FileKey,
		Type:            MediaDownloadResourceType(req.Type),
		DestinationPath: req.DestinationPath,
	})
	return appmedia.DownloadResult{ContentType: result.ContentType, BytesWritten: result.BytesWritten}, err
}

func toInternalMediaAttachmentCandidate(candidate MediaAttachmentCandidate) appmedia.AttachmentCandidate {
	return appmedia.AttachmentCandidate{
		AbsPath:         candidate.AbsPath,
		Kind:            appmedia.AttachmentKind(candidate.Kind),
		Size:            candidate.Size,
		MIME:            candidate.MIME,
		Hash:            candidate.Hash,
		Source:          candidate.Source,
		SourceMessageID: candidate.SourceMessageID,
		SourceFileKey:   candidate.SourceFileKey,
		OriginalName:    candidate.OriginalName,
		Requiredness:    appmedia.AttachmentRequiredness(candidate.Requiredness),
	}
}

func fromInternalMediaAttachmentCandidate(candidate appmedia.AttachmentCandidate) MediaAttachmentCandidate {
	return MediaAttachmentCandidate{
		AbsPath:         candidate.AbsPath,
		Kind:            MediaAttachmentKind(candidate.Kind),
		Size:            candidate.Size,
		MIME:            candidate.MIME,
		Hash:            candidate.Hash,
		Source:          candidate.Source,
		SourceMessageID: candidate.SourceMessageID,
		SourceFileKey:   candidate.SourceFileKey,
		OriginalName:    candidate.OriginalName,
		Requiredness:    MediaAttachmentRequiredness(candidate.Requiredness),
	}
}

func toInternalMediaAttachmentCandidates(candidates []MediaAttachmentCandidate) []appmedia.AttachmentCandidate {
	out := make([]appmedia.AttachmentCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, toInternalMediaAttachmentCandidate(candidate))
	}
	return out
}

func fromInternalMediaAttachment(attachment appmedia.NormalizedAttachment) MediaAttachment {
	return MediaAttachment{
		MediaAttachmentCandidate: fromInternalMediaAttachmentCandidate(attachment.AttachmentCandidate),
		Path:                     attachment.Path,
		Requiredness:             MediaAttachmentRequiredness(attachment.Requiredness),
		Decision:                 MediaAttachmentDecision(attachment.Decision),
		RejectionReason:          attachment.RejectionReason,
	}
}

func fromInternalMediaAttachments(attachments []appmedia.NormalizedAttachment) []MediaAttachment {
	out := make([]MediaAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		out = append(out, fromInternalMediaAttachment(attachment))
	}
	return out
}

func toInternalMediaAttachmentPolicyOptions(options MediaAttachmentPolicyOptions) appmedia.AttachmentPolicyOptions {
	return appmedia.AttachmentPolicyOptions{
		MaxCount:      options.MaxCount,
		MaxBytes:      options.MaxBytes,
		MaxFileBytes:  options.MaxFileBytes,
		ImageMaxBytes: options.ImageMaxBytes,
	}
}

func toInternalMediaResolveOptions(options MediaResolveOptions) appmedia.ResolveOptions {
	return appmedia.ResolveOptions{
		AttachmentPolicyOptions: toInternalMediaAttachmentPolicyOptions(mediaResolvePolicyOptions(options)),
		CacheTTL:                options.CacheTTL,
		CacheMaxBytes:           options.CacheMaxBytes,
	}
}

func fromInternalMediaResolveOptions(options appmedia.ResolveOptions) MediaResolveOptions {
	policy := MediaAttachmentPolicyOptions{
		MaxCount:      options.MaxCount,
		MaxBytes:      options.MaxBytes,
		MaxFileBytes:  options.MaxFileBytes,
		ImageMaxBytes: options.ImageMaxBytes,
	}
	return MediaResolveOptions{
		MediaAttachmentPolicyOptions: policy,
		AttachmentPolicyOptions:      policy,
		CacheTTL:                     options.CacheTTL,
		CacheMaxBytes:                options.CacheMaxBytes,
	}
}

func mediaResolvePolicyOptions(options MediaResolveOptions) MediaAttachmentPolicyOptions {
	if options.AttachmentPolicyOptions != (MediaAttachmentPolicyOptions{}) {
		return options.AttachmentPolicyOptions
	}
	return options.MediaAttachmentPolicyOptions
}

func toInternalMediaResourceRequests(requests []MediaResourceRequest) []appmedia.ResourceRequest {
	out := make([]appmedia.ResourceRequest, 0, len(requests))
	for _, req := range requests {
		out = append(out, appmedia.ResourceRequest{
			MessageID: req.MessageID,
			Resource: appmedia.ResourceDescriptor{
				Type:         appmedia.AttachmentKind(req.Resource.Type),
				FileKey:      req.Resource.FileKey,
				FileName:     req.Resource.FileName,
				Requiredness: appmedia.AttachmentRequiredness(req.Resource.Requiredness),
			},
		})
	}
	return out
}
