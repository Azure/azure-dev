// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package consent

import (
	"context"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestPromptWorkflowConsent_SkipsWhenAllServersTrusted(t *testing.T) {
	mgr := newTestConsentManager(t)
	ctx := context.Background()
	servers := []string{"copilot", "azure", "azd"}

	// Pre-grant server-level rules for all three servers
	for _, server := range servers {
		err := mgr.GrantConsent(ctx, ConsentRule{
			Scope:      ScopeGlobal,
			Target:     NewServerTarget(server),
			Action:     ActionAny,
			Operation:  OperationTypeTool,
			Permission: PermissionAllow,
		})
		require.NoError(t, err)
	}

	// allServersTrusted should return true — prompt would be skipped
	require.True(t, allServersTrusted(ctx, mgr, servers))
}

func TestAllServersTrusted_FalseWhenNoRules(t *testing.T) {
	mgr := newTestConsentManager(t)
	ctx := context.Background()

	require.False(t, allServersTrusted(ctx, mgr, []string{"copilot", "azure", "azd"}))
}

func TestAllServersTrusted_FalseWhenPartiallyTrusted(t *testing.T) {
	mgr := newTestConsentManager(t)
	ctx := context.Background()

	// Only grant trust to one of three servers
	err := mgr.GrantConsent(ctx, ConsentRule{
		Scope:      ScopeGlobal,
		Target:     NewServerTarget("copilot"),
		Action:     ActionAny,
		Operation:  OperationTypeTool,
		Permission: PermissionAllow,
	})
	require.NoError(t, err)

	require.False(t, allServersTrusted(ctx, mgr, []string{"copilot", "azure", "azd"}))
}

func TestAllServersTrusted_TrueWithGlobalWildcard(t *testing.T) {
	mgr := newTestConsentManager(t)
	ctx := context.Background()

	// A global wildcard (*/*) should cover all servers
	err := mgr.GrantConsent(ctx, ConsentRule{
		Scope:      ScopeGlobal,
		Target:     NewGlobalTarget(),
		Action:     ActionAny,
		Operation:  OperationTypeTool,
		Permission: PermissionAllow,
	})
	require.NoError(t, err)

	require.True(t, allServersTrusted(ctx, mgr, []string{"copilot", "azure", "azd"}))
}

func TestAllServersTrusted_ReadOnlyRuleNotSufficient(t *testing.T) {
	mgr := newTestConsentManager(t)
	ctx := context.Background()

	// Grant only read-only access — should NOT count as full trust
	for _, server := range []string{"copilot", "azure", "azd"} {
		err := mgr.GrantConsent(ctx, ConsentRule{
			Scope:      ScopeGlobal,
			Target:     NewServerTarget(server),
			Action:     ActionReadOnly,
			Operation:  OperationTypeTool,
			Permission: PermissionAllow,
		})
		require.NoError(t, err)
	}

	require.False(t, allServersTrusted(ctx, mgr, []string{"copilot", "azure", "azd"}))
}

func TestGrantWorkflowRules_SessionScope(t *testing.T) {
	mgr := newTestConsentManager(t)
	ctx := context.Background()
	servers := []string{"copilot", "azure", "azd"}

	err := grantWorkflowRules(ctx, mgr, servers, ScopeSession)
	require.NoError(t, err)

	// Verify every server is now trusted for tool operations
	for _, server := range servers {
		req := ConsentRequest{
			ToolID:      server + "/any-tool",
			ServerName:  server,
			Operation:   OperationTypeTool,
			Annotations: mcp.ToolAnnotation{},
		}
		decision, err := mgr.CheckConsent(ctx, req)
		require.NoError(t, err, "server=%s", server)
		require.True(t, decision.Allowed, "server=%s should be allowed", server)
	}
}

func TestGrantWorkflowRules_GlobalScope(t *testing.T) {
	// Use a shared config dir so the second manager sees persisted rules
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)

	fileConfigMgr := config.NewFileConfigManager(config.NewManager())
	userConfigMgr := config.NewUserConfigManager(fileConfigMgr)

	lazyEnvMgr := lazy.NewLazy[environment.Manager](func() (environment.Manager, error) {
		return nil, fmt.Errorf("no environment in test")
	})

	mgr := &consentManager{
		lazyEnvManager:    lazyEnvMgr,
		userConfigManager: userConfigMgr,
		sessionRules:      make([]ConsentRule, 0),
	}

	ctx := context.Background()
	servers := []string{"copilot", "azure", "azd"}

	err := grantWorkflowRules(ctx, mgr, servers, ScopeGlobal)
	require.NoError(t, err)

	// Create a FRESH consent manager (simulating a new session) — same config dir
	mgr2 := &consentManager{
		lazyEnvManager:    lazyEnvMgr,
		userConfigManager: userConfigMgr,
		sessionRules:      make([]ConsentRule, 0),
	}

	for _, server := range servers {
		req := ConsentRequest{
			ToolID:      server + "/any-tool",
			ServerName:  server,
			Operation:   OperationTypeTool,
			Annotations: mcp.ToolAnnotation{},
		}
		decision, err := mgr2.CheckConsent(ctx, req)
		require.NoError(t, err, "server=%s", server)
		require.True(t, decision.Allowed, "server=%s should survive reload", server)
	}
}

func TestGrantWorkflowRules_SessionScope_DoesNotSurviveReload(t *testing.T) {
	mgr := newTestConsentManager(t)
	ctx := context.Background()

	err := grantWorkflowRules(ctx, mgr, []string{"copilot"}, ScopeSession)
	require.NoError(t, err)

	// Same manager — should work
	decision, err := mgr.CheckConsent(ctx, ConsentRequest{
		ToolID:      "copilot/any-tool",
		ServerName:  "copilot",
		Operation:   OperationTypeTool,
		Annotations: mcp.ToolAnnotation{},
	})
	require.NoError(t, err)
	require.True(t, decision.Allowed)

	// New manager — session rules gone
	mgr2 := newTestConsentManager(t)
	decision, err = mgr2.CheckConsent(ctx, ConsentRequest{
		ToolID:      "copilot/any-tool",
		ServerName:  "copilot",
		Operation:   OperationTypeTool,
		Annotations: mcp.ToolAnnotation{},
	})
	require.NoError(t, err)
	require.False(t, decision.Allowed)
	require.True(t, decision.RequiresPrompt)
}
