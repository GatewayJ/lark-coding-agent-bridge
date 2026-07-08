package comments

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/intake"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runexecutor"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runflow"
	appsession "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/session"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/access"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/capability"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/runpolicy"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

const ReplyMaxChars = 2000

var ErrNoAccess = errors.New("comment no access")

type TargetResolver interface {
	ResolveCommentTarget(ctx context.Context, fileToken string, fileType string) (Target, bool, error)
}

type Fetcher interface {
	FetchComment(ctx context.Context, target Target, commentID string) (Thread, error)
}

type Replier interface {
	ReplyToComment(ctx context.Context, target Target, commentID string, text string, opts ReplyOptions) error
}

type Reactor interface {
	AddReaction(ctx context.Context, target Target, replyID string) (bool, error)
	RemoveReaction(ctx context.Context, target Target, replyID string) error
}

type WorkspaceStore interface {
	CWDFor(scopeID string) string
}

type Executor interface {
	Submit(ctx context.Context, input runexecutor.SubmitRunInput) (RunExecution, error)
}

type RunExecution interface {
	Subscribe(ctx context.Context) <-chan agentport.AgentEvent
	Stop(ctx context.Context) error
}

type RunExecutorAdapter struct {
	Executor interface {
		Submit(ctx context.Context, input runexecutor.SubmitRunInput) (*runexecutor.RunExecution, error)
	}
}

func (a RunExecutorAdapter) Submit(ctx context.Context, input runexecutor.SubmitRunInput) (RunExecution, error) {
	if a.Executor == nil {
		return nil, errors.New("comment run executor is required")
	}
	return a.Executor.Submit(ctx, input)
}

type ReplyOptions struct {
	TopLevel bool
}

type Options struct {
	Resolver TargetResolver
	Fetcher  Fetcher
	Replier  Replier
	Reactor  Reactor
	Executor Executor

	Sessions       *appsession.Store
	SessionCatalog *appsession.Catalog
	Workspaces     WorkspaceStore
	ProfileConfig  profile.Config
	Capability     capability.Capability

	ManagedDefaultWorkspace string
	StopGraceMs             int
	Model                   string
	Nowait                  bool

	Now                 func() time.Time
	NewExecutionScopeID func(commentScopeKey string) string
}

type Service struct {
	opts Options

	activeMu      sync.Mutex
	activeDocRuns map[string]int
}

type ResultStatus string

const (
	ResultSkipped ResultStatus = "skipped"
	ResultReplied ResultStatus = "replied"
)

type Result struct {
	Status           ResultStatus
	Reason           string
	CommentScopeKey  string
	DocumentScopeKey string
	ExecutionScopeID string
	SessionScopeID   string
	Target           Target
	Prompt           string
	Reply            string
	Workspace        WorkspaceResult
}

func NewService(opts Options) *Service {
	return &Service{
		opts:          opts,
		activeDocRuns: map[string]int{},
	}
}

