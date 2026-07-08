package comments

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/intake"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runexecutor"
	appsession "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/session"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/capability"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

func TestHandleSkipsNotMentionedWithoutResolvingTarget(t *testing.T) {
	ports := newFakeCommentPorts()
	executor := &fakeCommentExecutor{}
	service := newTestService(t, ports, executor, nil)
	input := testCommentInput()
	input.MentionedBot = false

	result, err := service.Handle(context.Background(), input)
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if result.Status != ResultSkipped || result.Reason != "not-mentioned" {
		t.Fatalf("result = %#v, want not-mentioned skip", result)
	}
	if ports.resolveCalls != 0 || executor.calls != 0 {
		t.Fatalf("not-mentioned event touched resolver/executor: resolves=%d runs=%d", ports.resolveCalls, executor.calls)
	}
}

func TestHandleSkipsUnsupportedTarget(t *testing.T) {
	ports := newFakeCommentPorts()
	ports.targetOK = false
	executor := &fakeCommentExecutor{}
	service := newTestService(t, ports, executor, nil)

	result, err := service.Handle(context.Background(), testCommentInput())
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if result.Status != ResultSkipped || result.Reason != "unsupported-target" {
		t.Fatalf("result = %#v, want unsupported-target skip", result)
	}
	if ports.fetchCalls != 0 || len(ports.replies) != 0 || executor.calls != 0 {
		t.Fatalf("unsupported target continued unexpectedly: fetch=%d replies=%d runs=%d", ports.fetchCalls, len(ports.replies), executor.calls)
	}
}

func TestHandleSkipsEmptyQuestion(t *testing.T) {
	ports := newFakeCommentPorts()
	ports.thread = Thread{
		Replies: []Reply{{
			ReplyID: "reply-1",
			Content: ReplyContent{Elements: []ReplyElement{{
				Type:   "person",
				Person: &ReplyPerson{UserID: "ou_user"},
			}}},
		}},
	}
	executor := &fakeCommentExecutor{}
	service := newTestService(t, ports, executor, nil)

	result, err := service.Handle(context.Background(), testCommentInput())
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if result.Status != ResultSkipped || result.Reason != "empty-question" {
		t.Fatalf("result = %#v, want empty-question skip", result)
	}
	if len(ports.replies) != 0 || executor.calls != 0 {
		t.Fatalf("empty question replied or ran: replies=%d runs=%d", len(ports.replies), executor.calls)
	}
}

func TestHandleSkipsNoAccessFetchError(t *testing.T) {
	ports := newFakeCommentPorts()
	ports.fetchErr = ErrNoAccess
	executor := &fakeCommentExecutor{}
	service := newTestService(t, ports, executor, nil)

	result, err := service.Handle(context.Background(), testCommentInput())
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if result.Status != ResultSkipped || result.Reason != "no-access" {
		t.Fatalf("result = %#v, want no-access skip", result)
	}
	if len(ports.replies) != 0 || executor.calls != 0 {
		t.Fatalf("no-access fetch replied or ran: replies=%d runs=%d", len(ports.replies), executor.calls)
	}
}

func TestHandleRepliesWhenWorkspaceInvalid(t *testing.T) {
	badPath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(badPath, []byte("file"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	ports := newFakeCommentPorts()
	executor := &fakeCommentExecutor{}
	service := newTestService(t, ports, executor, func(opts *Options) {
		opts.ProfileConfig.Workspaces.Default = badPath
		opts.ManagedDefaultWorkspace = badPath
	})

	result, err := service.Handle(context.Background(), testCommentInput())
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if result.Status != ResultReplied || result.Reason != "workspace-rejected" {
		t.Fatalf("result = %#v, want workspace rejection reply", result)
	}
	if len(ports.replies) != 1 || !strings.Contains(ports.replies[0].text, "工作目录不可用") {
		t.Fatalf("workspace reply = %#v", ports.replies)
	}
	if executor.calls != 0 {
		t.Fatalf("invalid workspace still submitted %d runs", executor.calls)
	}
}

func TestHandleSuccessfulRunRepliesWithPlainText(t *testing.T) {
	ports := newFakeCommentPorts()
	ports.target = Target{FileToken: "resolved-token", FileType: "docx"}
	executor := &fakeCommentExecutor{
		execution: newFakeCommentExecution(
			textEvent("**完成**"),
			doneEvent(),
		),
	}
	service := newTestService(t, ports, executor, nil)

	result, err := service.Handle(context.Background(), testCommentInput())
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if result.Status != ResultReplied || result.Reason != "completed" {
		t.Fatalf("result = %#v, want completed reply", result)
	}
	if len(ports.replies) != 1 || ports.replies[0].text != "完成" {
		t.Fatalf("reply = %#v, want stripped plain text", ports.replies)
	}
	if executor.calls != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.calls)
	}
	if !strings.Contains(executor.last.Policy.Prompt, "https://feishu.cn/docx/resolved-token") ||
		!strings.Contains(executor.last.Policy.Prompt, "用户的问题：请总结这段") ||
		!strings.Contains(executor.last.Policy.Prompt, "不要调用云文档评论或回复接口") {
		t.Fatalf("prompt missing comment context:\n%s", executor.last.Policy.Prompt)
	}
	if executor.last.ScopeID != result.ExecutionScopeID || result.SessionScopeID != DocumentSessionScopeKey("resolved-token") {
		t.Fatalf("scope mismatch: submit=%q result=%#v", executor.last.ScopeID, result)
	}
}

