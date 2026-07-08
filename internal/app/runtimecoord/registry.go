package runtimecoord

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type registryFile struct {
	Entries []ProcessEntry `json:"entries"`
}

func (c *Coordinator) RegisterProcess(opts StartOptions) (ProcessEntry, error) {
	entry := ProcessEntry{
		ID:          generateShortID(),
		PID:         c.pid,
		AppID:       opts.AppID,
		Tenant:      normalizeTenant(opts.Tenant),
		ProfileName: c.paths.Profile,
		AgentKind:   normalizeAgentKind(opts.AgentKind),
		ConfigPath:  opts.ConfigPath,
		StartedAt:   c.now().UTC().Format(time.RFC3339Nano),
		Version:     firstNonEmpty(opts.Version, c.version),
	}
	if entry.AgentKind == "" {
		entry.AgentKind = c.agentKind
	}
	err := c.withRegistryLock(func() error {
		live, _, err := c.readAndPruneProcessesLocked()
		if err != nil {
			return err
		}
		live = append(live, entry)
		return writeRegistryFile(c.paths.UserRegistryFile, live)
	})
	return entry, err
}

func (c *Coordinator) ReadAndPruneProcesses() ([]ProcessEntry, error) {
	var entries []ProcessEntry
	err := c.withRegistryLock(func() error {
		live, pruned, err := c.readAndPruneProcessesLocked()
		if err != nil {
			return err
		}
		if pruned {
			if err := writeRegistryFile(c.paths.UserRegistryFile, live); err != nil {
				return err
			}
		}
		entries = live
		return nil
	})
	return entries, err
}

func (c *Coordinator) unregisterProcess(id string) error {
	if id == "" {
		return nil
	}
	return c.withRegistryLock(func() error {
		live, pruned, err := c.readAndPruneProcessesLocked()
		if err != nil {
			return err
		}
		next := live[:0]
		removed := false
		for _, entry := range live {
			if entry.ID == id {
				removed = true
				continue
			}
			next = append(next, entry)
		}
		if !removed && !pruned {
			return nil
		}
		return writeRegistryFile(c.paths.UserRegistryFile, next)
	})
}

func (c *Coordinator) updateProcess(id string, patch ProcessEntry) error {
	return c.withRegistryLock(func() error {
		live, pruned, err := c.readAndPruneProcessesLocked()
		if err != nil {
			return err
		}
		changed := false
		for i := range live {
			if live[i].ID != id {
				continue
			}
			if patch.AppID != "" {
				live[i].AppID = patch.AppID
			}
			if patch.Tenant != "" {
				live[i].Tenant = patch.Tenant
			}
			if patch.ConfigPath != "" {
				live[i].ConfigPath = patch.ConfigPath
			}
			if patch.BotName != "" {
				live[i].BotName = patch.BotName
			}
			changed = true
		}
		if !changed && !pruned {
			return nil
		}
		return writeRegistryFile(c.paths.UserRegistryFile, live)
	})
}

func (c *Coordinator) sameAppLiveOthers(appID string) ([]ProcessEntry, error) {
	entries, err := c.ReadAndPruneProcesses()
	if err != nil {
		return nil, err
	}
	var out []ProcessEntry
	for _, entry := range entries {
		if entry.AppID == appID && entry.PID != c.pid {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (c *Coordinator) readAndPruneProcessesLocked() ([]ProcessEntry, bool, error) {
	raw := readRegistryFile(c.paths.UserRegistryFile)
	live := make([]ProcessEntry, 0, len(raw))
	for _, entry := range raw {
		stale, err := c.isEntryStale(entry)
		if err != nil {
			return nil, false, err
		}
		if !stale {
			live = append(live, entry)
		}
	}
	return live, len(live) != len(raw), nil
}

func (c *Coordinator) isEntryStale(entry ProcessEntry) (bool, error) {
	paths, err := c.pathsForProfile(entry.ProfileName)
	if err != nil {
		return true, nil
	}
	profileLock := checkRuntimeLock(paths.ProfileLockFile, c.lockStale)
	appLock := checkRuntimeLock(paths.AppLockFile(entry.AppID), c.lockStale)
	profileOK, err := lockMatchesEntry(profileLock, entry, LockProfile)
	if err != nil {
		return false, err
	}
	appOK, err := lockMatchesEntry(appLock, entry, LockApp)
	if err != nil {
		return false, err
	}
	return !profileOK || !appOK, nil
}

func lockMatchesEntry(state lockState, entry ProcessEntry, kind LockKind) (bool, error) {
	if state.Uncertain {
		return false, state.Err
	}
	if !state.Locked || state.Meta == nil {
		return false, nil
	}
	meta := state.Meta
	if meta.Kind != kind || meta.Profile != entry.ProfileName || meta.AgentKind != entry.AgentKind || meta.PID != entry.PID {
		return false, nil
	}
	if kind == LockApp && meta.AppID != entry.AppID {
		return false, nil
	}
	return true, nil
}

func (c *Coordinator) withRegistryLock(fn func() error) error {
	if err := ensureRegistryFile(c.paths.UserRegistryFile); err != nil {
		return err
	}
	lock, err := acquireDirLock(c.paths.UserRegistryFile, c.lockStale, c.lockUpdate)
	if err != nil {
		return err
	}
	defer func() {
		_ = lock.Release()
	}()
	return fn()
}

func ensureRegistryFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries := []ProcessEntry(nil)
	if legacy := legacyRegistryFile(path); legacy != "" && legacy != path {
		entries = readRegistryFile(legacy)
	}
	body, err := json.MarshalIndent(registryFile{Entries: entries}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o600)
}

func readRegistryFile(path string) []ProcessEntry {
	body, err := os.ReadFile(path)
	if err != nil {
		legacy := legacyRegistryFile(path)
		if legacy != "" && legacy != path {
			return readRegistryFile(legacy)
		}
		return nil
	}
	var file registryFile
	if err := json.Unmarshal(body, &file); err != nil {
		return nil
	}
	out := file.Entries[:0]
	for _, entry := range file.Entries {
		if validEntry(entry) {
			out = append(out, entry)
		}
	}
	return out
}

func writeRegistryFile(path string, entries []ProcessEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(registryFile{Entries: entries}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	fsyncDir(filepath.Dir(path))
	return nil
}

func legacyRegistryFile(path string) string {
	if filepath.Base(path) != "processes.json" {
		return ""
	}
	parent := filepath.Dir(path)
	if filepath.Base(parent) != "registry" {
		return ""
	}
	if _, err := os.Stat(path); err == nil {
		return ""
	}
	return filepath.Join(filepath.Dir(parent), "processes.json")
}

func validEntry(entry ProcessEntry) bool {
	if entry.ID == "" || entry.PID == 0 || entry.AppID == "" || entry.ProfileName == "" || entry.ConfigPath == "" || entry.StartedAt == "" || entry.Version == "" {
		return false
	}
	if entry.Tenant != TenantFeishu && entry.Tenant != TenantLark {
		return false
	}
	if entry.AgentKind != AgentClaude && entry.AgentKind != AgentCodex {
		return false
	}
	return true
}

func generateShortID() string {
	var buf [2]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "0000"
	}
	return hex.EncodeToString(buf[:])
}