func (s *Service) Handle(ctx context.Context, input intake.CommentInput) (Result, error) {
	if s == nil {
		return Result{}, errors.New("comment service is nil")
	}
	eventDocScopeKey := DocumentScopeKey(input.FileToken)
	commentThreadScopeKey := ScopeKey(input.FileToken, input.CommentID)
	base := Result{
		CommentScopeKey:  commentThreadScopeKey,
		DocumentScopeKey: eventDocScopeKey,
	}

	if !input.MentionedBot {
		return base.skip("not-mentioned"), nil
	}
	if isBridgeSelfReply(input) {
		return base.skip("bridge-self-reply"), nil
	}
	if !isSupportedFileType(input.FileType) {
		return base.skip("unsupported-fileType"), nil
	}
	if s.opts.Resolver == nil {
		return base, errors.New("comment target resolver is required")
	}
	target, ok, err := s.opts.Resolver.ResolveCommentTarget(ctx, input.FileToken, input.FileType)
	if err != nil {
		return base, err
	}
	if !ok {
		return base.skip("unsupported-target"), nil
	}
	base.Target = target
	base.DocumentScopeKey = DocumentScopeKey(target.FileToken)

	if s.opts.Fetcher == nil {
		return base, errors.New("comment fetcher is required")
	}
	thread, err := s.opts.Fetcher.FetchComment(ctx, target, input.CommentID)
	if err != nil {
		if errors.Is(err, ErrNoAccess) {
			return base.skip("no-access"), nil
		}
		return base, err
	}
	parsed, ok := ExtractCommentQuestionFromReplies(ExtractQuestionInput{
		ReplyID: input.ReplyID,
		Replies: thread.Replies,
	})
	if !ok || parsed.Question == "" {
		return base.skip("empty-question"), nil
	}
	commentCtx := CommentContext{
		Question:      parsed.Question,
		Quote:         thread.Quote,
		IsWhole:       thread.IsWhole,
		TargetReplyID: parsed.TargetReplyID,
	}
	prompt := BuildPrompt(target, commentCtx)
	base.Prompt = prompt

	docSessionScopeKey := DocumentSessionScopeKey(target.FileToken)
	legacyDocSessionScopeKey := LegacyDocumentSessionScopeKey(target.FileToken)
	base.SessionScopeID = docSessionScopeKey
	executionScopeID := s.executionScopeID(commentThreadScopeKey)
	base.ExecutionScopeID = executionScopeID

	workspaceResult := resolveCommentWorkingDirectory(
		cwdForFirst(s.opts.Workspaces, docSessionScopeKey, legacyDocSessionScopeKey),
		s.opts.ProfileConfig.Workspaces.Default,
		s.opts.ManagedDefaultWorkspace,
	)
	base.Workspace = workspaceResult
	if !workspaceResult.OK {
		reply := "工作目录不可用：" + workspaceResult.UserVisible
		if err := s.reply(ctx, target, input.CommentID, reply, commentCtx.IsWhole); err != nil {
			return base, err
		}
		base.Status = ResultReplied
		base.Reason = "workspace-rejected"
		base.Reply = reply
		return base, nil
	}

	reactionAdded := false
	if s.opts.Reactor != nil && commentCtx.TargetReplyID != "" {
		added, err := s.opts.Reactor.AddReaction(ctx, target, commentCtx.TargetReplyID)
		if err == nil {
			reactionAdded = added
		}
	}
	defer func() {
		if reactionAdded && s.opts.Reactor != nil {
			_ = s.opts.Reactor.RemoveReaction(context.Background(), target, commentCtx.TargetReplyID)
		}
	}()

	policy, watchdogEnabled, err := s.evaluatePolicy(input, target, prompt, workspaceResult, executionScopeID, commentThreadScopeKey, docSessionScopeKey)
	if err != nil {
		return base, err
	}
	if !policy.OK {
		base.Status = ResultSkipped
		base.Reason = string(policy.RejectReason.Code)
		return base, nil
	}

	agentSessionRun := s.markDocRun(docSessionScopeKey)
	defer agentSessionRun.release()
	sessionID, threadID := s.resolveResume(agentSessionRun.wasActive, docSessionScopeKey, legacyDocSessionScopeKey, policy.Allow)
	execution, err := s.submitRun(ctx, executionScopeID, policy.Allow, sessionID, threadID)
	if err != nil {
		var rejected *runexecutor.RunRejected
		if errors.As(err, &rejected) {
			reply := commentRunRejectedReply(rejected.Code)
			if reply == "" {
				base.Status = ResultSkipped
				base.Reason = string(rejected.Code)
				return base, nil
			}
			if replyErr := s.reply(ctx, target, input.CommentID, reply, commentCtx.IsWhole); replyErr != nil {
				return base, replyErr
			}
			base.Status = ResultReplied
			base.Reason = string(rejected.Code)
			base.Reply = reply
			return base, nil
		}
		return base, err
	}

	reply, reason, err := s.collectReply(ctx, execution, docSessionScopeKey, policy.Allow, watchdogEnabled)
	if err != nil {
		return base, err
	}
	if reason == "interrupted" {
		base.Status = ResultSkipped
		base.Reason = reason
		return base, nil
	}
	if err := s.reply(ctx, target, input.CommentID, reply, commentCtx.IsWhole); err != nil {
		return base, err
	}
	base.Status = ResultReplied
	base.Reason = reason
	base.Reply = reply
	return base, nil
}

