package runflow

import (
	"context"
	"errors"
	"time"

	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/runexecutor"
	appsession "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/session"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/workspace"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/access"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/capability"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/profile"
	"github.com/zarazhangrui/lark-coding-agent-bridge/internal/domain/runpolicy"
	agentport "github.com/zarazhangrui/lark-coding-agent-bridge/internal/ports/agent"
)

type WorkspaceStore interface {
	CWDFor(scopeID string) string
}

type Executor interface {
	Submit(ctx context.Context, input runexecutor.SubmitRunInput) (*runexecutor.RunExecution, error)
}

type Observability struct {
	Profile string
	Agent   string
	Source  string
	Stage   string
}

type StartInput struct {
	ScopeID        string
	Scope          runpolicy.ScopeContext
	Prompt         string
	Attachments    []runpolicy.AgentAttachment
	Access         access.Decision
	Capability     capability.Capability
	ProfileConfig  profile.Config
	Sessions       *appsession.Store
	SessionCatalog *appsession.Catalog
	Workspaces     WorkspaceStore
	Executor       Executor
	Now            time.Time
	StopGraceMs    int
	Model          string
	Nowait         bool
	Observability  *Observability
}

type RejectReason struct {
	Code        string `json:"code"`
	UserVisible string `json:"userVisible"`
}

type StartResult struct {
	OK           bool
	Execution    *runexecutor.RunExecution
	Policy       runpolicy.Allow
	CWDRealpath  string
	ResumeFrom   string
	RejectReason RejectReason
	Workspace    workspace.ResolveResult
}

type RecordSessionEventInput struct {
	ScopeID        string
	Sessions       *appsession.Store
	SessionCatalog *appsession.Catalog
	Capability     capability.Capability
	Policy         runpolicy.Allow
	Event          agentport.AgentEvent
	Now            time.Time
}

func Start(ctx context.Context, input StartInput) (StartResult, error) {
	if input.Executor == nil {
		return StartResult{}, errors.New("runflow executor is required")
	}

	requestedCWD := ""
	if input.Workspaces != nil {
		requestedCWD = input.Workspaces.CWDFor(input.ScopeID)
	}
	if requestedCWD == "" {
		requestedCWD = input.ProfileConfig.Workspaces.Default
	}
	workspaceResult := workspace.ResolveWorkingDirectory(requestedCWD)
	if !workspaceResult.OK {
		return StartResult{
			OK: false,
			RejectReason: RejectReason{
				Code:        string(workspaceResult.Reason),
				UserVisible: workspaceResult.UserVisible,
			},
			Workspace: workspaceResult,
		}, nil
	}

	policyResult, err := runpolicy.Evaluate(runpolicy.Input{
		Scope:         input.Scope,
		Attachments:   input.Attachments,
		Prompt:        input.Prompt,
		RequestedCWD:  requestedCWD,
		CWDRealpath:   workspaceResult.CWDRealpath,
		Access:        input.Access,
		Capability:    input.Capability,
		ProfileConfig: input.ProfileConfig,
		Now:           input.Now,
	})
	if err != nil {
		return StartResult{}, err
	}
	if !policyResult.OK {
		return StartResult{
			OK: false,
			RejectReason: RejectReason{
				Code:        string(policyResult.RejectReason.Code),
				UserVisible: policyResult.RejectReason.UserVisible,
			},
			Workspace: workspaceResult,
		}, nil
	}
	policy := policyResult.Allow

	sessionID, threadID, resumeFrom := resolveResume(input, policy)

	execution, err := input.Executor.Submit(ctx, runexecutor.SubmitRunInput{
		ScopeID:       input.ScopeID,
		Policy:        toExecutorPolicy(policy),
		SessionID:     sessionID,
		ThreadID:      threadID,
		Model:         input.Model,
		Images:        codexImages(input.Capability, policy.Attachments),
		StopGraceMs:   input.StopGraceMs,
		Nowait:        input.Nowait,
		Observability: toExecutorObservability(input),
	})
	if err != nil {
		var rejected *runexecutor.RunRejected
		if errors.As(err, &rejected) {
			return StartResult{
				OK: false,
				RejectReason: RejectReason{
					Code:        string(rejected.Code),
					UserVisible: runRejectedUserVisible(rejected.Code),
				},
				Workspace: workspaceResult,
			}, nil
		}
		return StartResult{}, err
	}

	return StartResult{
		OK:          true,
		Execution:   execution,
		Policy:      policy,
		CWDRealpath: workspaceResult.CWDRealpath,
		ResumeFrom:  resumeFrom,
		Workspace:   workspaceResult,
	}, nil
}

