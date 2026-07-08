package comments

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/workspace"
)

type WorkspaceFallback struct {
	From   string
	To     string
	Reason string
}

type WorkspaceResult struct {
	OK           bool
	RequestedCWD string
	CWDRealpath  string
	Reason       workspace.RejectReason
	UserVisible  string
	Fallback     *WorkspaceFallback
}

func resolveCommentWorkingDirectory(configuredCWD string, defaultCWD string, managedFallbackCWD string) WorkspaceResult {
	failures := []string{}
	if configuredCWD != "" {
		configured := fromWorkspaceResolve(workspace.ResolveWorkingDirectory(configuredCWD))
		if configured.OK {
			return configured
		}
		failures = appendVisible(failures, configured.UserVisible)
		if defaultCWD != "" {
			fallback := fromWorkspaceResolve(workspace.ResolveWorkingDirectory(defaultCWD))
			if fallback.OK {
				fallback.Fallback = &WorkspaceFallback{From: "document", To: "profile-default", Reason: string(configured.Reason)}
				return fallback
			}
			failures = appendVisible(failures, fallback.UserVisible)
			return resolveManagedCommentWorkingDirectory(managedFallbackCWD, "document/profile-default", string(fallback.Reason), failures)
		}
		return resolveManagedCommentWorkingDirectory(managedFallbackCWD, "document", string(configured.Reason), failures)
	}

	if defaultCWD == "" {
		return resolveManagedCommentWorkingDirectory(managedFallbackCWD, "missing-default", "missing-default-cwd", failures)
	}
	defaultWorkspace := fromWorkspaceResolve(workspace.ResolveWorkingDirectory(defaultCWD))
	if defaultWorkspace.OK {
		return defaultWorkspace
	}
	failures = appendVisible(failures, defaultWorkspace.UserVisible)
	return resolveManagedCommentWorkingDirectory(managedFallbackCWD, "profile-default", string(defaultWorkspace.Reason), failures)
}

func resolveManagedCommentWorkingDirectory(managedFallbackCWD string, fallbackFrom string, fallbackReason string, failures []string) WorkspaceResult {
	if managedFallbackCWD == "" {
		managedFallbackCWD = defaultManagedCommentWorkspace()
	}
	if err := os.MkdirAll(managedFallbackCWD, 0o700); err != nil {
		return WorkspaceResult{
			OK:           false,
			RequestedCWD: managedFallbackCWD,
			CWDRealpath:  managedFallbackCWD,
			Reason:       "managed-fallback-unavailable",
			UserVisible:  strings.Join(appendVisible(failures, "托管工作目录不可用："+err.Error()), "；"),
		}
	}
	resolved := fromWorkspaceResolve(workspace.ResolveWorkingDirectory(managedFallbackCWD))
	if resolved.OK {
		resolved.Fallback = &WorkspaceFallback{From: fallbackFrom, To: "managed-default", Reason: fallbackReason}
		return resolved
	}
	return WorkspaceResult{
		OK:           false,
		RequestedCWD: managedFallbackCWD,
		CWDRealpath:  managedFallbackCWD,
		Reason:       resolved.Reason,
		UserVisible:  strings.Join(appendVisible(failures, resolved.UserVisible), "；"),
	}
}

func fromWorkspaceResolve(result workspace.ResolveResult) WorkspaceResult {
	return WorkspaceResult{
		OK:           result.OK,
		RequestedCWD: result.RequestedCWD,
		CWDRealpath:  result.CWDRealpath,
		Reason:       result.Reason,
		UserVisible:  result.UserVisible,
	}
}

func defaultManagedCommentWorkspace() string {
	if cacheDir, err := os.UserCacheDir(); err == nil && cacheDir != "" {
		return filepath.Join(cacheDir, "lark-channel-bridge", "comments-workspace")
	}
	return filepath.Join(os.TempDir(), "lark-channel-bridge-comments-workspace")
}

func appendVisible(items []string, item string) []string {
	if strings.TrimSpace(item) == "" {
		return items
	}
	return append(items, item)
}
