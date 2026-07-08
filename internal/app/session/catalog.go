package session

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/capability"
)

const catalogKeySeparator = "\x1f"

type CatalogStatus string

const (
	CatalogActive   CatalogStatus = "active"
	CatalogArchived CatalogStatus = "archived"
)

type CatalogIdentity struct {
	ScopeID           string        `json:"scopeId"`
	AgentID           capability.ID `json:"agentId"`
	CWDRealpath       string        `json:"cwdRealpath"`
	PolicyFingerprint string        `json:"policyFingerprint"`
}

type CatalogEntry struct {
	CatalogIdentity
	Key         string        `json:"key"`
	Status      CatalogStatus `json:"status"`
	UpdatedAt   int64         `json:"updatedAt"`
	SessionID   string        `json:"sessionId,omitempty"`
	ThreadID    string        `json:"threadId,omitempty"`
	LastSummary string        `json:"lastSummary,omitempty"`
}

type UpsertCatalogInput struct {
	CatalogIdentity
	Now         time.Time
	SessionID   string
	ThreadID    string
	LastSummary string
}

type ArchiveCatalogInput struct {
	CatalogIdentity
	Now time.Time
}

type CatalogGCOptions struct {
	Now                  time.Time
	MaxArchivedAge       time.Duration
	MaxEntriesPerScope   int
	MaxEntriesPerProfile int
}

type Catalog struct {
	mu      sync.Mutex
	path    string
	data    map[string]CatalogEntry
	lastErr error
	now     func() time.Time
}

func NewCatalog(path string) *Catalog {
	return &Catalog{
		path: path,
		data: map[string]CatalogEntry{},
		now:  time.Now,
	}
}

func SessionCatalogKey(input CatalogIdentity) string {
	return strings.Join([]string{
		input.ScopeID,
		string(input.AgentID),
		input.CWDRealpath,
		input.PolicyFingerprint,
	}, catalogKeySeparator)
}

func (c *Catalog) Load() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.path == "" {
		return nil
	}
	raw, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		c.data = map[string]CatalogEntry{}
		return nil
	}
	var parsed []CatalogEntry
	if err := json.Unmarshal(raw, &parsed); err != nil {
		c.data = map[string]CatalogEntry{}
		return nil
	}
	c.data = map[string]CatalogEntry{}
	for _, entry := range parsed {
		normalized, ok := normalizeCatalogEntry(entry)
		if !ok {
			continue
		}
		c.data[normalized.Key] = normalized
	}
	return nil
}

func (c *Catalog) ActiveFor(input CatalogIdentity) (CatalogEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.data[SessionCatalogKey(input)]
	if !ok || entry.Status != CatalogActive || !matchesIdentity(entry, input) || !isValidAgentEntry(entry) {
		return CatalogEntry{}, false
	}
	return entry, true
}

func (c *Catalog) UpsertActive(input UpsertCatalogInput) (CatalogEntry, error) {
	if err := assertAgentIdentity(input); err != nil {
		return CatalogEntry{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := input.Now
	if now.IsZero() {
		now = c.now()
	}
	entry := CatalogEntry{
		CatalogIdentity: input.CatalogIdentity,
		Key:             SessionCatalogKey(input.CatalogIdentity),
		Status:          CatalogActive,
		UpdatedAt:       now.UnixMilli(),
		SessionID:       input.SessionID,
		ThreadID:        input.ThreadID,
		LastSummary:     input.LastSummary,
	}
	c.data[entry.Key] = entry
	c.persistLocked()
	return entry, c.lastErr
}

func (c *Catalog) ArchiveActive(input ArchiveCatalogInput) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := SessionCatalogKey(input.CatalogIdentity)
	entry, ok := c.data[key]
	if !ok || entry.Status != CatalogActive {
		return false
	}
	now := input.Now
	if now.IsZero() {
		now = c.now()
	}
	entry.Status = CatalogArchived
	entry.UpdatedAt = now.UnixMilli()
	c.data[key] = entry
	c.persistLocked()
	return true
}

func (c *Catalog) Entries() []CatalogEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entriesLocked()
}

