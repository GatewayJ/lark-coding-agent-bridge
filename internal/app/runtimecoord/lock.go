package runtimecoord

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultLockStale  = 30 * time.Second
	defaultLockUpdate = 10 * time.Second
)

type RuntimeLockConflictError struct {
	Kind   LockKind
	Target string
	Meta   *RuntimeLockMeta
	Err    error
}

func (e *RuntimeLockConflictError) Error() string {
	return fmt.Sprintf("runtime %s lock is already held: %s", e.Kind, e.Target)
}

func (e *RuntimeLockConflictError) Unwrap() error {
	return e.Err
}

type AcquiredLock struct {
	Kind   LockKind
	Target string

	metaFile string
	lockDir  string
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
}

func (l *AcquiredLock) Release() error {
	if l == nil {
		return nil
	}
	var err error
	l.once.Do(func() {
		close(l.stop)
		<-l.done
		_ = os.Remove(l.metaFile)
		err = os.Remove(l.lockDir)
	})
	return err
}

func (c *Coordinator) AcquireProfileLock() (*AcquiredLock, error) {
	return c.acquireRuntimeLock(RuntimeLockMeta{
		Kind:      LockProfile,
		Target:    c.paths.ProfileLockFile,
		Profile:   c.paths.Profile,
		AgentKind: c.agentKind,
	})
}

func (c *Coordinator) AcquireAppLock(appID string) (*AcquiredLock, error) {
	return c.acquireRuntimeLock(RuntimeLockMeta{
		Kind:      LockApp,
		Target:    c.paths.AppLockFile(appID),
		Profile:   c.paths.Profile,
		AgentKind: c.agentKind,
		AppID:     appID,
	})
}

func (c *Coordinator) acquireRuntimeLock(meta RuntimeLockMeta) (*AcquiredLock, error) {
	if err := os.MkdirAll(filepath.Dir(meta.Target), 0o700); err != nil {
		return nil, err
	}
	target, err := os.OpenFile(meta.Target, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	_ = target.Close()
	_ = os.Chmod(meta.Target, 0o600)

	lock, err := acquireDirLock(meta.Target, c.lockStale, c.lockUpdate)
	if err != nil {
		existing := readRuntimeLockMeta(meta.Target)
		return nil, &RuntimeLockConflictError{
			Kind:   meta.Kind,
			Target: meta.Target,
			Meta:   existing,
			Err:    err,
		}
	}

	meta.PID = c.pid
	meta.StartedAt = c.now().UTC().Format(time.RFC3339Nano)
	metaFile := RuntimeLockMetaFile(meta.Target)
	body, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		_ = lock.Release()
		return nil, err
	}
	if err := os.WriteFile(metaFile, append(body, '\n'), 0o600); err != nil {
		_ = lock.Release()
		return nil, err
	}
	_ = os.Chmod(metaFile, 0o600)
	lock.Kind = meta.Kind
	lock.metaFile = metaFile
	return lock, nil
}

func RuntimeLockMetaFile(target string) string {
	return target + ".meta.json"
}

type lockState struct {
	Locked    bool
	Meta      *RuntimeLockMeta
	Uncertain bool
	Err       error
}

func checkRuntimeLock(target string, stale time.Duration) lockState {
	lockDir := target + ".lock"
	info, err := os.Stat(lockDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return lockState{}
		}
		return lockState{Locked: true, Uncertain: true, Err: err}
	}
	if !info.IsDir() {
		return lockState{Locked: true, Uncertain: true, Err: fmt.Errorf("lock path is not a directory: %s", lockDir)}
	}
	if stale > 0 && time.Since(info.ModTime()) > stale {
		return lockState{}
	}
	meta := readRuntimeLockMeta(target)
	if meta == nil {
		return lockState{Locked: true, Uncertain: true, Err: errors.New("missing-or-invalid-runtime-lock-meta")}
	}
	return lockState{Locked: true, Meta: meta}
}

func readRuntimeLockMeta(target string) *RuntimeLockMeta {
	body, err := os.ReadFile(RuntimeLockMetaFile(target))
	if err != nil {
		return nil
	}
	var meta RuntimeLockMeta
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil
	}
	if !validLockMeta(meta) {
		return nil
	}
	return &meta
}

func validLockMeta(meta RuntimeLockMeta) bool {
	if meta.Kind != LockProfile && meta.Kind != LockApp {
		return false
	}
	if meta.Target == "" || meta.Profile == "" || meta.PID == 0 || meta.StartedAt == "" {
		return false
	}
	if meta.AgentKind != AgentClaude && meta.AgentKind != AgentCodex {
		return false
	}
	if meta.Kind == LockApp && meta.AppID == "" {
		return false
	}
	return true
}

func acquireDirLock(target string, stale, update time.Duration) (*AcquiredLock, error) {
	lockDir := target + ".lock"
	for {
		err := os.Mkdir(lockDir, 0o700)
		if err == nil {
			lock := &AcquiredLock{
				Target:  target,
				lockDir: lockDir,
				stop:    make(chan struct{}),
				done:    make(chan struct{}),
			}
			go lock.touchLoop(update)
			return lock, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		info, statErr := os.Stat(lockDir)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			return nil, statErr
		}
		if stale <= 0 || time.Since(info.ModTime()) <= stale {
			return nil, os.ErrExist
		}
		if removeErr := os.Remove(lockDir); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return nil, os.ErrExist
		}
	}
}

func (l *AcquiredLock) touchLoop(update time.Duration) {
	if update <= 0 {
		update = defaultLockUpdate
	}
	_ = os.Chtimes(l.lockDir, time.Now(), time.Now())
	ticker := time.NewTicker(update)
	defer func() {
		ticker.Stop()
		close(l.done)
	}()
	for {
		select {
		case <-ticker.C:
			_ = os.Chtimes(l.lockDir, time.Now(), time.Now())
		case <-l.stop:
			return
		}
	}
}
