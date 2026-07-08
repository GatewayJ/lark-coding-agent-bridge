package permissions

type AccessMode string

const (
	AccessReadOnly  AccessMode = "read-only"
	AccessWorkspace AccessMode = "workspace"
	AccessFull      AccessMode = "full"
)

type CodexSandboxMode string

const (
	CodexSandboxReadOnly         CodexSandboxMode = "read-only"
	CodexSandboxWorkspaceWrite   CodexSandboxMode = "workspace-write"
	CodexSandboxDangerFullAccess CodexSandboxMode = "danger-full-access"
)

type ClaudePermissionMode string

const (
	ClaudePermissionDefault           ClaudePermissionMode = "default"
	ClaudePermissionAcceptEdits       ClaudePermissionMode = "acceptEdits"
	ClaudePermissionBypassPermissions ClaudePermissionMode = "bypassPermissions"
	ClaudePermissionPlan              ClaudePermissionMode = "plan"
)

type PermissionSource string

const (
	PermissionSourcePermissions PermissionSource = "permissions"
	PermissionSourceSandbox     PermissionSource = "sandbox"
	PermissionSourceDefault     PermissionSource = "default"
)

type PermissionConfig struct {
	DefaultAccess AccessMode              `json:"defaultAccess"`
	MaxAccess     AccessMode              `json:"maxAccess"`
	Claude        *ClaudePermissionConfig `json:"claude,omitempty"`
}

type ClaudePermissionConfig struct {
	PermissionMode ClaudePermissionMode `json:"permissionMode"`
}

type PartialPermissionConfig struct {
	DefaultAccess *AccessMode                    `json:"defaultAccess,omitempty"`
	MaxAccess     *AccessMode                    `json:"maxAccess,omitempty"`
	Claude        *PartialClaudePermissionConfig `json:"claude,omitempty"`
}

type PartialClaudePermissionConfig struct {
	PermissionMode *ClaudePermissionMode `json:"permissionMode,omitempty"`
}

type LegacySandboxInput struct {
	Default     *CodexSandboxMode `json:"default,omitempty"`
	Max         *CodexSandboxMode `json:"max,omitempty"`
	DefaultMode *CodexSandboxMode `json:"defaultMode,omitempty"`
	MaxMode     *CodexSandboxMode `json:"maxMode,omitempty"`
}

type LegacySandbox struct {
	Default     CodexSandboxMode `json:"default"`
	Max         CodexSandboxMode `json:"max"`
	DefaultMode CodexSandboxMode `json:"defaultMode"`
	MaxMode     CodexSandboxMode `json:"maxMode"`
}

type NormalizeInput struct {
	Permissions *PartialPermissionConfig `json:"permissions,omitempty"`
	Sandbox     *LegacySandboxInput      `json:"sandbox,omitempty"`
}

type NormalizedPermissions struct {
	Permissions PermissionConfig `json:"permissions"`
	Source      PermissionSource `json:"source"`
}