func (c *Catalog) GC(options CatalogGCOptions) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := options.Now
	if now.IsZero() {
		now = c.now()
	}
	maxArchivedAge := options.MaxArchivedAge
	if maxArchivedAge <= 0 {
		maxArchivedAge = 90 * 24 * time.Hour
	}
	maxEntriesPerScope := options.MaxEntriesPerScope
	if maxEntriesPerScope <= 0 {
		maxEntriesPerScope = 20
	}
	maxEntriesPerProfile := options.MaxEntriesPerProfile
	if maxEntriesPerProfile <= 0 {
		maxEntriesPerProfile = 1000
	}

	for key, entry := range c.data {
		if entry.Status == CatalogArchived && now.Sub(time.UnixMilli(entry.UpdatedAt)) > maxArchivedAge {
			delete(c.data, key)
		}
	}

	byScope := map[string][]CatalogEntry{}
	for _, entry := range c.data {
		byScope[entry.ScopeID] = append(byScope[entry.ScopeID], entry)
	}
	for _, scoped := range byScope {
		sort.Slice(scoped, func(i, j int) bool { return scoped[i].UpdatedAt > scoped[j].UpdatedAt })
		if len(scoped) > maxEntriesPerScope {
			for _, entry := range scoped[maxEntriesPerScope:] {
				delete(c.data, entry.Key)
			}
		}
	}

	all := c.entriesLocked()
	sort.Slice(all, func(i, j int) bool { return all[i].UpdatedAt > all[j].UpdatedAt })
	if len(all) > maxEntriesPerProfile {
		for _, entry := range all[maxEntriesPerProfile:] {
			delete(c.data, entry.Key)
		}
	}
	c.persistLocked()
}

func (c *Catalog) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

func (c *Catalog) ReplaceForTest(entries []CatalogEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = map[string]CatalogEntry{}
	for _, entry := range entries {
		c.data[entry.Key] = entry
	}
	c.persistLocked()
}

func (c *Catalog) entriesLocked() []CatalogEntry {
	entries := make([]CatalogEntry, 0, len(c.data))
	for _, entry := range c.data {
		entries = append(entries, entry)
	}
	return entries
}

func (c *Catalog) persistLocked() {
	if c.path == "" {
		return
	}
	payload, err := json.MarshalIndent(c.entriesLocked(), "", "  ")
	if err != nil {
		c.lastErr = err
		return
	}
	payload = append(payload, '\n')
	c.lastErr = writeFileAtomic(c.path, payload, 0o600)
}

func normalizeCatalogEntry(entry CatalogEntry) (CatalogEntry, bool) {
	if entry.Key == "" ||
		entry.ScopeID == "" ||
		(entry.AgentID != capability.IDClaude && entry.AgentID != capability.IDCodex) ||
		entry.CWDRealpath == "" ||
		entry.PolicyFingerprint == "" ||
		(entry.Status != CatalogActive && entry.Status != CatalogArchived) ||
		entry.UpdatedAt == 0 {
		return CatalogEntry{}, false
	}
	return entry, true
}

func matchesIdentity(entry CatalogEntry, input CatalogIdentity) bool {
	return entry.ScopeID == input.ScopeID &&
		entry.AgentID == input.AgentID &&
		entry.CWDRealpath == input.CWDRealpath &&
		entry.PolicyFingerprint == input.PolicyFingerprint &&
		entry.Key == SessionCatalogKey(input)
}

func isValidAgentEntry(entry CatalogEntry) bool {
	if entry.AgentID == capability.IDClaude {
		return entry.SessionID != "" && entry.ThreadID == ""
	}
	return entry.ThreadID != "" && entry.SessionID == ""
}

func assertAgentIdentity(input UpsertCatalogInput) error {
	if input.AgentID == capability.IDClaude {
		if input.SessionID == "" || input.ThreadID != "" {
			return errors.New("Claude catalog entries require sessionId and must not include threadId")
		}
		return nil
	}
	if input.ThreadID == "" || input.SessionID != "" {
		return errors.New("Codex catalog entries require threadId and must not include sessionId")
	}
	return nil
}