func toExecutorObservability(input StartInput) runexecutor.Observability {
	out := runexecutor.Observability{
		Agent:  string(input.Capability.AgentID),
		Source: string(input.Scope.Source),
		Stage:  "submit",
	}
	if input.Observability != nil {
		if input.Observability.Profile != "" {
			out.Profile = input.Observability.Profile
		}
		if input.Observability.Agent != "" {
			out.Agent = input.Observability.Agent
		}
		if input.Observability.Source != "" {
			out.Source = input.Observability.Source
		}
		if input.Observability.Stage != "" {
			out.Stage = input.Observability.Stage
		}
	}
	return out
}

func RecordSessionEvent(input RecordSessionEventInput) error {
	if input.Event.Type != agentport.EventSystem {
		return nil
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	if input.Capability.AgentID == capability.IDClaude && input.Event.SessionID != nil && *input.Event.SessionID != "" {
		cwdRealpath := input.Policy.CWDRealpath
		if input.Event.CWD != nil && *input.Event.CWD != "" {
			cwdRealpath = *input.Event.CWD
		}
		if input.Sessions != nil {
			input.Sessions.Set(input.ScopeID, *input.Event.SessionID, cwdRealpath)
		}
		if input.SessionCatalog != nil {
			_, err := input.SessionCatalog.UpsertActive(appsession.UpsertCatalogInput{
				CatalogIdentity: appsession.CatalogIdentity{
					ScopeID:           input.ScopeID,
					AgentID:           capability.IDClaude,
					CWDRealpath:       cwdRealpath,
					PolicyFingerprint: input.Policy.PolicyFingerprint,
				},
				Now:       now,
				SessionID: *input.Event.SessionID,
			})
			return err
		}
		return nil
	}
	if input.Capability.AgentID == capability.IDCodex && input.Event.ThreadID != nil && *input.Event.ThreadID != "" && input.SessionCatalog != nil {
		_, err := input.SessionCatalog.UpsertActive(appsession.UpsertCatalogInput{
			CatalogIdentity: appsession.CatalogIdentity{
				ScopeID:           input.ScopeID,
				AgentID:           capability.IDCodex,
				CWDRealpath:       input.Policy.CWDRealpath,
				PolicyFingerprint: input.Policy.PolicyFingerprint,
			},
			Now:      now,
			ThreadID: *input.Event.ThreadID,
		})
		return err
	}
	return nil
}

func resolveResume(input StartInput, policy runpolicy.Allow) (sessionID string, threadID string, resumeFrom string) {
	if input.SessionCatalog != nil {
		entry, ok := input.SessionCatalog.ActiveFor(appsession.CatalogIdentity{
			ScopeID:           input.ScopeID,
			AgentID:           input.Capability.AgentID,
			CWDRealpath:       policy.CWDRealpath,
			PolicyFingerprint: policy.PolicyFingerprint,
		})
		if ok && entry.AgentID == capability.IDClaude {
			return entry.SessionID, "", entry.SessionID
		}
		if ok && entry.AgentID == capability.IDCodex {
			return "", entry.ThreadID, entry.ThreadID
		}
	}
	if input.Capability.AgentID == capability.IDClaude && input.Sessions != nil {
		if resume := input.Sessions.ResumeFor(input.ScopeID, policy.CWDRealpath); resume != "" {
			return resume, "", resume
		}
		if stale, ok := input.Sessions.GetRaw(input.ScopeID); ok && stale.CWD != "" && stale.CWD != policy.CWDRealpath {
			input.Sessions.Clear(input.ScopeID)
		}
	}
	return "", "", ""
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

func codexImages(cap capability.Capability, attachments []runpolicy.AgentAttachment) []string {
	if cap.AgentID != capability.IDCodex {
		return nil
	}
	var images []string
	for _, attachment := range attachments {
		if attachment.Kind == "image" && attachment.Decision == runpolicy.AttachmentAccepted && attachment.Path != "" {
			images = append(images, attachment.Path)
		}
	}
	return images
}

func runRejectedUserVisible(code runexecutor.RunRejectedCode) string {
	switch code {
	case runexecutor.RunRejectedReconnectInProgress:
		return "当前 bot 正在重连，稍后会继续处理新消息。"
	case runexecutor.RunRejectedAlreadyActive:
		return "当前会话已有运行在执行，请稍后再试或先停止当前运行。"
	default:
		return "当前无法发起运行，请稍后重试。"
	}
}
