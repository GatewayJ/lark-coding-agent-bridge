package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	SessionID          string `json:"sessionId,omitempty"`
	CWD                string `json:"cwd,omitempty"`
	UpdatedAt          int64  `json:"updatedAt"`
	IdleTimeoutMinutes *int   `json:"idleTimeoutMinutes,omitempty"`
}

type Store struct {
	mu      sync.Mutex
	path    string
	data    map[string]Entry
	lastErr error
	now     func() time.Time
}

func NewStore(path string) *Store {
	return &Store{
		path: path,
		data: map[string]Entry{},
		now:  time.Now,
	}
}

func (s *Store) SetNowForTest(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return nil
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var parsed map[string]Entry
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return err
	}
	s.data = map[string]Entry{}
	for scopeID, entry := range parsed {
		if entry.UpdatedAt == 0 {
			continue
		}
		hasSession := entry.SessionID != "" && entry.CWD != ""
		if !hasSession && entry.IdleTimeoutMinutes == nil {
			continue
		}
		s.data[scopeID] = entry
	}
	return nil
}

func (s *Store) ResumeFor(scopeID string, cwd string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.data[scopeID]
	if !ok || entry.CWD != cwd {
		return ""
	}
	return entry.SessionID
}

func (s *Store) GetRaw(scopeID string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.data[scopeID]
	return entry, ok
}

func (s *Store) Set(scopeID string, sessionID string, cwd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.data[scopeID]
	entry := Entry{
		SessionID: sessionID,
		CWD:       cwd,
		UpdatedAt: s.now().UnixMilli(),
	}
	if prev.IdleTimeoutMinutes != nil {
		value := *prev.IdleTimeoutMinutes
		entry.IdleTimeoutMinutes = &value
	}
	s.data[scopeID] = entry
	s.persistLocked()
}

func (s *Store) Clear(scopeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.data[scopeID]
	if !ok {
		return
	}
	if prev.IdleTimeoutMinutes != nil {
		value := *prev.IdleTimeoutMinutes
		s.data[scopeID] = Entry{
			IdleTimeoutMinutes: &value,
			UpdatedAt:          s.now().UnixMilli(),
		}
	} else {
		delete(s.data, scopeID)
	}
	s.persistLocked()
}

func (s *Store) GetIdleTimeoutMinutes(scopeID string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.data[scopeID]
	if !ok || entry.IdleTimeoutMinutes == nil {
		return 0, false
	}
	return *entry.IdleTimeoutMinutes, true
}

func (s *Store) SetIdleTimeoutMinutes(scopeID string, minutes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if minutes < 0 {
		minutes = 0
	}
	if minutes > 120 {
		minutes = 120
	}
	prev := s.data[scopeID]
	value := minutes
	prev.IdleTimeoutMinutes = &value
	prev.UpdatedAt = s.now().UnixMilli()
	s.data[scopeID] = prev
	s.persistLocked()
}

func (s *Store) ClearIdleTimeoutOverride(scopeID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.data[scopeID]
	if !ok || prev.IdleTimeoutMinutes == nil {
		return false
	}
	prev.IdleTimeoutMinutes = nil
	prev.UpdatedAt = s.now().UnixMilli()
	s.data[scopeID] = prev
	s.persistLocked()
	return true
}

func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

func (s *Store) ReplaceForTest(data map[string]Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = map[string]Entry{}
	for key, value := range data {
		s.data[key] = value
	}
	s.persistLocked()
}

func (s *Store) persistLocked() {
	if s.path == "" {
		return
	}
	payload, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		s.lastErr = err
		return
	}
	payload = append(payload, '\n')
	s.lastErr = writeFileAtomic(s.path, payload, 0o600)
}

func writeFileAtomic(path string, payload []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
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
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
