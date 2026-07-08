package cardauth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type NonceState string

const (
	NonceUsed    NonceState = "used"
	NonceRevoked NonceState = "revoked"
)

type NonceStore struct {
	mu      sync.Mutex
	path    string
	nonces  map[string]NonceState
	lastErr error
}

func NewNonceStore(path string) *NonceStore {
	return &NonceStore{
		path:   path,
		nonces: map[string]NonceState{},
	}
}

func (s *NonceStore) Load() error {
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
	var parsed map[string]NonceState
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return err
	}
	s.nonces = map[string]NonceState{}
	for nonce, state := range parsed {
		if state == NonceUsed || state == NonceRevoked {
			s.nonces[nonce] = state
		}
	}
	return nil
}

func (s *NonceStore) State(nonce string) (NonceState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.nonces[nonce]
	return state, ok
}

func (s *NonceStore) Consume(nonce string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nonces[nonce]; ok {
		return false
	}
	s.nonces[nonce] = NonceUsed
	s.persistLocked()
	return true
}

func (s *NonceStore) Revoke(nonce string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nonces[nonce] = NonceRevoked
	s.persistLocked()
}

func (s *NonceStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

func (s *NonceStore) persistLocked() {
	if s.path == "" {
		return
	}
	payload, err := json.MarshalIndent(s.nonces, "", "  ")
	if err != nil {
		s.lastErr = err
		return
	}
	payload = append(payload, '\n')
	s.lastErr = writeAtomic(s.path, payload, 0o600)
}

func writeAtomic(path string, payload []byte, mode os.FileMode) error {
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
