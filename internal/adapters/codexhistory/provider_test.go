package codexhistory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	promptport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/presentation/prompt"
)

func TestProviderListsThreadHistoryThroughAppServer(t *testing.T) {
	runner := &fakeRunner{}
	provider := New(ProviderOptions{
		Runner:  runner,
		Environ: func() []string { return []string{"CODEX_HOME=/outer/codex-home", "KEEP=1"} },
	})

	entries, err := provider.List(context.Background(), ListOptions{
		Binary:          "/usr/local/bin/codex",
		CWD:             "/repo",
		Limit:           2,
		ProfileStateDir: "/state/profile",
		Timeout:         time.Second,
	})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	wantName := "New work"
	want := []ThreadHistoryEntry{
		{
			ThreadID:    "thread-new",
			SessionID:   "session-new",
			Preview:     "new thread prompt",
			CWD:         "/repo",
			CreatedAtMs: 1_700_000_000_000,
			UpdatedAtMs: 1_700_000_050_000,
			Source:      "exec",
			Name:        &wantName,
		},
		{
			ThreadID:    "thread-old",
			SessionID:   "session-old",
			Preview:     "(空会话)",
			CWD:         "/repo",
			CreatedAtMs: 1_699_999_000_000,
			UpdatedAtMs: 1_699_999_500_000,
			Source:      "cli",
		},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("entries mismatch\nwant: %#v\n got: %#v", want, entries)
	}

	spec := runner.firstSpec(t)
	if spec.Binary != "/usr/local/bin/codex" {
		t.Fatalf("binary = %q", spec.Binary)
	}
	if !reflect.DeepEqual(spec.Args, []string{"app-server", "--listen", "stdio://"}) {
		t.Fatalf("args = %#v", spec.Args)
	}
	if got := envMap(spec.Env)["CODEX_HOME"]; got != "/outer/codex-home" {
		t.Fatalf("CODEX_HOME = %q", got)
	}

	requests := runner.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("expected 2 requests, got %#v", requests)
	}
	if requests[0]["method"] != "initialize" {
		t.Fatalf("first request = %#v", requests[0])
	}
	params := requests[1]["params"].(map[string]any)
	if requests[1]["method"] != "thread/list" ||
		params["cwd"] != "/repo" ||
		params["limit"] != float64(2) ||
		params["archived"] != false ||
		params["sortKey"] != "updated_at" ||
		params["sortDirection"] != "desc" ||
		params["useStateDbOnly"] != true {
		t.Fatalf("thread/list request = %#v", requests[1])
	}
	if !reflect.DeepEqual(params["sourceKinds"], []any{"cli", "vscode", "exec", "appServer", "unknown"}) {
		t.Fatalf("sourceKinds = %#v", params["sourceKinds"])
	}
}

func TestProviderUsesProfileLocalCodexHomeWhenInheritanceDisabled(t *testing.T) {
	inherit := false
	runner := &fakeRunner{}
	provider := New(ProviderOptions{
		Runner:  runner,
		Environ: func() []string { return []string{"CODEX_HOME=/outer/codex-home"} },
	})
	profileStateDir := filepath.Join(t.TempDir(), "profile")

	if _, err := provider.List(context.Background(), ListOptions{
		Binary:           "codex",
		CWD:              "/repo",
		Limit:            1,
		ProfileStateDir:  profileStateDir,
		InheritCodexHome: &inherit,
		Timeout:          time.Second,
	}); err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	want := filepath.Join(profileStateDir, "codex-home")
	if got := envMap(runner.firstSpec(t).Env)["CODEX_HOME"]; got != want {
		t.Fatalf("CODEX_HOME = %q, want %q", got, want)
	}
}

func TestProviderUsesExplicitCodexHome(t *testing.T) {
	inherit := false
	runner := &fakeRunner{}
	provider := New(ProviderOptions{
		Runner:  runner,
		Environ: func() []string { return []string{"CODEX_HOME=/outer/codex-home"} },
	})

	if _, err := provider.List(context.Background(), ListOptions{
		Binary:           "codex",
		CWD:              "/repo",
		Limit:            1,
		ProfileStateDir:  "/state/profile",
		CodexHome:        "/custom/codex-home",
		InheritCodexHome: &inherit,
		Timeout:          time.Second,
	}); err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	if got := envMap(runner.firstSpec(t).Env)["CODEX_HOME"]; got != "/custom/codex-home" {
		t.Fatalf("CODEX_HOME = %q", got)
	}
}

