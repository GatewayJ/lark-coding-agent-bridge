package bridge

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestMediaResolveOptionsFromConfig(t *testing.T) {
	options := MediaResolveOptionsFromConfig(ConfigAttachments{
		MaxCount:      2,
		MaxBytes:      3,
		MaxFileBytes:  4,
		ImageMaxBytes: 5,
		CacheTTLMS:    6,
		CacheMaxBytes: 7,
	})

	if options.MaxCount != 2 || options.MaxBytes != 3 || options.MaxFileBytes != 4 || options.ImageMaxBytes != 5 {
		t.Fatalf("policy options = %#v, want config values", options.AttachmentPolicyOptions)
	}
	if options.CacheTTL != 6*time.Millisecond || options.CacheMaxBytes != 7 {
		t.Fatalf("cache options = ttl %s max %d, want config values", options.CacheTTL, options.CacheMaxBytes)
	}
}

func TestMediaCachePublicAPI(t *testing.T) {
	cache := NewMediaCache(publicMediaDownloader{bytes: []byte("image"), contentType: "image/png"}, t.TempDir())
	attachments, err := cache.Resolve(context.Background(), []MediaResourceRequest{{
		MessageID: "om_1",
		Resource: MediaResource{
			Type:    MediaAttachmentImage,
			FileKey: "img_key",
		},
	}}, MediaResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if len(attachments) != 1 || attachments[0].Decision != MediaAttachmentAccepted {
		t.Fatalf("attachments = %#v, want accepted attachment", attachments)
	}
	if attachments[0].Kind != MediaAttachmentImage || SafeMediaExtensionForMIME(attachments[0].MIME) != "png" {
		t.Fatalf("attachment = %#v, want public media aliases to match", attachments[0])
	}
}

type publicMediaDownloader struct {
	bytes       []byte
	contentType string
}

func (d publicMediaDownloader) DownloadResource(_ context.Context, req MediaDownloadRequest) (MediaDownloadResult, error) {
	if err := os.WriteFile(req.DestinationPath, d.bytes, 0o644); err != nil {
		return MediaDownloadResult{}, err
	}
	return MediaDownloadResult{ContentType: d.contentType, BytesWritten: int64(len(d.bytes))}, nil
}
