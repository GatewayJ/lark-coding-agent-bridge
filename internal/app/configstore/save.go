package configstore

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func SaveRoot(path string, root RootConfig) error {
	data, err := FormatRootConfig(root)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o600)
}

func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	return writeFileAtomic(path, data, mode)
}

func FormatRootConfig(root RootConfig) ([]byte, error) {
	data, err := json.MarshalIndent(serializeRootConfig(root), "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	return data, nil
}

func WriteActiveProfile(rootDir string, profile string) error {
	return writeFileAtomic(filepath.Join(rootDir, "active-profile"), []byte(profile+"\n"), 0o600)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
