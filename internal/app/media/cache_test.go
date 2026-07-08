package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
)

func TestCacheResolveDownloadsResourcesIntoHashPaths(t *testing.T) {
	root := t.TempDir()
	bytes := []byte("image-bytes")
	cache := NewCache(fakeDownloader{
		"img_secret_key": {bytes: bytes, contentType: "image/png"},
	}, root)

	attachments, err := cache.Resolve(context.Background(), []ResourceRequest{{
		MessageID: "om_1",
		Resource: ResourceDescriptor{
			Type:     AttachmentKindImage,
			FileKey:  "img_secret_key",
			FileName: "private name.png",
		},
	}}, ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("len(attachments) = %d, want 1", len(attachments))
	}
	hash := sha256Hex(bytes)
	wantPath := filepath.Join(root, hash+".png")
	attachment := attachments[0]
	if attachment.AbsPath != wantPath || attachment.Path != wantPath || attachment.Hash != hash || attachment.MIME != "image/png" || attachment.Decision != AttachmentAccepted {
		t.Fatalf("attachment = %#v, want accepted hash path", attachment)
	}
	if filepath.Base(attachment.AbsPath) == "private name.png" || filepath.Base(attachment.AbsPath) == "img_secret_key" {
		t.Fatalf("attachment path leaked original name or file key: %s", attachment.AbsPath)
	}
	got, err := os.ReadFile(attachment.AbsPath)
	if err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	if string(got) != string(bytes) {
		t.Fatalf("file bytes = %q, want %q", got, bytes)
	}
}

func TestCacheResolveReusesHashPath(t *testing.T) {
	root := t.TempDir()
	downloader := &countingDownloader{entry: fakeDownloadEntry{bytes: []byte("same-image"), contentType: "image/png"}}
	cache := NewCache(downloader, root)
	req := []ResourceRequest{{
		MessageID: "om_1",
		Resource:  ResourceDescriptor{Type: AttachmentKindImage, FileKey: "image_key"},
	}}

	first, err := cache.Resolve(context.Background(), req, ResolveOptions{})
	if err != nil {
		t.Fatalf("first Resolve returned error: %v", err)
	}
	second, err := cache.Resolve(context.Background(), req, ResolveOptions{})
	if err != nil {
		t.Fatalf("second Resolve returned error: %v", err)
	}
	if first[0].AbsPath != second[0].AbsPath {
		t.Fatalf("paths = %q and %q, want same hash path", first[0].AbsPath, second[0].AbsPath)
	}
	files := regularFiles(t, root)
	if len(files) != 1 || filepath.Base(files[0]) != filepath.Base(first[0].AbsPath) {
		t.Fatalf("cache files = %#v, want only reused hash file", files)
	}
	if downloader.calls != 2 {
		t.Fatalf("download calls = %d, want 2 downloads before hash reuse", downloader.calls)
	}
}

func TestGCMediaCacheRemovesOldFilesByTTL(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "old.bin")
	freshPath := filepath.Join(root, "fresh.bin")
	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(freshPath, []byte("fresh"), 0o644); err != nil {
		t.Fatalf("write fresh: %v", err)
	}
	oldTime := time.Now().Add(-10 * time.Second)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes old: %v", err)
	}

	if err := GCMediaCache(context.Background(), time.Second, root); err != nil {
		t.Fatalf("GCMediaCache returned error: %v", err)
	}
	files := regularFiles(t, root)
	if len(files) != 1 || filepath.Base(files[0]) != "fresh.bin" {
		t.Fatalf("files = %#v, want only fresh.bin", files)
	}
}

