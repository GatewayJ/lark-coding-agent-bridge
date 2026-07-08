package bridge

import (
	"encoding/json"
	"errors"
	"os"
	"sync"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/configstore"
)

// FileWorkspaceStore persists chat and named workspaces using the same
// workspaces.json format as the JavaScript implementation.
type FileWorkspaceStore struct {
	mu      sync.Mutex
	path    string
	chats   map[string]fileWorkspaceChat
	named   map[string]string
	lastErr error
}

type fileWorkspaceChat struct {
	CWD string `json:"cwd"`
}

type fileWorkspaceData struct {
	Chats map[string]fileWorkspaceChat `json:"chats"`
	Named map[string]string            `json:"named"`
}

// NewFileWorkspaceStore loads a persistent CommandWorkspaceStore from path.
// A missing file is treated as an empty store.
func NewFileWorkspaceStore(path string) (*FileWorkspaceStore, error) {
	if path == "" {
		return nil, errors.New("workspace store path is required")
	}
	store := &FileWorkspaceStore{
		path:  path,
		chats: map[string]fileWorkspaceChat{},
		named: map[string]string{},
	}
	if err := store.Load(); err != nil {
		return nil, err
	}
	return store, nil
}

// Load refreshes the store from disk.
func (s *FileWorkspaceStore) Load() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.chats = map[string]fileWorkspaceChat{}
			s.named = map[string]string{}
			s.lastErr = nil
			return nil
		}
		s.lastErr = err
		return err
	}
	var parsed fileWorkspaceData
	if err := json.Unmarshal(data, &parsed); err != nil {
		s.lastErr = err
		return err
	}
	s.chats = cloneFileWorkspaceChats(parsed.Chats)
	s.named = cloneStringMap(parsed.Named)
	s.lastErr = nil
	return nil
}

// Flush writes the current in-memory state to disk.
func (s *FileWorkspaceStore) Flush() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistLocked()
}

// LastError returns the last load or persist error observed by the store.
func (s *FileWorkspaceStore) LastError() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

func (s *FileWorkspaceStore) CWDFor(scopeID string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chats[scopeID].CWD
}

func (s *FileWorkspaceStore) SetCWD(scopeID string, cwd string) error {
	if s == nil || scopeID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.chats[scopeID]
	s.chats[scopeID] = fileWorkspaceChat{CWD: cwd}
	if err := s.persistLocked(); err != nil {
		if existed {
			s.chats[scopeID] = previous
		} else {
			delete(s.chats, scopeID)
		}
		return err
	}
	return nil
}

// ListCWDs returns the chat/thread scoped working directories.
func (s *FileWorkspaceStore) ListCWDs() map[string]string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.chats))
	for scopeID, chat := range s.chats {
		out[scopeID] = chat.CWD
	}
	return out
}

// RemoveCWD removes a chat/thread scoped working directory.
func (s *FileWorkspaceStore) RemoveCWD(scopeID string) (bool, error) {
	if s == nil {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, ok := s.chats[scopeID]
	if !ok {
		return false, nil
	}
	delete(s.chats, scopeID)
	if err := s.persistLocked(); err != nil {
		s.chats[scopeID] = previous
		return false, err
	}
	return true, nil
}

func (s *FileWorkspaceStore) ListNamed() map[string]string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneStringMap(s.named)
}

func (s *FileWorkspaceStore) GetNamed(name string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.named[name]
}

func (s *FileWorkspaceStore) SaveNamed(name string, cwd string) error {
	if s == nil || name == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.named[name]
	s.named[name] = cwd
	if err := s.persistLocked(); err != nil {
		if existed {
			s.named[name] = previous
		} else {
			delete(s.named, name)
		}
		return err
	}
	return nil
}

func (s *FileWorkspaceStore) RemoveNamed(name string) (bool, error) {
	if s == nil {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, ok := s.named[name]
	if !ok {
		return false, nil
	}
	delete(s.named, name)
	if err := s.persistLocked(); err != nil {
		s.named[name] = previous
		return false, err
	}
	return true, nil
}

func (s *FileWorkspaceStore) persistLocked() error {
	payload, err := json.MarshalIndent(fileWorkspaceData{
		Chats: s.chats,
		Named: s.named,
	}, "", "  ")
	if err != nil {
		s.lastErr = err
		return err
	}
	payload = append(payload, '\n')
	if err := configstore.WriteFileAtomic(s.path, payload, 0o600); err != nil {
		s.lastErr = err
		return err
	}
	s.lastErr = nil
	return nil
}

func cloneFileWorkspaceChats(input map[string]fileWorkspaceChat) map[string]fileWorkspaceChat {
	out := make(map[string]fileWorkspaceChat, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneStringMap(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
