package permissions

import (
	"strings"
	"testing"
)

func TestNormalizePermissionsDefault(t *testing.T) {
	got, err := NormalizePermissions(NormalizeInput{})
	if err != nil {
		t.Fatalf("NormalizePermissions returned error: %v", err)
	}

	want := PermissionConfig{
		DefaultAccess: AccessFull,
		MaxAccess:     AccessFull,
	}
	if got.Source != PermissionSourceDefault {
		t.Fatalf("Source = %q, want %q", got.Source, PermissionSourceDefault)
	}
	if got.Permissions != want {
		t.Fatalf("Permissions = %#v, want %#v", got.Permissions, want)
	}
}

func TestNormalizePermissionsLegacySandbox(t *testing.T) {
	defaultMode := CodexSandboxReadOnly
	maxMode := CodexSandboxWorkspaceWrite

	got, err := NormalizePermissions(NormalizeInput{
		Sandbox: &LegacySandboxInput{
			DefaultMode: &defaultMode,
			MaxMode:     &maxMode,
		},
	})
	if err != nil {
		t.Fatalf("NormalizePermissions returned error: %v", err)
	}

	want := PermissionConfig{
		DefaultAccess: AccessReadOnly,
		MaxAccess:     AccessWorkspace,
	}
	if got.Source != PermissionSourceSandbox {
		t.Fatalf("Source = %q, want %q", got.Source, PermissionSourceSandbox)
	}
	if got.Permissions != want {
		t.Fatalf("Permissions = %#v, want %#v", got.Permissions, want)
	}
}

func TestCodexSandboxAccessConversions(t *testing.T) {
	tests := []struct {
		sandbox CodexSandboxMode
		access  AccessMode
	}{
		{sandbox: CodexSandboxReadOnly, access: AccessReadOnly},
		{sandbox: CodexSandboxWorkspaceWrite, access: AccessWorkspace},
		{sandbox: CodexSandboxDangerFullAccess, access: AccessFull},
	}

	for _, tt := range tests {
		t.Run(string(tt.sandbox), func(t *testing.T) {
			access, err := CodexSandboxToAccess(tt.sandbox)
			if err != nil {
				t.Fatalf("CodexSandboxToAccess returned error: %v", err)
			}
			if access != tt.access {
				t.Fatalf("CodexSandboxToAccess = %q, want %q", access, tt.access)
			}

			sandbox, err := AccessToCodexSandbox(tt.access)
			if err != nil {
				t.Fatalf("AccessToCodexSandbox returned error: %v", err)
			}
			if sandbox != tt.sandbox {
				t.Fatalf("AccessToCodexSandbox = %q, want %q", sandbox, tt.sandbox)
			}
		})
	}
}

func TestNormalizePermissionsCanonicalOverrideKeepsLegacyBase(t *testing.T) {
	defaultMode := CodexSandboxReadOnly
	maxMode := CodexSandboxReadOnly
	permissionMode := ClaudePermissionPlan

	got, err := NormalizePermissions(NormalizeInput{
		Sandbox: &LegacySandboxInput{
			DefaultMode: &defaultMode,
			MaxMode:     &maxMode,
		},
		Permissions: &PartialPermissionConfig{
			Claude: &PartialClaudePermissionConfig{
				PermissionMode: &permissionMode,
			},
		},
	})
	if err != nil {
		t.Fatalf("NormalizePermissions returned error: %v", err)
	}

	if got.Source != PermissionSourcePermissions {
		t.Fatalf("Source = %q, want %q", got.Source, PermissionSourcePermissions)
	}
	if got.Permissions.DefaultAccess != AccessReadOnly {
		t.Fatalf("DefaultAccess = %q, want %q", got.Permissions.DefaultAccess, AccessReadOnly)
	}
	if got.Permissions.MaxAccess != AccessReadOnly {
		t.Fatalf("MaxAccess = %q, want %q", got.Permissions.MaxAccess, AccessReadOnly)
	}
	if got.Permissions.Claude == nil || got.Permissions.Claude.PermissionMode != ClaudePermissionPlan {
		t.Fatalf("Claude = %#v, want permissionMode %q", got.Permissions.Claude, ClaudePermissionPlan)
	}
}

func TestNormalizePermissionsInvalidAccessPair(t *testing.T) {
	defaultAccess := AccessFull
	maxAccess := AccessWorkspace

	_, err := NormalizePermissions(NormalizeInput{
		Permissions: &PartialPermissionConfig{
			DefaultAccess: &defaultAccess,
			MaxAccess:     &maxAccess,
		},
	})
	if err == nil {
		t.Fatal("NormalizePermissions returned nil error")
	}
	if !strings.Contains(err.Error(), "defaultAccess cannot exceed maxAccess") {
		t.Fatalf("error = %q, want defaultAccess/maxAccess validation", err)
	}
}