func TestCacheResolveEnforcesCacheMaxBytesWithoutDeletingAccepted(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "old.bin")
	if err := os.WriteFile(oldPath, []byte("old-cache-entry"), 0o644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	oldTime := time.Now().Add(-10 * time.Second)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes old: %v", err)
	}

	bytes := []byte("fresh-image")
	cache := NewCache(fakeDownloader{
		"img_secret_key": {bytes: bytes, contentType: "image/png"},
	}, root)
	attachments, err := cache.Resolve(context.Background(), []ResourceRequest{{
		MessageID: "om_1",
		Resource:  ResourceDescriptor{Type: AttachmentKindImage, FileKey: "img_secret_key"},
	}}, ResolveOptions{CacheMaxBytes: int64(len(bytes))})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old file stat err = %v, want not exist", err)
	}
	if st, err := os.Stat(attachments[0].AbsPath); err != nil || st.Size() != int64(len(bytes)) {
		t.Fatalf("accepted stat = (%v, %v), want accepted file preserved", st, err)
	}
}

func TestCacheResolveRemovesRejectedCurrentFiles(t *testing.T) {
	root := t.TempDir()
	bytes := []byte("oversized-image")
	cache := NewCache(fakeDownloader{
		"img_secret_key": {bytes: bytes, contentType: "image/png"},
	}, root)

	attachments, err := cache.Resolve(context.Background(), []ResourceRequest{{
		MessageID: "om_1",
		Resource:  ResourceDescriptor{Type: AttachmentKindImage, FileKey: "img_secret_key"},
	}}, ResolveOptions{
		AttachmentPolicyOptions: AttachmentPolicyOptions{
			MaxCount:      10,
			MaxBytes:      100,
			MaxFileBytes:  100,
			ImageMaxBytes: int64(len(bytes) - 1),
		},
		CacheMaxBytes: int64(len(bytes) - 1),
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got := attachments[0]; got.Decision != AttachmentRejected || got.RejectionReason != "image-too-large" {
		t.Fatalf("attachment = %#v, want image-too-large rejection", got)
	}
	if _, err := os.Stat(attachments[0].AbsPath); !os.IsNotExist(err) {
		t.Fatalf("rejected file stat err = %v, want not exist", err)
	}
}

func TestResolveOptionsFromProfileMapsAttachmentConfig(t *testing.T) {
	options := ResolveOptionsFromProfile(profile.AttachmentConfig{
		MaxCount:      2,
		MaxBytes:      3,
		MaxFileBytes:  4,
		ImageMaxBytes: 5,
		CacheTTLMS:    6,
		CacheMaxBytes: 7,
	})
	if options.MaxCount != 2 || options.MaxBytes != 3 || options.MaxFileBytes != 4 || options.ImageMaxBytes != 5 {
		t.Fatalf("policy options = %#v, want profile values", options.AttachmentPolicyOptions)
	}
	if options.CacheTTL != 6*time.Millisecond || options.CacheMaxBytes != 7 {
		t.Fatalf("cache options = ttl %s max %d, want profile values", options.CacheTTL, options.CacheMaxBytes)
	}
}

type fakeDownloadEntry struct {
	bytes       []byte
	contentType string
}

type fakeDownloader map[string]fakeDownloadEntry

func (f fakeDownloader) DownloadResource(_ context.Context, req DownloadRequest) (DownloadResult, error) {
	entry := f[req.FileKey]
	if err := os.WriteFile(req.DestinationPath, entry.bytes, 0o644); err != nil {
		return DownloadResult{}, err
	}
	return DownloadResult{ContentType: entry.contentType, BytesWritten: int64(len(entry.bytes))}, nil
}

type countingDownloader struct {
	entry fakeDownloadEntry
	calls int
}

func (d *countingDownloader) DownloadResource(_ context.Context, req DownloadRequest) (DownloadResult, error) {
	d.calls++
	if err := os.WriteFile(req.DestinationPath, d.entry.bytes, 0o644); err != nil {
		return DownloadResult{}, err
	}
	return DownloadResult{ContentType: d.entry.contentType, BytesWritten: int64(len(d.entry.bytes))}, nil
}

func regularFiles(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", root, err)
	}
	var out []string
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			out = append(out, filepath.Join(root, entry.Name()))
		}
	}
	sort.Strings(out)
	return out
}

func sha256Hex(bytes []byte) string {
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:])
}