func TestProviderPassesOptionalThreadListParams(t *testing.T) {
	useStateDBOnly := false
	runner := &fakeRunner{}
	provider := New(ProviderOptions{Runner: runner})

	if _, err := provider.List(context.Background(), ListOptions{
		Binary:         "codex",
		CWD:            "/repo",
		Limit:          3,
		SourceKinds:    []ThreadSourceKind{ThreadSourceCLI, ThreadSourceExec},
		UseStateDBOnly: &useStateDBOnly,
		Timeout:        time.Second,
	}); err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	requests := runner.requestsSnapshot()
	params := requests[1]["params"].(map[string]any)
	if params["useStateDbOnly"] != false {
		t.Fatalf("useStateDbOnly = %#v", params["useStateDbOnly"])
	}
	if !reflect.DeepEqual(params["sourceKinds"], []any{"cli", "exec"}) {
		t.Fatalf("sourceKinds = %#v", params["sourceKinds"])
	}
}

func TestProviderThrowsTypedErrorWhenAppServerRejectsHistory(t *testing.T) {
	runner := &fakeRunner{failList: true}
	provider := New(ProviderOptions{Runner: runner})

	_, err := provider.List(context.Background(), ListOptions{
		Binary:  "codex",
		CWD:     "/repo",
		Limit:   1,
		Timeout: time.Second,
	})
	var historyErr *HistoryError
	if !errors.As(err, &historyErr) {
		t.Fatalf("expected HistoryError, got %T %v", err, err)
	}
	if historyErr.Code != ErrAppServer || historyErr.Error() != "history unavailable" {
		t.Fatalf("unexpected history error: %#v", historyErr)
	}
}

func TestProviderThrowsTypedErrorWhenSpawnFails(t *testing.T) {
	runner := &fakeRunner{startErr: errors.New("codex missing")}
	provider := New(ProviderOptions{Runner: runner})

	_, err := provider.List(context.Background(), ListOptions{
		Binary:  "missing-codex",
		CWD:     "/repo",
		Limit:   1,
		Timeout: time.Second,
	})
	var historyErr *HistoryError
	if !errors.As(err, &historyErr) {
		t.Fatalf("expected HistoryError, got %T %v", err, err)
	}
	if historyErr.Code != ErrSpawnFailed || historyErr.Error() != "codex missing" {
		t.Fatalf("unexpected history error: %#v", historyErr)
	}
}

func TestProviderThrowsTypedErrorOnMalformedThreadListResponse(t *testing.T) {
	runner := &fakeRunner{malformedList: true}
	provider := New(ProviderOptions{Runner: runner})

	_, err := provider.List(context.Background(), ListOptions{
		Binary:  "codex",
		CWD:     "/repo",
		Limit:   1,
		Timeout: time.Second,
	})
	var historyErr *HistoryError
	if !errors.As(err, &historyErr) {
		t.Fatalf("expected HistoryError, got %T %v", err, err)
	}
	if historyErr.Code != ErrMalformedResponse {
		t.Fatalf("code = %q", historyErr.Code)
	}
}

func TestProviderTimesOutWhenAppServerDoesNotReturnHistory(t *testing.T) {
	runner := &fakeRunner{noListResponse: true}
	provider := New(ProviderOptions{Runner: runner})

	_, err := provider.List(context.Background(), ListOptions{
		Binary:  "codex",
		CWD:     "/repo",
		Limit:   1,
		Timeout: 20 * time.Millisecond,
	})
	var historyErr *HistoryError
	if !errors.As(err, &historyErr) {
		t.Fatalf("expected HistoryError, got %T %v", err, err)
	}
	if historyErr.Code != ErrTimeout {
		t.Fatalf("code = %q", historyErr.Code)
	}
}

func TestProviderSummarizesBridgePrefixedCodexPreviews(t *testing.T) {
	firstPreview := "# lark-channel-bridge 运行约定\n\n## user_message\n\n" + promptport.BuildAgentPrompt(promptport.BuildAgentPromptInput{
		Context: promptport.BridgePromptContext{
			ChatID:   "oc_secret",
			ChatType: "p2p",
			SenderID: "ou_secret",
			Source:   promptport.BridgePromptSourceIM,
		},
		Instructions: []string{"internal bridge instruction"},
		UserInput:    "Codex 真实用户问题\n\n第二行",
	})
	runner := &fakeRunner{firstPreview: firstPreview}
	provider := New(ProviderOptions{Runner: runner})

	entries, err := provider.List(context.Background(), ListOptions{
		Binary:  "codex",
		CWD:     "/repo",
		Limit:   1,
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if got := entries[0].Preview; got != "Codex 真实用户问题 第二行" {
		t.Fatalf("preview = %q", got)
	}
}

type fakeRunner struct {
	mu             sync.Mutex
	specs          []CommandSpec
	requests       []map[string]any
	failList       bool
	malformedList  bool
	noListResponse bool
	firstPreview   string
	startErr       error
}

func (r *fakeRunner) Start(ctx context.Context, spec CommandSpec) (Process, error) {
	if r.startErr != nil {
		return nil, r.startErr
	}
	r.mu.Lock()
	r.specs = append(r.specs, CommandSpec{
		Binary: spec.Binary,
		Args:   append([]string(nil), spec.Args...),
		Env:    append([]string(nil), spec.Env...),
	})
	r.mu.Unlock()

	return newFakeProcess(ctx, r), nil
}

func (r *fakeRunner) firstSpec(t *testing.T) CommandSpec {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.specs) == 0 {
		t.Fatal("runner was not started")
	}
	return r.specs[0]
}

func (r *fakeRunner) requestsSnapshot() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]any, len(r.requests))
	copy(out, r.requests)
	return out
}

