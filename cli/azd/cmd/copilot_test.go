// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func TestFormatConsentDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		scope      string
		action     string
		operation  string
		target     string
		permission string
		expect     []string
		notExpect  []string
	}{
		{
			name:       "all_fields",
			scope:      "project",
			action:     "read",
			operation:  "list",
			target:     "env",
			permission: "write",
			expect: []string{
				"Scope: project",
				"Action: read",
				"Context: list",
				"Target: env",
				"Permission: write",
			},
		},
		{
			name:       "partial_fields",
			scope:      "global",
			action:     "deploy",
			operation:  "",
			target:     "",
			permission: "",
			expect:     []string{"Scope: global", "Action: deploy"},
			notExpect:  []string{"Context:", "Target:", "Permission:"},
		},
		{
			name:       "no_fields",
			scope:      "",
			action:     "",
			operation:  "",
			target:     "",
			permission: "",
			expect:     []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := formatConsentDescription(
				tt.scope, tt.action, tt.operation,
				tt.target, tt.permission,
			)
			for _, s := range tt.expect {
				assert.Contains(t, result, s)
			}
			for _, s := range tt.notExpect {
				assert.NotContains(t, result, s)
			}
		})
	}
}

func TestFormatConsentRuleDescription(t *testing.T) {
	t.Parallel()

	rule := consent.ConsentRule{
		Scope:      "subscription",
		Action:     "provision",
		Operation:  "create",
		Target:     "resources",
		Permission: "admin",
	}

	result := formatConsentRuleDescription(rule)
	require.Contains(t, result, "Scope: subscription")
	require.Contains(t, result, "Action: provision")
	require.Contains(t, result, "Context: create")
	require.Contains(t, result, "Target: resources")
	require.Contains(t, result, "Permission: admin")
}

// mockConsentManager implements consent.ConsentManager for testing.
type mockConsentManager struct {
	rules    []consent.ConsentRule
	listErr  error
	clearErr error
	grantErr error
}

func (m *mockConsentManager) ListConsentRules(
	ctx context.Context, filterOptions ...consent.FilterOption,
) ([]consent.ConsentRule, error) {
	return m.rules, m.listErr
}

func (m *mockConsentManager) ClearConsentRules(
	ctx context.Context, filterOptions ...consent.FilterOption,
) error {
	return m.clearErr
}

func (m *mockConsentManager) GrantConsent(ctx context.Context, rule consent.ConsentRule) error {
	return m.grantErr
}

func (m *mockConsentManager) CheckConsent(
	ctx context.Context, request consent.ConsentRequest,
) (*consent.ConsentDecision, error) {
	return &consent.ConsentDecision{Allowed: true}, nil
}

func (m *mockConsentManager) PromptWorkflowConsent(ctx context.Context, servers []string) error {
	return nil
}

func (m *mockConsentManager) IsProjectScopeAvailable(ctx context.Context) bool {
	return false
}

func testUserConfigManager(t *testing.T) config.UserConfigManager {
	t.Helper()
	mockCtx := mocks.NewMockContext(t.Context())
	return config.NewUserConfigManager(mockCtx.ConfigManager)
}

// --- List Action Tests ---

func Test_CopilotConsentListAction_NoRules(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	action := newCopilotConsentListAction(
		&copilotConsentListFlags{},
		&output.JsonFormatter{}, buf,
		mockinput.NewMockConsole(), testUserConfigManager(t),
		&mockConsentManager{rules: nil},
	)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Contains(t, buf.String(), "No consent rules found")
}

func Test_CopilotConsentListAction_NoRulesWithFilter(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	action := newCopilotConsentListAction(
		&copilotConsentListFlags{scope: "global"},
		&output.JsonFormatter{}, buf,
		mockinput.NewMockConsole(), testUserConfigManager(t),
		&mockConsentManager{rules: nil},
	)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Contains(t, buf.String(), "No consent rules found matching filters")
}

func Test_CopilotConsentListAction_WithRulesJson(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	cm := &mockConsentManager{
		rules: []consent.ConsentRule{{
			Scope: consent.ScopeGlobal, Target: consent.NewGlobalTarget(),
			Action: consent.ActionAny, Operation: consent.OperationTypeTool,
			Permission: consent.PermissionAllow,
		}},
	}
	action := newCopilotConsentListAction(
		&copilotConsentListFlags{},
		&output.JsonFormatter{}, buf,
		mockinput.NewMockConsole(), testUserConfigManager(t), cm,
	)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Contains(t, buf.String(), "global")
}