func (s *Service) evaluatePolicy(input intake.CommentInput, target Target, prompt string, workspaceResult WorkspaceResult, executionScopeID string, commentThreadScopeKey string, docSessionScopeKey string) (runpolicy.Result, bool, error) {
	timeout, watchdogEnabled := commentRunTimeout(s.opts.Sessions, executionScopeID, commentThreadScopeKey)
	now := s.now()
	result, err := runpolicy.Evaluate(runpolicy.Input{
		Scope: runpolicy.ScopeContext{
			Source:         runpolicy.SourceComment,
			ActorID:        input.Operator.OpenID,
			CommentScopeID: docSessionScopeKey,
			ResourceBindings: []runpolicy.ResourceBinding{{
				Kind:     runpolicy.ResourceBindingDoc,
				ID:       DocumentScopeKey(target.FileToken),
				Verified: true,
			}},
		},
		Attachments:   nil,
		Prompt:        prompt,
		RequestedCWD:  workspaceResult.RequestedCWD,
		CWDRealpath:   workspaceResult.CWDRealpath,
		Access:        access.Decision{OK: true, Reason: access.ReasonCommentMention},
		Capability:    s.opts.Capability,
		ProfileConfig: s.opts.ProfileConfig,
		Now:           now,
		TTL:           timeout,
	})
	return result, watchdogEnabled, err
}

func (s *Service) submitRun(ctx context.Context, executionScopeID string, policy runpolicy.Allow, sessionID string, threadID string) (RunExecution, error) {
	if s.opts.Executor == nil {
		return nil, errors.New("comment run executor is required")
	}
	return s.opts.Executor.Submit(ctx, runexecutor.SubmitRunInput{
		ScopeID:     executionScopeID,
		Policy:      toExecutorPolicy(policy),
		SessionID:   sessionID,
		ThreadID:    threadID,
		Model:       s.opts.Model,
		StopGraceMs: s.opts.StopGraceMs,
		Nowait:      s.opts.Nowait,
		Observability: runexecutor.Observability{
			Agent:  string(s.opts.Capability.AgentID),
			Source: "comment",
			Stage:  "submit",
		},
	})
}

func (s *Service) collectReply(ctx context.Context, execution RunExecution, sessionScopeID string, policy runpolicy.Allow, watchdogEnabled bool) (string, string, error) {
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := execution.Subscribe(subCtx)

	var timeout <-chan time.Time
	if watchdogEnabled {
		delay := time.Until(policy.ExpiresAt)
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		timeout = timer.C
	}

	var answer strings.Builder
	var errorMessage string
	for {
		select {
		case <-ctx.Done():
			_ = execution.Stop(context.Background())
			return "", "", ctx.Err()
		case <-timeout:
			_ = execution.Stop(context.Background())
			return "本次评论任务已超时，请重新 @ 我。", "policy-expired", nil
		case event, ok := <-events:
			if !ok {
				return finalizeReply(answer.String(), errorMessage), "completed", nil
			}
			_ = runflow.RecordSessionEvent(runflow.RecordSessionEventInput{
				ScopeID:        sessionScopeID,
				Sessions:       s.opts.Sessions,
				SessionCatalog: s.opts.SessionCatalog,
				Capability:     s.opts.Capability,
				Policy:         policy,
				Event:          event,
				Now:            s.now(),
			})
			switch event.Type {
			case agentport.EventText:
				if event.Delta != nil {
					answer.WriteString(*event.Delta)
				}
			case agentport.EventToolUse, agentport.EventToolResult:
				answer.Reset()
			case agentport.EventError:
				if event.Message != nil {
					errorMessage = *event.Message
				}
				return finalizeReply(answer.String(), errorMessage), "completed", nil
			case agentport.EventDone:
				if event.TerminationReason == agentport.TerminationInterrupted {
					return "", "interrupted", nil
				}
				return finalizeReply(answer.String(), errorMessage), "completed", nil
			}
		}
	}
}