func TestAccessToClaudePermissionModeDefaults(t *testing.T) {
	tests := []struct {
		access AccessMode
		want   ClaudePermissionMode
	}{
		{access: AccessReadOnly, want: ClaudePermissionPlan},
		{access: AccessWorkspace, want: ClaudePermissionAcceptEdits},
		{access: AccessFull, want: ClaudePermissionBypassPermissions},
	}

	for _, tt := range tests {
		t.Run(string(tt.access), func(t *testing.T) {
			got := AccessToClaudePermissionMode(tt.access, nil)
			if got != tt.want {
				t.Fatalf("AccessToClaudePermissionMode = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAccessToClaudePermissionModeOverrideDoesNotExceedAccess(t *testing.T) {
	cfg := PermissionConfig{
		DefaultAccess: AccessWorkspace,
		MaxAccess:     AccessFull,
		Claude: &ClaudePermissionConfig{
			PermissionMode: ClaudePermissionDefault,
		},
	}

	got := AccessToClaudePermissionMode(AccessWorkspace, &cfg)
	if got != ClaudePermissionDefault {
		t.Fatalf("AccessToClaudePermissionMode = %q, want %q", got, ClaudePermissionDefault)
	}
}

func TestAccessToClaudePermissionModeOverrideExceedingAccessFallsBack(t *testing.T) {
	cfg := PermissionConfig{
		DefaultAccess: AccessReadOnly,
		MaxAccess:     AccessFull,
		Claude: &ClaudePermissionConfig{
			PermissionMode: ClaudePermissionBypassPermissions,
		},
	}

	got := AccessToClaudePermissionMode(AccessReadOnly, &cfg)
	if got != ClaudePermissionPlan {
		t.Fatalf("AccessToClaudePermissionMode = %q, want %q", got, ClaudePermissionPlan)
	}
}

func TestNormalizePermissionsClaudeOverrideExceedingMaxErrors(t *testing.T) {
	maxAccess := AccessWorkspace
	permissionMode := ClaudePermissionBypassPermissions

	_, err := NormalizePermissions(NormalizeInput{
		Permissions: &PartialPermissionConfig{
			MaxAccess: &maxAccess,
			Claude: &PartialClaudePermissionConfig{
				PermissionMode: &permissionMode,
			},
		},
	})
	if err == nil {
		t.Fatal("NormalizePermissions returned nil error")
	}
	if !strings.Contains(err.Error(), "claude.permissionMode cannot exceed maxAccess") {
		t.Fatalf("error = %q, want Claude max access validation", err)
	}
}

func TestClampAccess(t *testing.T) {
	tests := []struct {
		name          string
		defaultAccess AccessMode
		profileMax    AccessMode
		capabilityMax AccessMode
		want          AccessMode
	}{
		{
			name:          "profile max clamps default",
			defaultAccess: AccessFull,
			profileMax:    AccessWorkspace,
			capabilityMax: AccessFull,
			want:          AccessWorkspace,
		},
		{
			name:          "capability max clamps default",
			defaultAccess: AccessWorkspace,
			profileMax:    AccessFull,
			capabilityMax: AccessReadOnly,
			want:          AccessReadOnly,
		},
		{
			name:          "default below max is preserved",
			defaultAccess: AccessReadOnly,
			profileMax:    AccessFull,
			capabilityMax: AccessFull,
			want:          AccessReadOnly,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClampAccess(tt.defaultAccess, tt.profileMax, tt.capabilityMax)
			if got != tt.want {
				t.Fatalf("ClampAccess = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPermissionsToLegacySandbox(t *testing.T) {
	got, err := PermissionsToLegacySandbox(PermissionConfig{
		DefaultAccess: AccessWorkspace,
		MaxAccess:     AccessFull,
	})
	if err != nil {
		t.Fatalf("PermissionsToLegacySandbox returned error: %v", err)
	}

	want := LegacySandbox{
		Default:     CodexSandboxWorkspaceWrite,
		Max:         CodexSandboxDangerFullAccess,
		DefaultMode: CodexSandboxWorkspaceWrite,
		MaxMode:     CodexSandboxDangerFullAccess,
	}
	if got != want {
		t.Fatalf("PermissionsToLegacySandbox = %#v, want %#v", got, want)
	}
}