func (r *fakeRunner) recordRequest(req map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
}

type fakeProcess struct {
	ctx      context.Context
	runner   *fakeRunner
	stdin    *fakeStdin
	stdoutR  *io.PipeReader
	stdoutW  *io.PipeWriter
	stderrR  *io.PipeReader
	stderrW  *io.PipeWriter
	done     chan struct{}
	killOnce sync.Once
}

func newFakeProcess(ctx context.Context, runner *fakeRunner) *fakeProcess {
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	proc := &fakeProcess{
		ctx:     ctx,
		runner:  runner,
		stdoutR: stdoutR,
		stdoutW: stdoutW,
		stderrR: stderrR,
		stderrW: stderrW,
		done:    make(chan struct{}),
	}
	proc.stdin = &fakeStdin{proc: proc}
	return proc
}

func (p *fakeProcess) Stdin() io.Writer  { return p.stdin }
func (p *fakeProcess) Stdout() io.Reader { return p.stdoutR }
func (p *fakeProcess) Stderr() io.Reader { return p.stderrR }

func (p *fakeProcess) Kill() error {
	p.killOnce.Do(func() {
		_ = p.stdoutW.Close()
		_ = p.stderrW.Close()
		close(p.done)
	})
	return nil
}

func (p *fakeProcess) Wait() error {
	select {
	case <-p.done:
		return nil
	case <-p.ctx.Done():
		_ = p.Kill()
		return p.ctx.Err()
	}
}

func (p *fakeProcess) handleLine(line string) error {
	var req map[string]any
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		return err
	}
	p.runner.recordRequest(req)

	method, _ := req["method"].(string)
	id, _ := req["id"].(float64)
	switch method {
	case "initialize":
		return p.writeJSON(map[string]any{
			"id": id,
			"result": map[string]any{
				"userAgent":      "fake-codex",
				"codexHome":      "",
				"platformFamily": "unix",
				"platformOs":     "macos",
			},
		})
	case "thread/list":
		if p.runner.noListResponse {
			return nil
		}
		if p.runner.failList {
			return p.writeJSON(map[string]any{
				"id":    id,
				"error": map[string]any{"code": -32000, "message": "history unavailable"},
			})
		}
		if p.runner.malformedList {
			return p.writeJSON(map[string]any{
				"id":     id,
				"result": map[string]any{"nextCursor": nil},
			})
		}
		return p.writeJSON(map[string]any{
			"id":     id,
			"result": fakeThreadListResult(p.runner.firstPreview),
		})
	default:
		return nil
	}
}

func (p *fakeProcess) writeJSON(value any) error {
	bytes, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(p.stdoutW, "%s\n", bytes)
	return err
}

type fakeStdin struct {
	mu      sync.Mutex
	proc    *fakeProcess
	pending string
}

func (s *fakeStdin) Write(input []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	written := len(input)
	s.pending += string(input)
	parts := strings.Split(s.pending, "\n")
	lines := parts
	if strings.HasSuffix(s.pending, "\n") {
		s.pending = ""
		lines = parts[:len(parts)-1]
	} else {
		s.pending = parts[len(parts)-1]
		lines = parts[:len(parts)-1]
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := s.proc.handleLine(line); err != nil {
			return written, err
		}
	}
	return written, nil
}

func fakeThreadListResult(firstPreview string) map[string]any {
	if firstPreview == "" {
		firstPreview = "new thread prompt"
	}
	return map[string]any{
		"data": []map[string]any{
			{
				"id":            "thread-new",
				"sessionId":     "session-new",
				"preview":       firstPreview,
				"createdAt":     1700000000,
				"updatedAt":     1700000050,
				"cwd":           "/repo",
				"source":        "exec",
				"name":          "New work",
				"status":        map[string]any{"type": "notLoaded"},
				"modelProvider": "openai",
				"turns":         []any{},
			},
			{
				"id":      "skip-missing-cwd",
				"preview": "invalid",
			},
			{
				"id":        "thread-old",
				"sessionId": "session-old",
				"preview":   "",
				"createdAt": 1699999000,
				"updatedAt": 1699999500,
				"cwd":       "/repo",
				"source":    "cli",
				"name":      nil,
				"turns":     []any{},
			},
		},
		"nextCursor":      nil,
		"backwardsCursor": nil,
	}
}

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}
