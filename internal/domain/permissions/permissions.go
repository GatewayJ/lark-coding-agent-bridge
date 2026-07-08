package permissions

import "fmt"

var accessOrder = map[AccessMode]int{
	AccessReadOnly:  0,
	AccessWorkspace: 1,
	AccessFull:      2,
}

var claudePermissionAccess = map[ClaudePermissionMode]AccessMode{
	ClaudePermissionPlan:              AccessReadOnly,
	ClaudePermissionDefault:           AccessWorkspace,
	ClaudePermissionAcceptEdits:       AccessWorkspace,
	ClaudePermissionBypassPermissions: AccessFull,
}

func NormalizePermissions(input NormalizeInput) (NormalizedPermissions, error) {
	hasSandbox := hasLegacySandbox(input.Sandbox)
	base := defaultPermissions()
	if hasSandbox {
		normalized, err := normalizeLegacySandboxPermissions(input.Sandbox)
		if err != nil {
			return NormalizedPermissions{}, err
		}
		base = normalized
	}

	if input.Permissions != nil {
		normalized, err := normalizeCanonicalPermissions(input.Permissions, base)
		if err != nil {
			return NormalizedPermissions{}, err
		}
		return NormalizedPermissions{
			Permissions: normalized,
			Source:      PermissionSourcePermissions,
		}, nil
	}

	source := PermissionSourceDefault
	if hasSandbox {
		source = PermissionSourceSandbox
	}
	return NormalizedPermissions{
		Permissions: base,
		Source:      source,
	}, nil
}

func AssertAccessPair(defaultAccess AccessMode, maxAccess AccessMode, source PermissionSource) error {
	if !IsAccessMode(defaultAccess) {
		return fmt.Errorf("invalid permission defaultAccess")
	}
	if !IsAccessMode(maxAccess) {
		return fmt.Errorf("invalid permission maxAccess")
	}
	if accessOrder[defaultAccess] > accessOrder[maxAccess] {
		suffix := ""
		if source == PermissionSourceSandbox {
			suffix = " from sandbox"
		}
		return fmt.Errorf("permission defaultAccess cannot exceed maxAccess%s", suffix)
	}
	return nil
}

func ClampAccess(defaultAccess AccessMode, profileMax AccessMode, capabilityMax AccessMode) AccessMode {
	maxAllowed := profileMax
	if accessOrder[capabilityMax] < accessOrder[profileMax] {
		maxAllowed = capabilityMax
	}
	if accessOrder[defaultAccess] <= accessOrder[maxAllowed] {
		return defaultAccess
	}
	return maxAllowed
}

func CodexSandboxToAccess(mode CodexSandboxMode) (AccessMode, error) {
	switch mode {
	case CodexSandboxReadOnly:
		return AccessReadOnly, nil
	case CodexSandboxWorkspaceWrite:
		return AccessWorkspace, nil
	case CodexSandboxDangerFullAccess:
		return AccessFull, nil
	default:
		return "", fmt.Errorf("invalid sandbox mode")
	}
}

func AccessToCodexSandbox(access AccessMode) (CodexSandboxMode, error) {
	switch access {
	case AccessReadOnly:
		return CodexSandboxReadOnly, nil
	case AccessWorkspace:
		return CodexSandboxWorkspaceWrite, nil
	case AccessFull:
		return CodexSandboxDangerFullAccess, nil
	default:
		return "", fmt.Errorf("invalid permission access")
	}
}

func AccessToClaudePermissionMode(access AccessMode, permissions *PermissionConfig) ClaudePermissionMode {
	if permissions != nil && permissions.Claude != nil {
		override := permissions.Claude.PermissionMode
		overrideAccess, ok := claudePermissionAccess[override]
		if ok && accessOrder[overrideAccess] <= accessOrder[access] {
			return override
		}
	}
	return accessToDefaultClaudePermissionMode(access)
}

func PermissionsToLegacySandbox(permissions PermissionConfig) (LegacySandbox, error) {
	defaultMode, err := AccessToCodexSandbox(permissions.DefaultAccess)
	if err != nil {
		return LegacySandbox{}, err
	}
	maxMode, err := AccessToCodexSandbox(permissions.MaxAccess)
	if err != nil {
		return LegacySandbox{}, err
	}
	return LegacySandbox{
		Default:     defaultMode,
		Max:         maxMode,
		DefaultMode: defaultMode,
		MaxMode:     maxMode,
	}, nil
}

func IsAccessMode(value AccessMode) bool {
	_, ok := accessOrder[value]
	return ok
}

func IsCodexSandboxMode(value CodexSandboxMode) bool {
	switch value {
	case CodexSandboxReadOnly, CodexSandboxWorkspaceWrite, CodexSandboxDangerFullAccess:
		return true
	default:
		return false
	}
}

