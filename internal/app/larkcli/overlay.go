package larkcli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type OverlayMarker struct {
	HadConfig bool   `json:"hadConfig"`
	Profile   string `json:"profile,omitempty"`
}

type OverlayPaths struct {
	BackupFile string
	MarkerFile string
}

func LegacyLarkCliSourceOverlayPaths(configFile string) OverlayPaths {
	dir := filepath.Dir(configFile)
	return OverlayPaths{
		BackupFile: filepath.Join(dir, ".config.json.lark-cli-bind-backup"),
		MarkerFile: filepath.Join(dir, ".config.json.lark-cli-bind-marker"),
	}
}

func RecoverLegacyLarkCliSourceOverlay(configFile string) error {
	return withConfigFileLock(configFile, func() error {
		return recoverLegacyLarkCliSourceOverlayUnlocked(configFile)
	})
}

func HasLegacyLarkCliSourceOverlay(configFile string) (bool, error) {
	paths := LegacyLarkCliSourceOverlayPaths(configFile)
	if _, err := os.Stat(paths.MarkerFile); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func WithLegacyLarkCliSourceOverlay(configFile string, sourceConfigFile string, fn func() error) error {
	return withConfigFileLock(configFile, func() error {
		if err := recoverLegacyLarkCliSourceOverlayUnlocked(configFile); err != nil {
			return err
		}

		paths := LegacyLarkCliSourceOverlayPaths(configFile)
		original, hadConfig, err := readOptional(configFile)
		if err != nil {
			return err
		}
		if hadConfig {
			if err := writeFileAtomic(paths.BackupFile, original, 0o600); err != nil {
				return err
			}
		} else if err := removeIfExists(paths.BackupFile); err != nil {
			return err
		}

		marker := OverlayMarker{HadConfig: hadConfig}
		markerData, err := json.MarshalIndent(marker, "", "  ")
		if err != nil {
			return err
		}
		markerData = append(markerData, '\n')
		if err := writeFileAtomic(paths.MarkerFile, markerData, 0o600); err != nil {
			return err
		}

		source, err := os.ReadFile(sourceConfigFile)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(configFile, source, 0o600); err != nil {
			return err
		}

		var callbackErr error
		var panicValue any
		func() {
			defer func() {
				panicValue = recover()
			}()
			callbackErr = fn()
		}()
		restoreErr := restoreLegacyLarkCliSourceOverlayUnlocked(configFile, nil)
		if panicValue != nil {
			panic(panicValue)
		}
		return errors.Join(callbackErr, restoreErr)
	})
}

func recoverLegacyLarkCliSourceOverlayUnlocked(configFile string) error {
	marker, err := readMarker(configFile)
	if err != nil || marker == nil {
		return err
	}
	return restoreLegacyLarkCliSourceOverlayUnlocked(configFile, marker)
}

func restoreLegacyLarkCliSourceOverlayUnlocked(configFile string, markerArg *OverlayMarker) error {
	marker := markerArg
	var err error
	if marker == nil {
		marker, err = readMarker(configFile)
		if err != nil || marker == nil {
			return err
		}
	}

	paths := LegacyLarkCliSourceOverlayPaths(configFile)
	if marker.HadConfig {
		backup, err := os.ReadFile(paths.BackupFile)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(configFile, backup, 0o600); err != nil {
			return err
		}
	} else if err := removeIfExists(configFile); err != nil {
		return err
	}

	if err := removeIfExists(paths.BackupFile); err != nil {
		return err
	}
	return removeIfExists(paths.MarkerFile)
}

func readMarker(configFile string) (*OverlayMarker, error) {
	paths := LegacyLarkCliSourceOverlayPaths(configFile)
	data, err := os.ReadFile(paths.MarkerFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var partial struct {
		HadConfig bool   `json:"hadConfig"`
		Profile   string `json:"profile"`
	}
	if err := json.Unmarshal(data, &partial); err != nil {
		return nil, err
	}
	return &OverlayMarker{HadConfig: partial.HadConfig, Profile: partial.Profile}, nil
}

func readOptional(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