func (s *Service) resolveResume(wasActive bool, docSessionScopeKey string, legacyDocSessionScopeKey string, policy runpolicy.Allow) (string, string) {
	if wasActive {
		return "", ""
	}
	var catalogEntry appsession.CatalogEntry
	var hasCatalogEntry bool
	if s.opts.SessionCatalog != nil {
		catalogEntry, hasCatalogEntry = s.opts.SessionCatalog.ActiveFor(appsession.CatalogIdentity{
			ScopeID:           docSessionScopeKey,
			AgentID:           s.opts.Capability.AgentID,
			CWDRealpath:       policy.CWDRealpath,
			PolicyFingerprint: policy.PolicyFingerprint,
		})
		if !hasCatalogEntry {
			catalogEntry, hasCatalogEntry = s.opts.SessionCatalog.ActiveFor(appsession.CatalogIdentity{
				ScopeID:           legacyDocSessionScopeKey,
				AgentID:           s.opts.Capability.AgentID,
				CWDRealpath:       policy.CWDRealpath,
				PolicyFingerprint: policy.PolicyFingerprint,
			})
		}
	}
	if s.opts.Capability.AgentID == capability.IDCodex && hasCatalogEntry {
		return "", catalogEntry.ThreadID
	}
	if s.opts.Capability.AgentID == capability.IDClaude && s.opts.Sessions != nil {
		if sessionID := s.opts.Sessions.ResumeFor(docSessionScopeKey, policy.CWDRealpath); sessionID != "" {
			return sessionID, ""
		}
		if sessionID := s.opts.Sessions.ResumeFor(legacyDocSessionScopeKey, policy.CWDRealpath); sessionID != "" {
			return sessionID, ""
		}
	}
	return "", ""
}

type activeDocRun struct {
	wasActive bool
	release   func()
}

func (s *Service) markDocRun(scopeID string) activeDocRun {
	s.activeMu.Lock()
	count := s.activeDocRuns[scopeID]
	s.activeDocRuns[scopeID] = count + 1
	s.activeMu.Unlock()

	var once sync.Once
	return activeDocRun{
		wasActive: count > 0,
		release: func() {
			once.Do(func() {
				s.activeMu.Lock()
				defer s.activeMu.Unlock()
				next := s.activeDocRuns[scopeID] - 1
				if next > 0 {
					s.activeDocRuns[scopeID] = next
				} else {
					delete(s.activeDocRuns, scopeID)
				}
			})
		},
	}
}

func (s *Service) reply(ctx context.Context, target Target, commentID string, text string, isWhole bool) error {
	if s.opts.Replier == nil {
		return errors.New("comment replier is required")
	}
	return s.opts.Replier.ReplyToComment(ctx, target, commentID, text, ReplyOptions{TopLevel: isWhole})
}

func (s *Service) executionScopeID(commentScopeKey string) string {
	if s.opts.NewExecutionScopeID != nil {
		return s.opts.NewExecutionScopeID(commentScopeKey)
	}
	return commentScopeKey + ":" + randomHex(6)
}

func (s *Service) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

func (r Result) skip(reason string) Result {
	r.Status = ResultSkipped
	r.Reason = reason
	return r
}

