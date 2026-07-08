package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWorkingDirectoryAcceptsPlainDirectory(t *testing.T) {
	dir := t.TempDir()
	result := ResolveWorkingDirectory(dir)
	if !result.OK {
		t.Fatalf("ResolveWorkingDirectory rejected temp subdir: %#v", result)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks returned error: %v", err)
	}
	if result.CWDRealpath != real {
		t.Fatalf("CWDRealpath = %q, want %q", result.CWDRealpath, real)
	}
}

func TestResolveWorkingDirectoryRejectsMissingFilesAndBroadRoots(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "file.txt")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	if result := ResolveWorkingDirectory(filepath.Join(base, "missing")); result.OK || result.Reason != RejectPathInaccessible {
		t.Fatalf("missing path result = %#v, want path-inaccessible", result)
	}
	if result := ResolveWorkingDirectory(file); result.OK || result.Reason != RejectNotDirectory {
		t.Fatalf("file result = %#v, want not-directory", result)
	}
	if result := ResolveWorkingDirectory("/"); result.OK || result.Reason != RejectFilesystemRoot {
		t.Fatalf("root result = %#v, want filesystem-root", result)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if result := ResolveWorkingDirectory(home); result.OK || result.Reason != RejectHomeRoot {
			t.Fatalf("home result = %#v, want home-root", result)
		}
	}
}
