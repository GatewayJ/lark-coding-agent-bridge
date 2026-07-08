package codexhistory

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	defaultHistoryTimeout = 5 * time.Second
	childExitGrace        = 250 * time.Millisecond
	maxScannerToken       = 4 * 1024 * 1024
	defaultPreviewMaxRune = 80
)

type ThreadSourceKind string

const (
	ThreadSourceCLI       ThreadSourceKind = "cli"
	ThreadSourceVSCode    ThreadSourceKind = "vscode"
	ThreadSourceExec      ThreadSourceKind = "exec"
	ThreadSourceAppServer ThreadSourceKind = "appServer"
	ThreadSourceUnknown   ThreadSourceKind = "unknown"
)

var defaultSourceKinds = []ThreadSourceKind{
	ThreadSourceCLI,
	ThreadSourceVSCode,
	ThreadSourceExec,
	ThreadSourceAppServer,
	ThreadSourceUnknown,
}

type ThreadHistoryEntry struct {
	ThreadID    string  `json:"threadId"`
	SessionID   string  `json:"sessionId,omitempty"`
	Preview     string  `json:"preview"`
	CWD         string  `json:"cwd"`
	CreatedAtMs int64   `json:"createdAtMs"`
	UpdatedAtMs int64   `json:"updatedAtMs"`
	Source      string  `json:"source"`
	Name        *string `json:"name,omitempty"`
}

type ListOptions struct {
	Binary           string
	CWD              string
	Limit            int
	ProfileStateDir  string
	CodexHome        string
	InheritCodexHome *bool
	Timeout          time.Duration
	SourceKinds      []ThreadSourceKind
	UseStateDBOnly   *bool
}

type ErrorCode string

const (
	ErrSpawnFailed       ErrorCode = "spawn-failed"
	ErrTimeout           ErrorCode = "timeout"
	ErrAppServer         ErrorCode = "app-server-error"
	ErrMalformedResponse ErrorCode = "malformed-response"
)

type HistoryError struct {
	Code ErrorCode
	Msg  string
	Err  error
}

func (e *HistoryError) Error() string {
	if e == nil {
		return ""
	}
	return e.Msg
}

func (e *HistoryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type ClientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

type ProviderOptions struct {
	Runner         CommandRunner
	Environ        func() []string
	DefaultTimeout time.Duration
	ClientInfo     ClientInfo
}

type Provider struct {
	runner         CommandRunner
	environ        func() []string
	defaultTimeout time.Duration
	clientInfo     ClientInfo
}

type CommandSpec struct {
	Binary string
	Args   []string
	Env    []string
}

type CommandRunner interface {
	Start(ctx context.Context, spec CommandSpec) (Process, error)
}

type Process interface {
	Stdin() io.Writer
	Stdout() io.Reader
	Stderr() io.Reader
	Kill() error
	Wait() error
}

func New(opts ProviderOptions) *Provider {
	runner := opts.Runner
	if runner == nil {
		runner = execRunner{}
	}
	environ := opts.Environ
	if environ == nil {
		environ = os.Environ
	}
	timeout := opts.DefaultTimeout
	if timeout <= 0 {
		timeout = defaultHistoryTimeout
	}
	return &Provider{
		runner:         runner,
		environ:        environ,
		defaultTimeout: timeout,
		clientInfo:     clientInfoWithDefaults(opts.ClientInfo),
	}
}

func ListThreadHistory(ctx context.Context, opts ListOptions) ([]ThreadHistoryEntry, error) {
	return New(ProviderOptions{}).List(ctx, opts)
}

func (p *Provider) List(ctx context.Context, opts ListOptions) ([]ThreadHistoryEntry, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = p.defaultTimeout
	}
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	proc, err := p.runner.Start(queryCtx, CommandSpec{
		Binary: opts.Binary,
		Args:   []string{"app-server", "--listen", "stdio://"},
		Env:    mergeEnv(p.environ(), envOverrides(opts)),
	})
	if err != nil {
		return nil, historyError(ErrSpawnFailed, errorMessage(err), err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- proc.Wait()
	}()

	stderr := newLimitedBuffer(maxScannerToken)
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(stderr, proc.Stderr())
	}()

	lines := scanLines(proc.Stdout())
	if _, err := io.WriteString(proc.Stdin(), requestPayload(p.clientInfo, opts)); err != nil {
		cleanupProcess(proc, waitCh)
		return nil, historyError(ErrSpawnFailed, errorMessage(err), err)
	}

	for {
		select {
		case <-queryCtx.Done():
			cleanupProcess(proc, waitCh)
			if errors.Is(queryCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
				return nil, historyError(ErrTimeout, fmt.Sprintf("codex history query timed out after %dms", timeout.Milliseconds()), nil)
			}
			return nil, queryCtx.Err()
		case err := <-waitCh:
			waitForStderr(stderrDone)
			if queryCtx.Err() != nil {
				if errors.Is(queryCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
					return nil, historyError(ErrTimeout, fmt.Sprintf("codex history query timed out after %dms", timeout.Milliseconds()), nil)
				}
				return nil, queryCtx.Err()
			}
			detail := waitDetail(err)
			if errText := strings.TrimSpace(stderr.String()); errText != "" {
				detail += ": " + errText
			}
			return nil, historyError(ErrSpawnFailed, "codex app-server exited before history response: "+detail, err)
		case line, ok := <-lines:
			if !ok {
				lines = nil
				continue
			}
			if line.err != nil {
				cleanupProcess(proc, waitCh)
				return nil, historyError(ErrSpawnFailed, "codex app-server stdout read error: "+line.err.Error(), line.err)
			}
			response, ok := parseRPCResponse(line.text)
			if !ok || response.ID != 2 {
				continue
			}
			if response.Error != nil {
				cleanupProcess(proc, waitCh)
				msg := response.Error.Message
				if msg == "" {
					msg = "codex app-server rejected history query"
				}
				return nil, historyError(ErrAppServer, msg, nil)
			}
			entries, err := parseThreadListResponse(response.Result)
			cleanupProcess(proc, waitCh)
			if err != nil {
				return nil, err
			}
			return entries, nil
		}
	}
}