func commentRunTimeout(sessions *appsession.Store, executionScopeID string, commentThreadScopeKey string) (time.Duration, bool) {
	if sessions == nil {
		return 0, false
	}
	if minutes, ok := sessions.GetIdleTimeoutMinutes(executionScopeID); ok {
		if minutes > 0 {
			return time.Duration(minutes) * time.Minute, true
		}
		return 0, false
	}
	if minutes, ok := sessions.GetIdleTimeoutMinutes(commentThreadScopeKey); ok {
		if minutes > 0 {
			return time.Duration(minutes) * time.Minute, true
		}
		return 0, false
	}
	return 0, false
}

func finalizeReply(answer string, errorMessage string) string {
	reply := StripMarkdown(strings.TrimSpace(answer))
	if errorMessage != "" {
		reply = "⚠️ Claude 报错：" + errorMessage
	}
	if reply == "" {
		reply = "（无回复内容）"
	}
	return truncateReply(reply, ReplyMaxChars)
}

func truncateReply(reply string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	runes := []rune(reply)
	if len(runes) <= maxChars {
		return reply
	}
	return string(runes[:maxChars-1]) + "…"
}

func commentRunRejectedReply(code runexecutor.RunRejectedCode) string {
	switch code {
	case runexecutor.RunRejectedAlreadyActive:
		return "当前评论线程已有任务在执行，请稍后再试。"
	case runexecutor.RunRejectedPoolFull:
		return "当前任务较多，请稍后再试。"
	case runexecutor.RunRejectedReconnectInProgress:
		return "当前 bot 正在重连，请稍后再试。"
	case runexecutor.RunRejectedPolicyExpired:
		return "本次评论任务已超时，请重新 @ 我。"
	default:
		return ""
	}
}

func toExecutorPolicy(policy runpolicy.Allow) runexecutor.RunPolicy {
	return runexecutor.RunPolicy{
		Prompt:         policy.Prompt,
		CWDRealpath:    policy.CWDRealpath,
		AccessMode:     policy.AccessMode,
		Sandbox:        policy.Sandbox,
		PermissionMode: policy.PermissionMode,
		ExpiresAt:      policy.ExpiresAt,
	}
}

func cwdForFirst(workspaces WorkspaceStore, scopeIDs ...string) string {
	if workspaces == nil {
		return ""
	}
	for _, scopeID := range scopeIDs {
		if cwd := workspaces.CWDFor(scopeID); cwd != "" {
			return cwd
		}
	}
	return ""
}

func isSupportedFileType(fileType string) bool {
	switch fileType {
	case "doc", "docx", "sheet", "file":
		return true
	default:
		return false
	}
}

func isBridgeSelfReply(input intake.CommentInput) bool {
	if input.BridgeReply {
		return true
	}
	return metadataMarksBridge(input.Metadata)
}

func metadataMarksBridge(metadata map[string]any) bool {
	if len(metadata) == 0 {
		return false
	}
	if metadataBool(metadata, "bridge") || metadataBool(metadata, "bridgeReply") || metadataBool(metadata, "bridge_reply") {
		return true
	}
	if source, _ := metadata["source"].(string); source == "lark-channel-bridge" {
		return true
	}
	for _, key := range []string{"replyMetadata", "reply_metadata", "metadata"} {
		nested, ok := metadata[key].(map[string]any)
		if ok && metadataMarksBridge(nested) {
			return true
		}
	}
	return false
}

func metadataBool(metadata map[string]any, key string) bool {
	value, ok := metadata[key].(bool)
	return ok && value
}

func TokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:16]
}

func DocumentScopeKey(fileToken string) string {
	return intake.CommentDocumentScopeKey(fileToken)
}

func ScopeKey(fileToken string, commentID string) string {
	return intake.CommentScopeKey(fileToken, commentID)
}

func DocumentSessionScopeKey(fileToken string) string {
	return "doc:" + TokenDigest(fileToken)
}

func LegacyDocumentSessionScopeKey(fileToken string) string {
	return "doc:" + fileToken
}

func randomHex(bytes int) string {
	if bytes <= 0 {
		bytes = 6
	}
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format("150405.000000000")))[:bytes*2]
	}
	return hex.EncodeToString(buf)
}