func TestHandleCleansUpReactionAfterRun(t *testing.T) {
	ports := newFakeCommentPorts()
	ports.addReaction = true
	executor := &fakeCommentExecutor{
		execution: newFakeCommentExecution(
			textEvent("ok"),
			doneEvent(),
		),
	}
	service := newTestService(t, ports, executor, nil)

	result, err := service.Handle(context.Background(), testCommentInput())
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if result.Status != ResultReplied {
		t.Fatalf("result = %#v, want reply", result)
	}
	if ports.addCalls != 1 || ports.removeCalls != 1 {
		t.Fatalf("reaction calls add=%d remove=%d", ports.addCalls, ports.removeCalls)
	}
	if ports.removedReplyIDs[0] != "reply-1" {
		t.Fatalf("removed reply ids = %#v", ports.removedReplyIDs)
	}
}

func TestHandleReactionAddFailureDoesNotBlockRun(t *testing.T) {
	ports := newFakeCommentPorts()
	ports.addErr = errors.New("reaction unavailable")
	executor := &fakeCommentExecutor{
		execution: newFakeCommentExecution(
			textEvent("ok"),
			doneEvent(),
		),
	}
	service := newTestService(t, ports, executor, nil)

	result, err := service.Handle(context.Background(), testCommentInput())
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if result.Status != ResultReplied || result.Reason != "completed" {
		t.Fatalf("result = %#v, want completed reply", result)
	}
	if ports.addCalls != 1 || ports.removeCalls != 0 || executor.calls != 1 {
		t.Fatalf("reaction/run calls add=%d remove=%d run=%d", ports.addCalls, ports.removeCalls, executor.calls)
	}
}

func TestHandleTimeoutStopsRunAndReplies(t *testing.T) {
	ports := newFakeCommentPorts()
	execution := &fakeCommentExecution{block: true}
	executor := &fakeCommentExecutor{execution: execution}
	service := newTestService(t, ports, executor, func(opts *Options) {
		opts.Sessions.SetIdleTimeoutMinutes(ScopeKey("doc-token", "comment-1"), 1)
		opts.Now = func() time.Time { return time.Now().Add(-2 * time.Minute) }
	})

	result, err := service.Handle(context.Background(), testCommentInput())
	if err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if result.Status != ResultReplied || result.Reason != "policy-expired" || result.Reply != "本次评论任务已超时，请重新 @ 我。" {
		t.Fatalf("result = %#v, want timeout reply", result)
	}
	if execution.stopCalls != 1 {
		t.Fatalf("stop calls = %d, want 1", execution.stopCalls)
	}
}

func newTestService(t *testing.T, ports *fakeCommentPorts, executor *fakeCommentExecutor, configure func(*Options)) *Service {
	t.Helper()
	cfg := profile.DefaultConfig(profile.AgentCodex)
	cfg.Workspaces.Default = t.TempDir()
	opts := Options{
		Resolver:                ports,
		Fetcher:                 ports,
		Replier:                 ports,
		Reactor:                 ports,
		Executor:                executor,
		Sessions:                appsession.NewStore(""),
		SessionCatalog:          appsession.NewCatalog(""),
		ProfileConfig:           cfg,
		Capability:              capability.Codex(cfg.Permissions.MaxAccess, ""),
		ManagedDefaultWorkspace: filepath.Join(t.TempDir(), "managed"),
		NewExecutionScopeID:     func(commentScopeKey string) string { return commentScopeKey + ":run" },
	}
	if configure != nil {
		configure(&opts)
	}
	return NewService(opts)
}