type execRunner struct{}

func (execRunner) Start(ctx context.Context, spec CommandSpec) (Process, error) {
	cmd := exec.CommandContext(ctx, spec.Binary, spec.Args...)
	cmd.Env = spec.Env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execProcess{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}, nil
}

type execProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func (p *execProcess) Stdin() io.Writer  { return p.stdin }
func (p *execProcess) Stdout() io.Reader { return p.stdout }
func (p *execProcess) Stderr() io.Reader { return p.stderr }

func (p *execProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

func (p *execProcess) Wait() error {
	return p.cmd.Wait()
}

type stdoutLine struct {
	text string
	err  error
}

func scanLines(reader io.Reader) <-chan stdoutLine {
	out := make(chan stdoutLine, 16)
	go func() {
		defer close(out)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), maxScannerToken)
		for scanner.Scan() {
			text := strings.TrimSpace(scanner.Text())
			if text != "" {
				out <- stdoutLine{text: text}
			}
		}
		if err := scanner.Err(); err != nil {
			out <- stdoutLine{err: err}
		}
	}()
	return out
}

func cleanupProcess(proc Process, waitCh <-chan error) {
	_ = proc.Kill()
	select {
	case <-waitCh:
	case <-time.After(childExitGrace):
		_ = proc.Kill()
	}
}

func waitForStderr(done <-chan struct{}) {
	select {
	case <-done:
	case <-time.After(childExitGrace):
	}
}

func requestPayload(clientInfo ClientInfo, opts ListOptions) string {
	initializeBytes, _ := json.Marshal(initializeRequest(clientInfo))
	listBytes, _ := json.Marshal(listRequest(opts))
	return string(initializeBytes) + "\n" + string(listBytes) + "\n"
}

func initializeRequest(clientInfo ClientInfo) rpcRequest {
	return rpcRequest{
		Method: "initialize",
		ID:     1,
		Params: initializeParams{
			ClientInfo:   clientInfo,
			Capabilities: nil,
		},
	}
}

func listRequest(opts ListOptions) rpcRequest {
	useStateDBOnly := true
	if opts.UseStateDBOnly != nil {
		useStateDBOnly = *opts.UseStateDBOnly
	}
	sourceKinds := opts.SourceKinds
	if len(sourceKinds) == 0 {
		sourceKinds = defaultSourceKinds
	}
	kinds := make([]string, 0, len(sourceKinds))
	for _, kind := range sourceKinds {
		kinds = append(kinds, string(kind))
	}
	return rpcRequest{
		Method: "thread/list",
		ID:     2,
		Params: threadListParams{
			Limit:          opts.Limit,
			SortKey:        "updated_at",
			SortDirection:  "desc",
			Archived:       false,
			CWD:            opts.CWD,
			UseStateDBOnly: useStateDBOnly,
			SourceKinds:    kinds,
		},
	}
}

type rpcRequest struct {
	Method string `json:"method"`
	ID     int    `json:"id"`
	Params any    `json:"params"`
}

type initializeParams struct {
	ClientInfo   ClientInfo `json:"clientInfo"`
	Capabilities any        `json:"capabilities"`
}

type threadListParams struct {
	Limit          int      `json:"limit"`
	SortKey        string   `json:"sortKey"`
	SortDirection  string   `json:"sortDirection"`
	Archived       bool     `json:"archived"`
	CWD            string   `json:"cwd"`
	UseStateDBOnly bool     `json:"useStateDbOnly"`
	SourceKinds    []string `json:"sourceKinds"`
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Message string `json:"message"`
}

func parseRPCResponse(line string) (rpcResponse, bool) {
	var response rpcResponse
	if err := json.Unmarshal([]byte(line), &response); err != nil {
		return rpcResponse{}, false
	}
	return response, true
}

