//go:build windows

package larkcli

import (
	"os"
	"path/filepath"
	"sync"
)

var windowsConfigFileLock sync.Mutex

func withConfigFileLock(configPath string, fn func() error) error {
	lockTarget := configPath + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockTarget), 0o700); err != nil {
		return err
	}
	lockFile, err := os.OpenFile(lockTarget, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lockFile.Close()
	_ = os.Chmod(lockTarget, 0o600)

	windowsConfigFileLock.Lock()
	defer windowsConfigFileLock.Unlock()
	return fn()
}
