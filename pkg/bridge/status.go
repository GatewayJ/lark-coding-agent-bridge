package bridge

import "time"

type BridgeMode string

const (
	BridgeModeAgent   BridgeMode = "agent"
	BridgeModeLark    BridgeMode = "lark"
	BridgeModeRuntime BridgeMode = "runtime"
)

type BridgeStatus struct {
	Started               bool       `json:"started"`
	Mode                  BridgeMode `json:"mode,omitempty"`
	Home                  string     `json:"home,omitempty"`
	Profile               string     `json:"profile,omitempty"`
	Version               string     `json:"version,omitempty"`
	StartedAt             time.Time  `json:"startedAt,omitempty"`
	AgentClientConfigured bool       `json:"agentClientConfigured"`
	RuntimeConfigured     bool       `json:"runtimeConfigured"`
}

type LarkStatus struct {
	Configured              bool   `json:"configured"`
	Started                 bool   `json:"started"`
	BotOpenID               string `json:"botOpenId,omitempty"`
	BotUserID               string `json:"botUserId,omitempty"`
	BotUnionID              string `json:"botUnionId,omitempty"`
	BotName                 string `json:"botName,omitempty"`
	LarkCliSourceConfigFile string `json:"larkCliSourceConfigFile,omitempty"`
	IdentityPolicyApplied   bool   `json:"identityPolicyApplied,omitempty"`
}