func parseThreadListResponse(input json.RawMessage) ([]ThreadHistoryEntry, error) {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if len(input) == 0 || json.Unmarshal(input, &envelope) != nil || len(envelope.Data) == 0 {
		return nil, historyError(ErrMalformedResponse, "codex app-server returned malformed thread/list response", nil)
	}
	var rawEntries []json.RawMessage
	if err := json.Unmarshal(envelope.Data, &rawEntries); err != nil {
		return nil, historyError(ErrMalformedResponse, "codex app-server returned malformed thread/list response", err)
	}
	entries := make([]ThreadHistoryEntry, 0, len(rawEntries))
	for _, raw := range rawEntries {
		entry, ok := normalizeThread(raw)
		if ok {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func normalizeThread(input json.RawMessage) (ThreadHistoryEntry, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(input, &raw); err != nil {
		return ThreadHistoryEntry{}, false
	}
	threadID, ok := stringField(raw, "id")
	if !ok || threadID == "" {
		return ThreadHistoryEntry{}, false
	}
	cwd, ok := stringField(raw, "cwd")
	if !ok || cwd == "" {
		return ThreadHistoryEntry{}, false
	}
	createdAt, _ := numberField(raw, "createdAt")
	updatedAt, _ := numberField(raw, "updatedAt")
	preview, _ := stringField(raw, "preview")
	normalizedPreview := normalizeSessionPreview(preview, defaultPreviewMaxRune)
	if normalizedPreview == "" {
		normalizedPreview = "(空会话)"
	}

	entry := ThreadHistoryEntry{
		ThreadID:    threadID,
		Preview:     normalizedPreview,
		CWD:         cwd,
		CreatedAtMs: int64(math.Round(createdAt * 1000)),
		UpdatedAtMs: int64(math.Round(updatedAt * 1000)),
		Source:      sourceValue(raw["source"]),
	}
	if sessionID, ok := stringField(raw, "sessionId"); ok && sessionID != "" {
		entry.SessionID = sessionID
	}
	if name, ok := stringField(raw, "name"); ok && name != "" {
		entry.Name = &name
	}
	return entry, true
}

var userInputSectionPattern = regexp.MustCompile(`(?s)<user_input>\n(.*?)\n</user_input>`)

func normalizeSessionPreview(input string, maxRunes int) string {
	text := extractBridgeUserInput(input)
	if text == "" {
		text = input
	}
	text = strings.Join(strings.Fields(text), " ")
	return truncateRunes(text, maxRunes)
}

func extractBridgeUserInput(input string) string {
	match := userInputSectionPattern.FindStringSubmatch(input)
	if match == nil {
		return ""
	}
	var section map[string]any
	if err := json.Unmarshal([]byte(match[1]), &section); err != nil {
		return ""
	}
	text, _ := section["text"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return text
}

func truncateRunes(input string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(input) <= maxRunes {
		return input
	}
	out := make([]rune, 0, maxRunes)
	for _, r := range input {
		out = append(out, r)
		if len(out) == maxRunes {
			break
		}
	}
	return string(out)
}

func stringField(raw map[string]json.RawMessage, key string) (string, bool) {
	value, ok := raw[key]
	if !ok {
		return "", false
	}
	var out string
	if err := json.Unmarshal(value, &out); err != nil {
		return "", false
	}
	return out, true
}

func numberField(raw map[string]json.RawMessage, key string) (float64, bool) {
	value, ok := raw[key]
	if !ok {
		return 0, false
	}
	var out float64
	if err := json.Unmarshal(value, &out); err != nil || math.IsNaN(out) || math.IsInf(out, 0) {
		return 0, false
	}
	return out, true
}

func sourceValue(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "unknown"
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return text
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		var compacted bytes.Buffer
		if err := json.Compact(&compacted, trimmed); err == nil {
			return compacted.String()
		}
	}
	return "unknown"
}

func envOverrides(opts ListOptions) map[string]string {
	overrides := map[string]string{}
	if opts.CodexHome != "" {
		overrides["CODEX_HOME"] = opts.CodexHome
		return overrides
	}
	if opts.InheritCodexHome != nil && !*opts.InheritCodexHome {
		overrides["CODEX_HOME"] = filepath.Join(opts.ProfileStateDir, "codex-home")
	}
	return overrides
}

func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	merged := make(map[string]string, len(base)+len(overrides))
	order := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, exists := merged[key]; !exists {
			order = append(order, key)
		}
		merged[key] = value
	}
	for key, value := range overrides {
		if _, exists := merged[key]; !exists {
			order = append(order, key)
		}
		merged[key] = value
	}
	out := make([]string, 0, len(order))
	for _, key := range order {
		out = append(out, key+"="+merged[key])
	}
	return out
}

func clientInfoWithDefaults(input ClientInfo) ClientInfo {
	if input.Name == "" {
		input.Name = "lark-channel-bridge"
	}
	if input.Title == "" {
		input.Title = "Lark Channel Bridge"
	}
	if input.Version == "" {
		input.Version = "0.2.3"
	}
	return input
}

func historyError(code ErrorCode, msg string, err error) *HistoryError {
	return &HistoryError{Code: code, Msg: msg, Err: err}
}

func waitDetail(err error) string {
	if err == nil {
		return "0"
	}
	return err.Error()
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type limitedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(input []byte) (int, error) {
	written := len(input)
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(input) > remaining {
			input = input[:remaining]
		}
		_, _ = b.buf.Write(input)
	}
	return written, nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
