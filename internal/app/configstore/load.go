package configstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func Load(path string, options LoadOptions) (*Snapshot, error) {
	if path == "" {
		return nil, errors.New("config path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if options.ActiveProfile == "" && options.Profile == "" {
		activeProfile, err := readActiveProfile(filepath.Dir(path))
		if err != nil {
			return nil, err
		}
		options.ActiveProfile = activeProfile
	}
	return Normalize(data, options)
}

func readActiveProfile(rootDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(rootDir, "active-profile"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