func testCommentInput() intake.CommentInput {
	return intake.CommentInput{
		EventID:      "event-1",
		FileToken:    "doc-token",
		FileType:     "docx",
		CommentID:    "comment-1",
		ReplyID:      "reply-1",
		Operator:     intake.Actor{OpenID: "ou_user"},
		MentionedBot: true,
	}
}

type fakeCommentPorts struct {
	target   Target
	targetOK bool
	thread   Thread
	fetchErr error

	resolveCalls int
	fetchCalls   int

	replies []postedCommentReply

	addReaction     bool
	addErr          error
	addCalls        int
	removeCalls     int
	addedReplyIDs   []string
	removedReplyIDs []string
}

type postedCommentReply struct {
	target    Target
	commentID string
	text      string
	topLevel  bool
}

func newFakeCommentPorts() *fakeCommentPorts {
	return &fakeCommentPorts{
		targetOK: true,
		thread: Thread{
			Replies: []Reply{{
				ReplyID: "reply-1",
				Content: ReplyContent{Elements: []ReplyElement{{
					Type:    "text_run",
					TextRun: &ReplyTextRun{Text: "请总结这段"},
				}}},
			}},
			Quote:   "原文",
			IsWhole: false,
		},
	}
}

func (p *fakeCommentPorts) ResolveCommentTarget(_ context.Context, fileToken string, fileType string) (Target, bool, error) {
	p.resolveCalls++
	if !p.targetOK {
		return Target{}, false, nil
	}
	if p.target.FileToken != "" {
		return p.target, true, nil
	}
	return Target{FileToken: fileToken, FileType: fileType}, true, nil
}

func (p *fakeCommentPorts) FetchComment(_ context.Context, target Target, commentID string) (Thread, error) {
	p.fetchCalls++
	if p.fetchErr != nil {
		return Thread{}, p.fetchErr
	}
	return p.thread, nil
}

func (p *fakeCommentPorts) ReplyToComment(_ context.Context, target Target, commentID string, text string, opts ReplyOptions) error {
	p.replies = append(p.replies, postedCommentReply{
		target:    target,
		commentID: commentID,
		text:      text,
		topLevel:  opts.TopLevel,
	})
	return nil
}

func (p *fakeCommentPorts) AddReaction(_ context.Context, _ Target, replyID string) (bool, error) {
	p.addCalls++
	p.addedReplyIDs = append(p.addedReplyIDs, replyID)
	if p.addErr != nil {
		return false, p.addErr
	}
	return p.addReaction, nil
}

func (p *fakeCommentPorts) RemoveReaction(_ context.Context, _ Target, replyID string) error {
	p.removeCalls++
	p.removedReplyIDs = append(p.removedReplyIDs, replyID)
	return nil
}

type fakeCommentExecutor struct {
	calls     int
	last      runexecutor.SubmitRunInput
	execution RunExecution
	err       error
}

func (e *fakeCommentExecutor) Submit(_ context.Context, input runexecutor.SubmitRunInput) (RunExecution, error) {
	e.calls++
	e.last = input
	if e.err != nil {
		return nil, e.err
	}
	if e.execution != nil {
		return e.execution, nil
	}
	return newFakeCommentExecution(doneEvent()), nil
}

type fakeCommentExecution struct {
	events    []agentport.AgentEvent
	block     bool
	stopCalls int
}

func newFakeCommentExecution(events ...agentport.AgentEvent) *fakeCommentExecution {
	return &fakeCommentExecution{events: events}
}

func (e *fakeCommentExecution) Subscribe(ctx context.Context) <-chan agentport.AgentEvent {
	out := make(chan agentport.AgentEvent, len(e.events))
	go func() {
		defer close(out)
		for _, event := range e.events {
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
		if e.block {
			<-ctx.Done()
		}
	}()
	return out
}

func (e *fakeCommentExecution) Stop(context.Context) error {
	e.stopCalls++
	return nil
}

func textEvent(delta string) agentport.AgentEvent {
	return agentport.AgentEvent{Type: agentport.EventText, Delta: &delta}
}

func doneEvent() agentport.AgentEvent {
	return agentport.AgentEvent{Type: agentport.EventDone, TerminationReason: agentport.TerminationNormal}
}
