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

// TestConsentPersistence_AlwaysAllow reproduces the scenario:
// Read 1: approve once, Read 2: approve once, Read 3: approve always, Read 4+: should auto-approve.
func TestConsentPersistence_AlwaysAllow(t *testing.T) {
	mgr := newTestConsentManager(t)
	ctx := context.Background()

	readOnly := true
	readReq := ConsentRequest{
		ToolID:     "copilot/read",
		ServerName: "copilot",
		Operation:  OperationTypeTool,
		Annotations: mcp.ToolAnnotation{
			ReadOnlyHint: &readOnly,
		},
	}

	// Read 1 & 2: no rules yet → should require prompt
	decision, err := mgr.CheckConsent(ctx, readReq)
	require.NoError(t, err)
	require.False(t, decision.Allowed, "read 1 should not be auto-allowed")
	require.True(t, decision.RequiresPrompt, "read 1 should require prompt")

	// Simulate "approve once" (ScopeOneTime) — should NOT persist
	err = mgr.GrantConsent(ctx, ConsentRule{
		Scope:      ScopeOneTime,
		Target:     NewToolTarget("copilot", "read"),
		Action:     ActionAny,
		Operation:  OperationTypeTool,
		Permission: PermissionAllow,
	})
	require.NoError(t, err)

	// Read 2: still no rules (one-time doesn't persist)
	decision, err = mgr.CheckConsent(ctx, readReq)
	require.NoError(t, err)
	require.False(t, decision.Allowed, "read 2 should not be auto-allowed after one-time")
	require.True(t, decision.RequiresPrompt, "read 2 should still require prompt")

	// Read 3: user selects "always" → global rule
	err = mgr.GrantConsent(ctx, ConsentRule{
		Scope:      ScopeGlobal,
		Target:     NewToolTarget("copilot", "read"),
		Action:     ActionAny,
		Operation:  OperationTypeTool,
		Permission: PermissionAllow,
	})
	require.NoError(t, err)

	// Read 4+: should auto-approve WITHOUT prompting
	decision, err = mgr.CheckConsent(ctx, readReq)
	require.NoError(t, err)
	require.True(t, decision.Allowed, "read 4 should be auto-allowed after 'always' grant")
	require.False(t, decision.RequiresPrompt, "read 4 should NOT require prompt")

	// Read 5: also auto-approve
	decision, err = mgr.CheckConsent(ctx, readReq)
	require.NoError(t, err)
	require.True(t, decision.Allowed, "read 5 should also be auto-allowed")
}

// TestConsentPersistence_AlwaysAllow_SurvivesReload verifies the rule persists across config reloads.
func TestConsentPersistence_AlwaysAllow_SurvivesReload(t *testing.T) {
	// Use a real file-backed config manager to test actual persistence
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

	readOnly := true
	readReq := ConsentRequest{
		ToolID:     "copilot/read",
		ServerName: "copilot",
		Operation:  OperationTypeTool,
		Annotations: mcp.ToolAnnotation{
			ReadOnlyHint: &readOnly,
		},
	}

	// Grant "always" → global rule persisted to file
	err := mgr.GrantConsent(ctx, ConsentRule{
		Scope:      ScopeGlobal,
		Target:     NewToolTarget("copilot", "read"),
		Action:     ActionAny,
		Operation:  OperationTypeTool,
		Permission: PermissionAllow,
	})
	require.NoError(t, err)

	// Create a FRESH consent manager (simulating a new session / reload)
	mgr2 := &consentManager{
		lazyEnvManager:    lazyEnvMgr,
		userConfigManager: userConfigMgr,
		sessionRules:      make([]ConsentRule, 0),
	}

	// Should still auto-approve from the persisted global rule
	decision, err := mgr2.CheckConsent(ctx, readReq)
	require.NoError(t, err)
	require.True(t, decision.Allowed, "rule should survive config reload")
	require.False(t, decision.RequiresPrompt)
}

// TestConsentPersistence_SessionScope_NotAfterReload verifies session rules don't survive reload.
func TestConsentPersistence_SessionScope_NotAfterReload(t *testing.T) {
	mgr := newTestConsentManager(t)
	ctx := context.Background()

	readOnly := true
	readReq := ConsentRequest{
		ToolID:     "copilot/read",
		ServerName: "copilot",
		Operation:  OperationTypeTool,
		Annotations: mcp.ToolAnnotation{
			ReadOnlyHint: &readOnly,
		},
	}

	// Grant "session" scope
	err := mgr.GrantConsent(ctx, ConsentRule{
		Scope:      ScopeSession,
		Target:     NewToolTarget("copilot", "read"),
		Action:     ActionAny,
		Operation:  OperationTypeTool,
		Permission: PermissionAllow,
	})
	require.NoError(t, err)

	// Should auto-approve in same manager
	decision, err := mgr.CheckConsent(ctx, readReq)
	require.NoError(t, err)
	require.True(t, decision.Allowed, "session rule should work within same session")

	// New manager → session rules gone
	mgr2 := newTestConsentManager(t)
	decision, err = mgr2.CheckConsent(ctx, readReq)
	require.NoError(t, err)
	require.False(t, decision.Allowed, "session rule should NOT survive new manager")
	require.True(t, decision.RequiresPrompt)
}

func newTestConsentManager(t *testing.T) ConsentManager {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)

	fileConfigMgr := config.NewFileConfigManager(config.NewManager())
	userConfigMgr := config.NewUserConfigManager(fileConfigMgr)

	// Provide a lazy env manager that always returns an error (no project scope)
	lazyEnvMgr := lazy.NewLazy[environment.Manager](func() (environment.Manager, error) {
		return nil, fmt.Errorf("no environment in test")
	})

	return &consentManager{
		lazyEnvManager:    lazyEnvMgr,
		userConfigManager: userConfigMgr,
		sessionRules:      make([]ConsentRule, 0),
	}
}
