package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type RejectReason string

const (
	RejectEmptyRequestedCWD RejectReason = "empty-requested-cwd"
	RejectPathInaccessible  RejectReason = "path-inaccessible"
	RejectNotDirectory      RejectReason = "not-directory"
	RejectFilesystemRoot    RejectReason = "filesystem-root"
	RejectHomeRoot          RejectReason = "home-root"
	RejectUserRoot          RejectReason = "user-root"
	RejectSystemRoot        RejectReason = "system-root"
	RejectTempRoot          RejectReason = "temp-root"
	RejectBroadUserFolder   RejectReason = "broad-user-folder"
	RejectVolumeRoot        RejectReason = "volume-root"
)

type ResolveResult struct {
	OK           bool
	RequestedCWD string
	CWDRealpath  string
	Reason       RejectReason
	UserVisible  string
}

func ResolveWorkingDirectory(requestedCWD string) ResolveResult {
	trimmed := strings.TrimSpace(requestedCWD)
	if trimmed == "" {
		return reject(RejectEmptyRequestedCWD, requestedCWD, "未指定工作目录。")
	}

	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return reject(RejectPathInaccessible, requestedCWD, "工作目录不存在或不可访问："+requestedCWD)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return reject(RejectPathInaccessible, requestedCWD, "工作目录不存在或不可访问："+requestedCWD)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return reject(RejectPathInaccessible, requestedCWD, "工作目录不存在或不可访问："+requestedCWD)
	}
	if !info.IsDir() {
		return reject(RejectNotDirectory, requestedCWD, "路径不是目录："+resolved)
	}

	tempRealpath := safeRealpath(os.TempDir())
	homeRealpath := safeHomeRealpath()
	if broad := classifyHighRiskWorkingDirectory(resolved, requestedCWD, tempRealpath, homeRealpath); !broad.OK {
		return broad
	}

	return ResolveResult{
		OK:           true,
		RequestedCWD: requestedCWD,
		CWDRealpath:  resolved,
	}
}

func reject(reason RejectReason, requestedCWD string, userVisible string) ResolveResult {
	return ResolveResult{
		OK:           false,
		Reason:       reason,
		RequestedCWD: requestedCWD,
		UserVisible:  userVisible,
	}
}

func classifyHighRiskWorkingDirectory(real string, requestedCWD string, tempRealpath string, homeRealpath string) ResolveResult {
	clean := filepath.Clean(real)
	if clean == filepath.Dir(clean) || clean == filepath.VolumeName(clean)+string(filepath.Separator) {
		return reject(RejectFilesystemRoot, requestedCWD, "不能把文件系统根目录设为工作目录。")
	}

	home := filepath.Clean(homeRealpath)
	if home != "." && clean == home {
		return reject(RejectHomeRoot, requestedCWD, "不能把 Home 根目录设为工作目录，请选择更具体的子目录。")
	}
	if home != "." && clean == filepath.Dir(home) {
		return reject(RejectUserRoot, requestedCWD, "不能把用户目录根设为工作目录，请选择更具体的子目录。")
	}
	if home != "." && filepath.Dir(clean) == home {
		base := filepath.Base(clean)
		if base == "Desktop" || base == "Downloads" {
			return reject(RejectBroadUserFolder, requestedCWD, "这个目录范围过大，请选择更具体的子目录。")
		}
	}

	temp := filepath.Clean(os.TempDir())
	if clean == temp || clean == filepath.Clean(tempRealpath) || clean == "/tmp" || clean == "/private/tmp" {
		return reject(RejectTempRoot, requestedCWD, "不能把临时目录根设为工作目录，请选择更具体的子目录。")
	}

	if runtime.GOOS != "windows" {
		systemRoots := map[string]struct{}{
			"/Applications": {},
			"/bin":          {},
			"/etc":          {},
			"/Library":      {},
			"/private":      {},
			"/sbin":         {},
			"/System":       {},
			"/usr":          {},
			"/var":          {},
		}
		if _, ok := systemRoots[clean]; ok {
			return reject(RejectSystemRoot, requestedCWD, "不能把系统目录设为工作目录。")
		}
		if clean == "/Volumes" || filepath.Dir(clean) == "/Volumes" {
			return reject(RejectVolumeRoot, requestedCWD, "不能把磁盘卷根目录设为工作目录，请选择更具体的子目录。")
		}
	}

	return ResolveResult{OK: true}
}

func safeRealpath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs
	}
	return resolved
}

func safeHomeRealpath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return safeRealpath(home)
}