func IsClaudePermissionMode(value ClaudePermissionMode) bool {
	_, ok := claudePermissionAccess[value]
	return ok
}

func normalizeCanonicalPermissions(input *PartialPermissionConfig, base PermissionConfig) (PermissionConfig, error) {
	maxAccess := base.MaxAccess
	if input.MaxAccess != nil {
		if !IsAccessMode(*input.MaxAccess) {
			return PermissionConfig{}, fmt.Errorf("invalid permission maxAccess")
		}
		maxAccess = *input.MaxAccess
	}

	defaultAccess := base.DefaultAccess
	if input.DefaultAccess != nil {
		if !IsAccessMode(*input.DefaultAccess) {
			return PermissionConfig{}, fmt.Errorf("invalid permission defaultAccess")
		}
		defaultAccess = *input.DefaultAccess
	} else if accessOrder[base.DefaultAccess] > accessOrder[maxAccess] {
		defaultAccess = maxAccess
	}

	if err := AssertAccessPair(defaultAccess, maxAccess, PermissionSourcePermissions); err != nil {
		return PermissionConfig{}, err
	}

	claude, err := normalizeClaudePermissions(input.Claude)
	if err != nil {
		return PermissionConfig{}, err
	}
	if claude != nil {
		if err := assertClaudePermissionWithinAccess(claude.PermissionMode, maxAccess); err != nil {
			return PermissionConfig{}, err
		}
	}

	return PermissionConfig{
		DefaultAccess: defaultAccess,
		MaxAccess:     maxAccess,
		Claude:        claude,
	}, nil
}

func defaultPermissions() PermissionConfig {
	return PermissionConfig{
		DefaultAccess: AccessFull,
		MaxAccess:     AccessFull,
	}
}

func assertClaudePermissionWithinAccess(permissionMode ClaudePermissionMode, maxAccess AccessMode) error {
	if accessOrder[claudePermissionAccess[permissionMode]] > accessOrder[maxAccess] {
		return fmt.Errorf("permission claude.permissionMode cannot exceed maxAccess")
	}
	return nil
}

func normalizeLegacySandboxPermissions(input *LegacySandboxInput) (PermissionConfig, error) {
	maxMode := CodexSandboxDangerFullAccess
	if input.Max != nil {
		if !IsCodexSandboxMode(*input.Max) {
			return PermissionConfig{}, fmt.Errorf("invalid sandbox maxMode")
		}
		maxMode = *input.Max
	} else if input.MaxMode != nil {
		if !IsCodexSandboxMode(*input.MaxMode) {
			return PermissionConfig{}, fmt.Errorf("invalid sandbox maxMode")
		}
		maxMode = *input.MaxMode
	}

	defaultMode := maxMode
	if input.Default != nil {
		if !IsCodexSandboxMode(*input.Default) {
			return PermissionConfig{}, fmt.Errorf("invalid sandbox defaultMode")
		}
		defaultMode = *input.Default
	} else if input.DefaultMode != nil {
		if !IsCodexSandboxMode(*input.DefaultMode) {
			return PermissionConfig{}, fmt.Errorf("invalid sandbox defaultMode")
		}
		defaultMode = *input.DefaultMode
	}

	defaultAccess, err := CodexSandboxToAccess(defaultMode)
	if err != nil {
		return PermissionConfig{}, err
	}
	maxAccess, err := CodexSandboxToAccess(maxMode)
	if err != nil {
		return PermissionConfig{}, err
	}
	if err := AssertAccessPair(defaultAccess, maxAccess, PermissionSourceSandbox); err != nil {
		return PermissionConfig{}, err
	}

	return PermissionConfig{
		DefaultAccess: defaultAccess,
		MaxAccess:     maxAccess,
	}, nil
}

func normalizeClaudePermissions(input *PartialClaudePermissionConfig) (*ClaudePermissionConfig, error) {
	if input == nil || input.PermissionMode == nil {
		return nil, nil
	}
	if !IsClaudePermissionMode(*input.PermissionMode) {
		return nil, fmt.Errorf("invalid permission claude.permissionMode")
	}
	return &ClaudePermissionConfig{
		PermissionMode: *input.PermissionMode,
	}, nil
}

func hasLegacySandbox(input *LegacySandboxInput) bool {
	if input == nil {
		return false
	}
	return input.Default != nil ||
		input.Max != nil ||
		input.DefaultMode != nil ||
		input.MaxMode != nil
}

func accessToDefaultClaudePermissionMode(access AccessMode) ClaudePermissionMode {
	switch access {
	case AccessReadOnly:
		return ClaudePermissionPlan
	case AccessWorkspace:
		return ClaudePermissionAcceptEdits
	case AccessFull:
		return ClaudePermissionBypassPermissions
	default:
		return ClaudePermissionPlan
	}
}