func Test_CopilotConsentListAction_InvalidScope(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentListAction(
		&copilotConsentListFlags{scope: "invalid-scope"},
		&output.JsonFormatter{}, &bytes.Buffer{},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_CopilotConsentListAction_InvalidOperation(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentListAction(
		&copilotConsentListFlags{operation: "invalid-operation"},
		&output.JsonFormatter{}, &bytes.Buffer{},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_CopilotConsentListAction_InvalidAction(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentListAction(
		&copilotConsentListFlags{action: "invalid-action"},
		&output.JsonFormatter{}, &bytes.Buffer{},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_CopilotConsentListAction_InvalidPermission(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentListAction(
		&copilotConsentListFlags{permission: "invalid-permission"},
		&output.JsonFormatter{}, &bytes.Buffer{},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_CopilotConsentListAction_ListError(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentListAction(
		&copilotConsentListFlags{},
		&output.JsonFormatter{}, &bytes.Buffer{},
		mockinput.NewMockConsole(), testUserConfigManager(t),
		&mockConsentManager{listErr: assert.AnError},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list consent rules")
}

func Test_CopilotConsentListAction_WithTargetFilter(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	action := newCopilotConsentListAction(
		&copilotConsentListFlags{target: "server/tool"},
		&output.JsonFormatter{}, buf,
		mockinput.NewMockConsole(), testUserConfigManager(t),
		&mockConsentManager{rules: nil},
	)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Contains(t, buf.String(), "No consent rules found matching filters")
}

func Test_CopilotConsentListAction_TableFormat(t *testing.T) {
	t.Parallel()
	cm := &mockConsentManager{
		rules: []consent.ConsentRule{{
			Scope: consent.ScopeGlobal, Target: consent.NewGlobalTarget(),
			Action: consent.ActionAny, Operation: consent.OperationTypeTool,
			Permission: consent.PermissionAllow,
		}},
	}
	action := newCopilotConsentListAction(
		&copilotConsentListFlags{},
		&output.TableFormatter{}, &bytes.Buffer{},
		mockinput.NewMockConsole(), testUserConfigManager(t), cm,
	)
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func Test_CopilotConsentListAction_NoneFormat(t *testing.T) {
	t.Parallel()
	cm := &mockConsentManager{
		rules: []consent.ConsentRule{{
			Scope: consent.ScopeGlobal, Target: consent.NewGlobalTarget(),
			Action: consent.ActionAny, Operation: consent.OperationTypeTool,
			Permission: consent.PermissionAllow,
		}},
	}
	// NoneFormatter returns an error when attempting to format data.
	// Use it to exercise the fallback path and verify the error surfaces.
	action := newCopilotConsentListAction(
		&copilotConsentListFlags{},
		&output.NoneFormatter{}, &bytes.Buffer{},
		mockinput.NewMockConsole(), testUserConfigManager(t), cm,
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none")
}

// --- Grant Action Tests ---

func Test_CopilotConsentGrantAction_ToolWithoutServer(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentGrantAction(
		&copilotConsentGrantFlags{
			tool: "my-tool", scope: "global", action: "all", operation: "tool", permission: "allow",
		},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, internal.ErrInvalidFlagCombination)
}

func Test_CopilotConsentGrantAction_GlobalWithServer(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentGrantAction(
		&copilotConsentGrantFlags{
			globalFlag: true, server: "my-server",
			scope: "global", action: "all", operation: "tool", permission: "allow",
		},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, internal.ErrInvalidFlagCombination)
}

func Test_CopilotConsentGrantAction_NeitherGlobalNorServer(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentGrantAction(
		&copilotConsentGrantFlags{
			scope: "global", action: "all", operation: "tool", permission: "allow",
		},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, internal.ErrInvalidFlagCombination)
}

func Test_CopilotConsentGrantAction_InvalidAction(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentGrantAction(
		&copilotConsentGrantFlags{
			globalFlag: true, scope: "global", action: "bad-action", operation: "tool", permission: "allow",
		},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_CopilotConsentGrantAction_InvalidOperation(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentGrantAction(
		&copilotConsentGrantFlags{
			globalFlag: true, scope: "global", action: "all", operation: "bad-op", permission: "allow",
		},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_CopilotConsentGrantAction_InvalidPermission(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentGrantAction(
		&copilotConsentGrantFlags{
			globalFlag: true, scope: "global", action: "all", operation: "tool", permission: "bad-perm",
		},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_CopilotConsentGrantAction_InvalidScope(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentGrantAction(
		&copilotConsentGrantFlags{
			globalFlag: true, scope: "bad-scope", action: "all", operation: "tool", permission: "allow",
		},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_CopilotConsentGrantAction_SamplingWithTool(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentGrantAction(
		&copilotConsentGrantFlags{
			server: "my-server", tool: "my-tool",
			scope: "global", action: "all", operation: "sampling", permission: "allow",
		},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, internal.ErrInvalidFlagCombination)
}

func Test_CopilotConsentGrantAction_Success_Global(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentGrantAction(
		&copilotConsentGrantFlags{
			globalFlag: true, scope: "global", action: "all", operation: "tool", permission: "allow",
		},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Message.Header, "granted successfully")
}

func Test_CopilotConsentGrantAction_Success_ServerTarget(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentGrantAction(
		&copilotConsentGrantFlags{
			server: "my-server", scope: "global", action: "all", operation: "tool", permission: "allow",
		},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_CopilotConsentGrantAction_Success_ToolTarget(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentGrantAction(
		&copilotConsentGrantFlags{
			server: "my-server", tool: "my-tool",
			scope: "global", action: "all", operation: "tool", permission: "allow",
		},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_CopilotConsentGrantAction_GrantError(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentGrantAction(
		&copilotConsentGrantFlags{
			globalFlag: true, scope: "global", action: "all", operation: "tool", permission: "allow",
		},
		mockinput.NewMockConsole(), testUserConfigManager(t),
		&mockConsentManager{grantErr: assert.AnError},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to grant consent")
}

// --- Revoke Action Tests ---

func Test_CopilotConsentRevokeAction_Confirmed(t *testing.T) {
	t.Parallel()
	mc := mockinput.NewMockConsole()
	mc.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).Respond(true)
	action := newCopilotConsentRevokeAction(
		&copilotConsentRevokeFlags{}, mc, testUserConfigManager(t), &mockConsentManager{},
	)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Message.Header, "revoked successfully")
}

func Test_CopilotConsentRevokeAction_Cancelled(t *testing.T) {
	t.Parallel()
	mc := mockinput.NewMockConsole()
	mc.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).Respond(false)
	action := newCopilotConsentRevokeAction(
		&copilotConsentRevokeFlags{}, mc, testUserConfigManager(t), &mockConsentManager{},
	)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	assert.Nil(t, result)
}

func Test_CopilotConsentRevokeAction_WithFilters(t *testing.T) {
	t.Parallel()
	mc := mockinput.NewMockConsole()
	mc.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).Respond(true)
	action := newCopilotConsentRevokeAction(
		&copilotConsentRevokeFlags{
			scope: "global", operation: "tool", target: "my-server", action: "all", permission: "allow",
		}, mc, testUserConfigManager(t), &mockConsentManager{},
	)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_CopilotConsentRevokeAction_InvalidScope(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentRevokeAction(
		&copilotConsentRevokeFlags{scope: "bad-scope"},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_CopilotConsentRevokeAction_InvalidOperation(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentRevokeAction(
		&copilotConsentRevokeFlags{operation: "bad-op"},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_CopilotConsentRevokeAction_InvalidAction(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentRevokeAction(
		&copilotConsentRevokeFlags{action: "bad-action"},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_CopilotConsentRevokeAction_InvalidPermission(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentRevokeAction(
		&copilotConsentRevokeFlags{permission: "bad-perm"},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_CopilotConsentRevokeAction_ClearError(t *testing.T) {
	t.Parallel()
	mc := mockinput.NewMockConsole()
	mc.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).Respond(true)
	action := newCopilotConsentRevokeAction(
		&copilotConsentRevokeFlags{}, mc, testUserConfigManager(t),
		&mockConsentManager{clearErr: assert.AnError},
	)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to clear consent rules")
}

// --- Constructor Tests ---

func Test_NewCopilotConsentListAction_ReturnsAction(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentListAction(
		&copilotConsentListFlags{}, &output.JsonFormatter{}, io.Discard,
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	require.NotNil(t, action)
	_ = action // already actions.Action
}

func Test_NewCopilotConsentRevokeAction_ReturnsAction(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentRevokeAction(
		&copilotConsentRevokeFlags{},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	require.NotNil(t, action)
	_ = action // already actions.Action
}

func Test_NewCopilotConsentGrantAction_ReturnsAction(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentGrantAction(
		&copilotConsentGrantFlags{},
		mockinput.NewMockConsole(), testUserConfigManager(t), &mockConsentManager{},
	)
	require.NotNil(t, action)
	_ = action // already actions.Action
}

// --- formatConsentDescription Tests ---

func Test_FormatConsentDescription(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, scope, action, operation, target, permission, expected string
	}{
		{"AllEmpty", "", "", "", "", "", ""},
		{"ScopeOnly", "global", "", "", "", "", "Scope: global"},
		{"AllSet", "global", "any", "tool", "server", "allow",
			"Scope: global, Target: server, Context: tool, Action: any, Permission: allow"},
		{"PartialSet", "", "readonly", "", "my-target", "",
			"Target: my-target, Action: readonly"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatConsentDescription(tt.scope, tt.action, tt.operation, tt.target, tt.permission)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func Test_NewCopilotConsentListAction(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentListAction(
		&copilotConsentListFlags{},
		&output.JsonFormatter{},
		&bytes.Buffer{},
		mockinput.NewMockConsole(),
		nil, // userConfigManager
		nil, // consentManager
	)
	require.NotNil(t, action)
}

func Test_NewCopilotConsentGrantAction(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentGrantAction(
		&copilotConsentGrantFlags{},
		mockinput.NewMockConsole(),
		nil, // userConfigManager
		nil, // consentManager
	)
	require.NotNil(t, action)
}

func Test_NewCopilotConsentRevokeAction(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentRevokeAction(
		&copilotConsentRevokeFlags{},
		mockinput.NewMockConsole(),
		nil, // userConfigManager
		nil, // consentManager
	)
	require.NotNil(t, action)
}

func Test_NewCopilotConsentListFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newCopilotConsentListFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewCopilotConsentGrantFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newCopilotConsentGrantFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewCopilotConsentRevokeFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newCopilotConsentRevokeFlags(cmd, global)
	require.NotNil(t, flags)
}
