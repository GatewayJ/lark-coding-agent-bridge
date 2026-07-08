package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
)

type Cache struct {
	downloader ResourceDownloader
	rootDir    string
}

func NewCache(downloader ResourceDownloader, rootDir string) *Cache {
	return &Cache{downloader: downloader, rootDir: rootDir}
}

func ResolveOptionsFromProfile(config profile.AttachmentConfig) ResolveOptions {
	return ResolveOptions{
		AttachmentPolicyOptions: AttachmentPolicyOptions{
			MaxCount:      config.MaxCount,
			MaxBytes:      config.MaxBytes,
			MaxFileBytes:  config.MaxFileBytes,
			ImageMaxBytes: config.ImageMaxBytes,
		},
		CacheTTL:      time.Duration(config.CacheTTLMS) * time.Millisecond,
		CacheMaxBytes: config.CacheMaxBytes,
	}
}

func (c *Cache) Resolve(ctx context.Context, items []ResourceRequest, options ResolveOptions) ([]NormalizedAttachment, error) {
	if len(items) == 0 {
		return nil, nil
	}
	if c == nil {
		return nil, errors.New("media cache is nil")
	}
	if c.downloader == nil {
		return nil, errors.New("media downloader is required")
	}
	if c.rootDir == "" {
		return nil, errors.New("media cache root is required")
	}
	if err := os.MkdirAll(c.rootDir, 0o755); err != nil {
		return nil, err
	}
	if options.CacheTTL > 0 {
		if err := GCMediaCache(ctx, options.CacheTTL, c.rootDir); err != nil {
			return nil, err
		}
	}

	candidates := make([]AttachmentCandidate, 0, len(items))
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate, err := c.resolveOne(ctx, item)
		if err != nil {
			continue
		}
		if candidate != nil {
			candidates = append(candidates, *candidate)
		}
	}
	normalized := NormalizeAttachments(candidates, options.AttachmentPolicyOptions)
	if err := removeRejectedResolvedFiles(normalized); err != nil {
		return nil, err
	}
	if options.CacheMaxBytes > 0 {
		if err := enforceCacheMaxBytes(c.rootDir, options.CacheMaxBytes, acceptedPathSet(normalized)); err != nil {
			return nil, err
		}
	}
	return normalized, nil
}

func (c *Cache) resolveOne(ctx context.Context, item ResourceRequest) (*AttachmentCandidate, error) {
	resource := item.Resource
	kind := resource.Type
	if kind == "" {
		kind = AttachmentKindFile
	}
	if kind == AttachmentKindSticker {
		return nil, nil
	}
	tmp, err := os.CreateTemp(c.rootDir, ".tmp-*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return nil, err
	}
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	downloadType := DownloadResourceFile
	if kind == AttachmentKindImage {
		downloadType = DownloadResourceImage
	}
	result, err := c.downloader.DownloadResource(ctx, DownloadRequest{
		MessageID:       item.MessageID,
		FileKey:         resource.FileKey,
		Type:            downloadType,
		DestinationPath: tmpPath,
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	st, err := os.Stat(tmpPath)
	if err != nil {
		return nil, err
	}
	hash, err := hashFile(tmpPath)
	if err != nil {
		return nil, err
	}
	mime := result.ContentType
	if mime == "" {
		mime = defaultMIME(kind)
	}
	absPath := filepath.Join(c.rootDir, fmt.Sprintf("%s.%s", hash, SafeExtensionForMIME(mime)))
	if _, err := os.Stat(absPath); err == nil {
		_ = os.Remove(tmpPath)
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(tmpPath, absPath); err != nil {
			if _, statErr := os.Stat(absPath); statErr == nil {
				_ = os.Remove(tmpPath)
			} else {
				return nil, err
			}
		}
	} else {
		return nil, err
	}

	return &AttachmentCandidate{
		AbsPath:         absPath,
		Kind:            kind,
		Size:            st.Size(),
		MIME:            mime,
		Hash:            hash,
		Source:          "lark",
		SourceMessageID: item.MessageID,
		SourceFileKey:   resource.FileKey,
		OriginalName:    resource.FileName,
		Requiredness:    resource.Requiredness,
	}, nil
}

func GCMediaCache(ctx context.Context, maxAge time.Duration, root string) error {
	if maxAge <= 0 {
		return nil
	}
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	cutoff := time.Now().Add(-maxAge)
	files, err := listFiles(root)
	if err != nil {
		return err
	}
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		st, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if st.Mode().IsRegular() && st.ModTime().Before(cutoff) {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func defaultMIME(kind AttachmentKind) string {
	switch kind {
	case AttachmentKindImage:
		return "image/png"
	case AttachmentKindAudio:
		return "audio/ogg"
	case AttachmentKindVideo:
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func listFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

func enforceCacheMaxBytes(root string, maxBytes int64, protectedPaths map[string]struct{}) error {
	if maxBytes <= 0 {
		return nil
	}
	files, err := listFiles(root)
	if err != nil {
		return err
	}
	items := make([]cacheFile, 0, len(files))
	var total int64
	for _, path := range files {
		st, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		size := st.Size()
		total += size
		items = append(items, cacheFile{path: path, size: size, mtime: st.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].mtime.Before(items[j].mtime)
	})
	for _, item := range items {
		if total <= maxBytes {
			break
		}
		if _, ok := protectedPaths[item.path]; ok {
			continue
		}
		if err := os.Remove(item.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		total -= item.size
	}
	return nil
}

func removeRejectedResolvedFiles(attachments []NormalizedAttachment) error {
	accepted := acceptedPathSet(attachments)
	for _, attachment := range attachments {
		if attachment.Decision == AttachmentAccepted || attachment.AbsPath == "" {
			continue
		}
		if _, protected := accepted[attachment.AbsPath]; protected {
			continue
		}
		if err := os.Remove(attachment.AbsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func acceptedPathSet(attachments []NormalizedAttachment) map[string]struct{} {
	out := map[string]struct{}{}
	for _, attachment := range attachments {
		if attachment.Decision == AttachmentAccepted && attachment.AbsPath != "" {
			out[attachment.AbsPath] = struct{}{}
		}
	}
	return out
}

type cacheFile struct {
	path  string
	size  int64
	mtime time.Time
}
