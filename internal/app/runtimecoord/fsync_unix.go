//go:build !windows

package runtimecoord

import "os"

func fsyncDir(path string) {
	dir, err := os.Open(path)
	if err != nil {
		return
	}
	defer dir.Close()
	_ = dir.Sync()
}
