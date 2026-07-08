package bridge

import (
	"context"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/adapters/codexhistory"
)

type CodexThreadSourceKind string

const (
	CodexThreadSourceCLI       CodexThreadSourceKind = "cli"
	CodexThreadSourceVSCode    CodexThreadSourceKind = "vscode"
	CodexThreadSourceExec      CodexThreadSourceKind = "exec"
	CodexThreadSourceAppServer CodexThreadSourceKind = "appServer"
	CodexThreadSourceUnknown   CodexThreadSourceKind = "unknown"
)

type CodexThreadHistoryEntry struct {
	ThreadID    string  `json:"threadId"`
	SessionID   string  `json:"sessionId,omitempty"`
	Preview     string  `json:"preview"`
	CWD         string  `json:"cwd"`
	CreatedAtMs int64   `json:"createdAtMs"`
	UpdatedAtMs int64   `json:"updatedAtMs"`
	Source      string  `json:"source"`
	Name        *string `json:"name,omitempty"`
}

type CodexHistoryOptions struct {
	CWD            string
	Limit          int
	Timeout        time.Duration
	SourceKinds    []CodexThreadSourceKind
	UseStateDBOnly *bool
}

func (c *Client) ListCodexThreads(ctx context.Context, options CodexHistoryOptions) ([]CodexThreadHistoryEntry, error) {
	if c == nil {
		return nil, ErrNilClient
	}
	listOptions := c.codexHistoryListOptions(options)
	entries, err := codexhistory.ListThreadHistory(ctx, listOptions)
	if err != nil {
		return nil, err
	}
	out := make([]CodexThreadHistoryEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, CodexThreadHistoryEntry(entry))
	}
	return out, nil
}

func (c *Client) codexHistoryListOptions(options CodexHistoryOptions) codexhistory.ListOptions {
	cfg := c.profile.Codex
	listOptions := codexhistory.ListOptions{
		CWD:             options.CWD,
		Limit:           options.Limit,
		Timeout:         options.Timeout,
		ProfileStateDir: c.profileStateDir,
		UseStateDBOnly:  options.UseStateDBOnly,
	}
	if cfg != nil {
		listOptions.Binary = cfg.BinaryPath
		listOptions.CodexHome = cfg.CodexHome
		inherit := cfg.InheritCodexHome
		listOptions.InheritCodexHome = &inherit
	}
	if listOptions.Binary == "" {
		listOptions.Binary = "codex"
	}
	if options.CWD == "" {
		listOptions.CWD = c.profile.Workspaces.Default
	}
	if listOptions.CWD == "" {
		listOptions.CWD = "."
	}
	listOptions.SourceKinds = make([]codexhistory.ThreadSourceKind, 0, len(options.SourceKinds))
	for _, source := range options.SourceKinds {
		listOptions.SourceKinds = append(listOptions.SourceKinds, codexhistory.ThreadSourceKind(source))
	}
	return listOptions
}
