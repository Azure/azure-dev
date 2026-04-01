// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package consent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewToolTarget(t *testing.T) {
	target := NewToolTarget("myServer", "myTool")
	require.Equal(t, Target("myServer/myTool"), target)
}

func TestNewServerTarget(t *testing.T) {
	target := NewServerTarget("myServer")
	require.Equal(t, Target("myServer/*"), target)
}

func TestNewGlobalTarget(t *testing.T) {
	target := NewGlobalTarget()
	require.Equal(t, Target("*/*"), target)
}

func TestTargetValidate(t *testing.T) {
	tests := []struct {
		name    string
		target  Target
		wantErr bool
		errMsg  string
	}{
		{
			name:   "ValidServerTool",
			target: Target("server/tool"),
		},
		{
			name:   "ValidServerWildcard",
			target: Target("server/*"),
		},
		{
			name:   "ValidGlobalStar",
			target: Target("*"),
		},
		{
			name:   "ValidGlobalStarSlashStar",
			target: Target("*/*"),
		},
		{
			name:    "Empty",
			target:  Target(""),
			wantErr: true,
			errMsg:  "target cannot be empty",
		},
		{
			name:    "NoSlash",
			target:  Target("noslash"),
			wantErr: true,
			errMsg:  "target must be in format",
		},
		{
			name:    "EmptyServer",
			target:  Target("/tool"),
			wantErr: true,
			errMsg:  "server part of target cannot be empty",
		},
		{
			name:    "EmptyTool",
			target:  Target("server/"),
			wantErr: true,
			errMsg:  "tool part of target cannot be empty",
		},
		{
			name:    "TooManyParts",
			target:  Target("a/b/c"),
			wantErr: true,
			errMsg:  "target must be in format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.target.Validate()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestParseOperationType(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    OperationType
		wantErr bool
	}{
		{"Tool", "tool", OperationTypeTool, false},
		{"Sampling", "sampling", OperationTypeSampling, false},
		{"Elicitation", "elicitation", OperationTypeElicitation, false},
		{"Invalid", "unknown", "", true},
		{"Empty", "", "", true},
		{"CaseSensitive", "Tool", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseOperationType(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "invalid operation context")
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestParseScope(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Scope
		wantErr bool
	}{
		{"Global", "global", ScopeGlobal, false},
		{"Project", "project", ScopeProject, false},
		{"Session", "session", ScopeSession, false},
		{"OneTime", "one_time", ScopeOneTime, false},
		{"Invalid", "forever", "", true},
		{"Empty", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseScope(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "invalid scope")
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestParseActionType(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    ActionType
		wantErr bool
	}{
		{"ReadOnly", "readonly", ActionReadOnly, false},
		{"All", "all", ActionAny, false},
		{"Invalid", "write", "", true},
		{"Empty", "", "", true},
		{"AnyLiteral", "any", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseActionType(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "invalid action type")
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestParsePermission(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Permission
		wantErr bool
	}{
		{"Allow", "allow", PermissionAllow, false},
		{"Deny", "deny", PermissionDeny, false},
		{"Prompt", "prompt", PermissionPrompt, false},
		{"Invalid", "block", "", true},
		{"Empty", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePermission(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "invalid permission")
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestConsentRuleValidate(t *testing.T) {
	validRule := ConsentRule{
		Scope:      ScopeGlobal,
		Target:     NewToolTarget("server", "tool"),
		Action:     ActionAny,
		Operation:  OperationTypeTool,
		Permission: PermissionAllow,
		GrantedAt:  time.Now(),
	}

	tests := []struct {
		name    string
		modify  func(r *ConsentRule)
		wantErr bool
		errMsg  string
	}{
		{
			name:   "Valid",
			modify: func(_ *ConsentRule) {},
		},
		{
			name: "InvalidTarget",
			modify: func(r *ConsentRule) {
				r.Target = Target("")
			},
			wantErr: true,
			errMsg:  "invalid target",
		},
		{
			name: "InvalidScope",
			modify: func(r *ConsentRule) {
				r.Scope = Scope("bogus")
			},
			wantErr: true,
			errMsg:  "invalid scope",
		},
		{
			name: "InvalidAction",
			modify: func(r *ConsentRule) {
				r.Action = ActionType("write")
			},
			wantErr: true,
			errMsg:  "invalid action",
		},
		{
			name: "InvalidOperation",
			modify: func(r *ConsentRule) {
				r.Operation = OperationType("deploy")
			},
			wantErr: true,
			errMsg:  "invalid operation context",
		},
		{
			name: "InvalidPermission",
			modify: func(r *ConsentRule) {
				r.Permission = Permission("maybe")
			},
			wantErr: true,
			errMsg:  "invalid decision",
		},
		{
			name: "AllScopes",
			modify: func(r *ConsentRule) {
				r.Scope = ScopeSession
			},
		},
		{
			name: "ReadOnlyAction",
			modify: func(r *ConsentRule) {
				r.Action = ActionReadOnly
			},
		},
		{
			name: "SamplingOperation",
			modify: func(r *ConsentRule) {
				r.Operation = OperationTypeSampling
			},
		},
		{
			name: "ElicitationOperation",
			modify: func(r *ConsentRule) {
				r.Operation = OperationTypeElicitation
			},
		},
		{
			name: "DenyPermission",
			modify: func(r *ConsentRule) {
				r.Permission = PermissionDeny
			},
		},
		{
			name: "PromptPermission",
			modify: func(r *ConsentRule) {
				r.Permission = PermissionPrompt
			},
		},
		{
			name: "GlobalWildcardTarget",
			modify: func(r *ConsentRule) {
				r.Target = NewGlobalTarget()
			},
		},
		{
			name: "ServerWildcardTarget",
			modify: func(r *ConsentRule) {
				r.Target = NewServerTarget("myServer")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := validRule
			tt.modify(&rule)
			err := rule.Validate()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestFilterOptions(t *testing.T) {
	t.Run("WithScope", func(t *testing.T) {
		var opts FilterOptions
		WithScope(ScopeGlobal)(&opts)
		require.NotNil(t, opts.Scope)
		require.Equal(t, ScopeGlobal, *opts.Scope)
	})

	t.Run("WithOperation", func(t *testing.T) {
		var opts FilterOptions
		WithOperation(OperationTypeTool)(&opts)
		require.NotNil(t, opts.Operation)
		require.Equal(t, OperationTypeTool, *opts.Operation)
	})

	t.Run("WithTarget", func(t *testing.T) {
		var opts FilterOptions
		target := NewToolTarget("s", "t")
		WithTarget(target)(&opts)
		require.NotNil(t, opts.Target)
		require.Equal(t, target, *opts.Target)
	})

	t.Run("WithAction", func(t *testing.T) {
		var opts FilterOptions
		WithAction(ActionReadOnly)(&opts)
		require.NotNil(t, opts.Action)
		require.Equal(t, ActionReadOnly, *opts.Action)
	})

	t.Run("WithPermission", func(t *testing.T) {
		var opts FilterOptions
		WithPermission(PermissionDeny)(&opts)
		require.NotNil(t, opts.Permission)
		require.Equal(t, PermissionDeny, *opts.Permission)
	})

	t.Run("MultipleOptions", func(t *testing.T) {
		var opts FilterOptions
		for _, fn := range []FilterOption{
			WithScope(ScopeSession),
			WithOperation(OperationTypeSampling),
			WithPermission(PermissionAllow),
		} {
			fn(&opts)
		}
		require.NotNil(t, opts.Scope)
		require.Equal(t, ScopeSession, *opts.Scope)
		require.NotNil(t, opts.Operation)
		require.Equal(t, OperationTypeSampling, *opts.Operation)
		require.NotNil(t, opts.Permission)
		require.Equal(t, PermissionAllow, *opts.Permission)
		require.Nil(t, opts.Target)
		require.Nil(t, opts.Action)
	})
}
