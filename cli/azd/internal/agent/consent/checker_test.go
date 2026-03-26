// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package consent

import (
	"context"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

// mockConsentManager is a simple in-memory ConsentManager for testing.
type mockConsentManager struct {
	decisions map[string]*ConsentDecision
	granted   []ConsentRule
	err       error
}

func (m *mockConsentManager) CheckConsent(
	_ context.Context, req ConsentRequest,
) (*ConsentDecision, error) {
	if m.err != nil {
		return nil, m.err
	}
	if d, ok := m.decisions[req.ToolID]; ok {
		return d, nil
	}
	return &ConsentDecision{
		Allowed:        false,
		RequiresPrompt: true,
		Reason:         "no consent",
	}, nil
}

func (m *mockConsentManager) GrantConsent(
	_ context.Context, rule ConsentRule,
) error {
	if m.err != nil {
		return m.err
	}
	m.granted = append(m.granted, rule)
	return nil
}

func (m *mockConsentManager) ListConsentRules(
	_ context.Context, _ ...FilterOption,
) ([]ConsentRule, error) {
	return nil, nil
}

func (m *mockConsentManager) ClearConsentRules(
	_ context.Context, _ ...FilterOption,
) error {
	return nil
}

func (m *mockConsentManager) PromptWorkflowConsent(
	_ context.Context, _ []string,
) error {
	return nil
}

func (m *mockConsentManager) IsProjectScopeAvailable(
	_ context.Context,
) bool {
	return false
}

func TestNewConsentChecker(t *testing.T) {
	mgr := &mockConsentManager{}
	cc := NewConsentChecker(mgr, "test-server")
	require.NotNil(t, cc)
	require.Equal(t, "test-server", cc.serverName)
}

func TestCheckToolConsent(t *testing.T) {
	tests := []struct {
		name       string
		decisions  map[string]*ConsentDecision
		wantAllow  bool
		wantPrompt bool
	}{
		{
			name: "Allowed",
			decisions: map[string]*ConsentDecision{
				"srv/myTool": {Allowed: true, Reason: "allowed"},
			},
			wantAllow:  true,
			wantPrompt: false,
		},
		{
			name:       "NoConsent",
			decisions:  map[string]*ConsentDecision{},
			wantAllow:  false,
			wantPrompt: true,
		},
		{
			name: "Denied",
			decisions: map[string]*ConsentDecision{
				"srv/myTool": {
					Allowed: false,
					Reason:  "denied",
				},
			},
			wantAllow:  false,
			wantPrompt: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := &mockConsentManager{decisions: tt.decisions}
			cc := NewConsentChecker(mgr, "srv")

			decision, err := cc.CheckToolConsent(
				context.Background(),
				"myTool",
				"does stuff",
				mcp.ToolAnnotation{},
			)
			require.NoError(t, err)
			require.Equal(t, tt.wantAllow, decision.Allowed)
			require.Equal(t, tt.wantPrompt, decision.RequiresPrompt)
		})
	}
}

func TestCheckToolConsent_Error(t *testing.T) {
	mgr := &mockConsentManager{
		err: fmt.Errorf("storage failure"),
	}
	cc := NewConsentChecker(mgr, "srv")

	_, err := cc.CheckToolConsent(
		context.Background(),
		"tool",
		"desc",
		mcp.ToolAnnotation{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "storage failure")
}

func TestCheckSamplingConsent(t *testing.T) {
	mgr := &mockConsentManager{
		decisions: map[string]*ConsentDecision{
			"srv/sample": {Allowed: true, Reason: "ok"},
		},
	}
	cc := NewConsentChecker(mgr, "srv")

	decision, err := cc.CheckSamplingConsent(
		context.Background(), "sample",
	)
	require.NoError(t, err)
	require.True(t, decision.Allowed)
}

func TestCheckElicitationConsent(t *testing.T) {
	mgr := &mockConsentManager{
		decisions: map[string]*ConsentDecision{
			"srv/elicit": {Allowed: true, Reason: "ok"},
		},
	}
	cc := NewConsentChecker(mgr, "srv")

	decision, err := cc.CheckElicitationConsent(
		context.Background(), "elicit",
	)
	require.NoError(t, err)
	require.True(t, decision.Allowed)
}

func TestFormatToolDescriptionWithAnnotations(t *testing.T) {
	cc := &ConsentChecker{serverName: "test"}
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name        string
		desc        string
		annotations mcp.ToolAnnotation
		contains    []string
		notContains []string
	}{
		{
			name:        "EmptyDescGetsDefault",
			desc:        "",
			annotations: mcp.ToolAnnotation{},
			contains:    []string{"No description available"},
		},
		{
			name: "WithTitle",
			desc: "base desc",
			annotations: mcp.ToolAnnotation{
				Title: "My Tool",
			},
			contains: []string{
				"base desc",
				"Title: My Tool",
			},
		},
		{
			name: "ReadOnlyTrue",
			desc: "desc",
			annotations: mcp.ToolAnnotation{
				ReadOnlyHint: boolPtr(true),
			},
			contains: []string{"Read-only operation"},
		},
		{
			name: "ReadOnlyFalse",
			desc: "desc",
			annotations: mcp.ToolAnnotation{
				ReadOnlyHint: boolPtr(false),
			},
			contains: []string{"May modify data"},
		},
		{
			name: "DestructiveTrue",
			desc: "desc",
			annotations: mcp.ToolAnnotation{
				DestructiveHint: boolPtr(true),
			},
			contains: []string{
				"Potentially destructive",
			},
		},
		{
			name: "DestructiveFalse",
			desc: "desc",
			annotations: mcp.ToolAnnotation{
				DestructiveHint: boolPtr(false),
			},
			contains: []string{"Non-destructive"},
		},
		{
			name: "IdempotentTrue",
			desc: "desc",
			annotations: mcp.ToolAnnotation{
				IdempotentHint: boolPtr(true),
			},
			contains: []string{"safe to retry"},
		},
		{
			name: "IdempotentFalse",
			desc: "desc",
			annotations: mcp.ToolAnnotation{
				IdempotentHint: boolPtr(false),
			},
			contains: []string{"side effects on retry"},
		},
		{
			name: "OpenWorldTrue",
			desc: "desc",
			annotations: mcp.ToolAnnotation{
				OpenWorldHint: boolPtr(true),
			},
			contains: []string{"external resources"},
		},
		{
			name: "OpenWorldFalse",
			desc: "desc",
			annotations: mcp.ToolAnnotation{
				OpenWorldHint: boolPtr(false),
			},
			contains: []string{"local resources only"},
		},
		{
			name: "AllAnnotations",
			desc: "full desc",
			annotations: mcp.ToolAnnotation{
				Title:           "Full Tool",
				ReadOnlyHint:    boolPtr(true),
				DestructiveHint: boolPtr(false),
				IdempotentHint:  boolPtr(true),
				OpenWorldHint:   boolPtr(false),
			},
			contains: []string{
				"full desc",
				"Tool characteristics:",
				"Title: Full Tool",
				"Read-only operation",
				"Non-destructive",
				"safe to retry",
				"local resources only",
			},
		},
		{
			name:        "NoAnnotations",
			desc:        "plain",
			annotations: mcp.ToolAnnotation{},
			contains:    []string{"plain"},
			notContains: []string{"Tool characteristics:"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cc.formatToolDescriptionWithAnnotations(
				tt.desc, tt.annotations,
			)
			for _, s := range tt.contains {
				require.Contains(t, result, s)
			}
			for _, s := range tt.notContains {
				require.NotContains(t, result, s)
			}
		})
	}
}

func TestGrantConsentFromChoice(t *testing.T) {
	tests := []struct {
		name      string
		toolID    string
		choice    string
		operation OperationType
		wantScope Scope
		wantTgt   Target
		wantAct   ActionType
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "Once",
			toolID:    "srv/tool",
			choice:    "once",
			operation: OperationTypeTool,
			wantScope: ScopeOneTime,
			wantTgt:   NewToolTarget("srv", "tool"),
			wantAct:   ActionAny,
		},
		{
			name:      "Session",
			toolID:    "srv/tool",
			choice:    "session",
			operation: OperationTypeTool,
			wantScope: ScopeSession,
			wantTgt:   NewToolTarget("srv", "tool"),
			wantAct:   ActionAny,
		},
		{
			name:      "Project",
			toolID:    "srv/tool",
			choice:    "project",
			operation: OperationTypeSampling,
			wantScope: ScopeProject,
			wantTgt:   NewToolTarget("srv", "tool"),
			wantAct:   ActionAny,
		},
		{
			name:      "Always",
			toolID:    "srv/tool",
			choice:    "always",
			operation: OperationTypeTool,
			wantScope: ScopeGlobal,
			wantTgt:   NewToolTarget("srv", "tool"),
			wantAct:   ActionAny,
		},
		{
			name:      "Server",
			toolID:    "srv/tool",
			choice:    "server",
			operation: OperationTypeTool,
			wantScope: ScopeGlobal,
			wantTgt:   NewServerTarget("srv"),
			wantAct:   ActionAny,
		},
		{
			name:      "Global",
			toolID:    "srv/tool",
			choice:    "global",
			operation: OperationTypeElicitation,
			wantScope: ScopeGlobal,
			wantTgt:   NewGlobalTarget(),
			wantAct:   ActionAny,
		},
		{
			name:      "ReadOnlySession",
			toolID:    "srv/tool",
			choice:    "readonly_session",
			operation: OperationTypeTool,
			wantScope: ScopeSession,
			wantTgt:   NewGlobalTarget(),
			wantAct:   ActionReadOnly,
		},
		{
			name:      "ReadOnlyGlobal",
			toolID:    "srv/tool",
			choice:    "readonly_global",
			operation: OperationTypeTool,
			wantScope: ScopeGlobal,
			wantTgt:   NewGlobalTarget(),
			wantAct:   ActionReadOnly,
		},
		{
			name:      "ReadOnlySessionNonToolFails",
			toolID:    "srv/tool",
			choice:    "readonly_session",
			operation: OperationTypeSampling,
			wantErr:   true,
			errMsg:    "readonly session option only available",
		},
		{
			name:      "ReadOnlyGlobalNonToolFails",
			toolID:    "srv/tool",
			choice:    "readonly_global",
			operation: OperationTypeSampling,
			wantErr:   true,
			errMsg:    "readonly global option only available",
		},
		{
			name:      "UnknownChoice",
			toolID:    "srv/tool",
			choice:    "magic",
			operation: OperationTypeTool,
			wantErr:   true,
			errMsg:    "unknown consent choice",
		},
		{
			name:      "InvalidToolID",
			toolID:    "notool",
			choice:    "once",
			operation: OperationTypeTool,
			wantErr:   true,
			errMsg:    "invalid toolId format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := &mockConsentManager{}
			cc := NewConsentChecker(mgr, "srv")

			err := cc.grantConsentFromChoice(
				context.Background(),
				tt.toolID,
				tt.choice,
				tt.operation,
			)

			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)
				return
			}

			require.NoError(t, err)
			require.Len(t, mgr.granted, 1)
			rule := mgr.granted[0]
			require.Equal(t, tt.wantScope, rule.Scope)
			require.Equal(t, tt.wantTgt, rule.Target)
			require.Equal(t, tt.wantAct, rule.Action)
			require.Equal(t, tt.operation, rule.Operation)
			require.Equal(t, PermissionAllow, rule.Permission)
		})
	}
}

func TestIsServerAlreadyTrusted(t *testing.T) {
	t.Run("Trusted", func(t *testing.T) {
		mgr := &mockConsentManager{
			decisions: map[string]*ConsentDecision{
				"srv/test-tool": {Allowed: true},
			},
		}
		cc := NewConsentChecker(mgr, "srv")
		require.True(
			t, cc.isServerAlreadyTrusted(
				context.Background(), OperationTypeTool,
			),
		)
	})

	t.Run("NotTrusted", func(t *testing.T) {
		mgr := &mockConsentManager{
			decisions: map[string]*ConsentDecision{},
		}
		cc := NewConsentChecker(mgr, "srv")
		require.False(
			t, cc.isServerAlreadyTrusted(
				context.Background(), OperationTypeTool,
			),
		)
	})

	t.Run("ErrorReturnsFalse", func(t *testing.T) {
		mgr := &mockConsentManager{
			err: fmt.Errorf("boom"),
		}
		cc := NewConsentChecker(mgr, "srv")
		require.False(
			t, cc.isServerAlreadyTrusted(
				context.Background(), OperationTypeSampling,
			),
		)
	})
}

func TestConsentManagerRuleMatchesFilters(t *testing.T) {
	mgr := newTestConsentManager(t)
	cm := mgr.(*consentManager)
	ctx := context.Background()

	// Grant several rules of different types
	rules := []ConsentRule{
		{
			Scope:      ScopeSession,
			Target:     NewToolTarget("srv", "read"),
			Action:     ActionReadOnly,
			Operation:  OperationTypeTool,
			Permission: PermissionAllow,
		},
		{
			Scope:      ScopeGlobal,
			Target:     NewToolTarget("srv", "write"),
			Action:     ActionAny,
			Operation:  OperationTypeTool,
			Permission: PermissionAllow,
		},
		{
			Scope:      ScopeSession,
			Target:     NewToolTarget("srv", "sample"),
			Action:     ActionAny,
			Operation:  OperationTypeSampling,
			Permission: PermissionAllow,
		},
	}
	for _, r := range rules {
		require.NoError(t, mgr.GrantConsent(ctx, r))
	}

	t.Run("FilterByScope", func(t *testing.T) {
		listed, err := mgr.ListConsentRules(
			ctx, WithScope(ScopeSession),
		)
		require.NoError(t, err)
		require.Len(t, listed, 2)
		for _, r := range listed {
			require.Equal(t, ScopeSession, r.Scope)
		}
	})

	t.Run("FilterByOperation", func(t *testing.T) {
		listed, err := mgr.ListConsentRules(
			ctx, WithOperation(OperationTypeSampling),
		)
		require.NoError(t, err)
		require.Len(t, listed, 1)
		require.Equal(
			t, OperationTypeSampling, listed[0].Operation,
		)
	})

	t.Run("FilterByAction", func(t *testing.T) {
		listed, err := mgr.ListConsentRules(
			ctx, WithAction(ActionReadOnly),
		)
		require.NoError(t, err)
		require.Len(t, listed, 1)
		require.Equal(t, ActionReadOnly, listed[0].Action)
	})

	t.Run("FilterByTarget", func(t *testing.T) {
		tgt := NewToolTarget("srv", "write")
		listed, err := mgr.ListConsentRules(
			ctx, WithTarget(tgt),
		)
		require.NoError(t, err)
		require.Len(t, listed, 1)
		require.Equal(t, tgt, listed[0].Target)
	})

	t.Run("FilterByPermission", func(t *testing.T) {
		listed, err := mgr.ListConsentRules(
			ctx, WithPermission(PermissionAllow),
		)
		require.NoError(t, err)
		require.Len(t, listed, 3)
	})

	t.Run("CombinedFilters", func(t *testing.T) {
		listed, err := mgr.ListConsentRules(
			ctx,
			WithScope(ScopeSession),
			WithOperation(OperationTypeTool),
		)
		require.NoError(t, err)
		require.Len(t, listed, 1)
		require.Equal(
			t,
			NewToolTarget("srv", "read"),
			listed[0].Target,
		)
	})

	t.Run("NoMatchReturnsEmpty", func(t *testing.T) {
		listed, err := mgr.ListConsentRules(
			ctx, WithPermission(PermissionDeny),
		)
		require.NoError(t, err)
		require.Empty(t, listed)
	})

	t.Run("EvaluateRule", func(t *testing.T) {
		d := cm.evaluateRule(ConsentRule{
			Permission: PermissionAllow,
		})
		require.True(t, d.Allowed)

		d = cm.evaluateRule(ConsentRule{
			Permission: PermissionDeny,
		})
		require.False(t, d.Allowed)
		require.False(t, d.RequiresPrompt)

		d = cm.evaluateRule(ConsentRule{
			Permission: PermissionPrompt,
		})
		require.False(t, d.Allowed)
		require.True(t, d.RequiresPrompt)

		d = cm.evaluateRule(ConsentRule{
			Permission: Permission("unknown"),
		})
		require.False(t, d.Allowed)
		require.True(t, d.RequiresPrompt)
	})
}

func TestActionMatches(t *testing.T) {
	mgr := newTestConsentManager(t)
	cm := mgr.(*consentManager)

	tests := []struct {
		name       string
		ruleAction ActionType
		readOnly   bool
		want       bool
	}{
		{"AnyMatchesReadOnly", ActionAny, true, true},
		{"AnyMatchesNonReadOnly", ActionAny, false, true},
		{"ReadOnlyMatchesReadOnly", ActionReadOnly, true, true},
		{"ReadOnlyRejectsNonReadOnly", ActionReadOnly, false, false},
		{"UnknownRejects", ActionType("x"), true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cm.actionMatches(tt.ruleAction, tt.readOnly)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTargetMatches(t *testing.T) {
	mgr := newTestConsentManager(t)
	cm := mgr.(*consentManager)

	tests := []struct {
		name    string
		rule    Target
		request Target
		want    bool
	}{
		{
			"GlobalStar",
			Target("*"), Target("srv/tool"), true,
		},
		{
			"GlobalStarSlashStar",
			Target("*/*"), Target("srv/tool"), true,
		},
		{
			"ServerWildcard",
			Target("srv/*"), Target("srv/tool"), true,
		},
		{
			"ServerWildcardNoMatch",
			Target("other/*"), Target("srv/tool"), false,
		},
		{
			"ExactMatch",
			Target("srv/tool"), Target("srv/tool"), true,
		},
		{
			"ExactNoMatch",
			Target("srv/tool"), Target("srv/other"), false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cm.targetMatches(tt.rule, tt.request)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestClearConsentRules(t *testing.T) {
	mgr := newTestConsentManager(t)
	ctx := context.Background()

	// Grant rules in session scope
	require.NoError(t, mgr.GrantConsent(ctx, ConsentRule{
		Scope:      ScopeSession,
		Target:     NewToolTarget("srv", "a"),
		Action:     ActionAny,
		Operation:  OperationTypeTool,
		Permission: PermissionAllow,
	}))
	require.NoError(t, mgr.GrantConsent(ctx, ConsentRule{
		Scope:      ScopeSession,
		Target:     NewToolTarget("srv", "b"),
		Action:     ActionAny,
		Operation:  OperationTypeTool,
		Permission: PermissionAllow,
	}))

	// Verify we have 2 rules
	all, err := mgr.ListConsentRules(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)

	// Clear all session rules
	require.NoError(t, mgr.ClearConsentRules(
		ctx, WithScope(ScopeSession),
	))

	// Verify empty
	all, err = mgr.ListConsentRules(ctx)
	require.NoError(t, err)
	require.Empty(t, all)
}

func TestGrantConsent_InvalidRule(t *testing.T) {
	mgr := newTestConsentManager(t)
	err := mgr.GrantConsent(context.Background(), ConsentRule{
		Scope:      ScopeGlobal,
		Target:     Target(""), // invalid
		Action:     ActionAny,
		Operation:  OperationTypeTool,
		Permission: PermissionAllow,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid consent rule")
}

func TestGrantConsent_UnknownScope(t *testing.T) {
	mgr := newTestConsentManager(t)
	err := mgr.GrantConsent(context.Background(), ConsentRule{
		Scope:      Scope("unknown"),
		Target:     NewToolTarget("s", "t"),
		Action:     ActionAny,
		Operation:  OperationTypeTool,
		Permission: PermissionAllow,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid consent rule")
}

func TestAddOrUpdateRule(t *testing.T) {
	mgr := newTestConsentManager(t)
	cm := mgr.(*consentManager)

	rules := []ConsentRule{}
	r1 := ConsentRule{
		Target:     NewToolTarget("s", "t"),
		Operation:  OperationTypeTool,
		Action:     ActionAny,
		Permission: PermissionAllow,
	}

	// Add new rule
	rules = cm.addOrUpdateRule(rules, r1)
	require.Len(t, rules, 1)

	// Update existing rule
	r2 := r1
	r2.Permission = PermissionDeny
	rules = cm.addOrUpdateRule(rules, r2)
	require.Len(t, rules, 1)
	require.Equal(t, PermissionDeny, rules[0].Permission)

	// Add different target
	r3 := ConsentRule{
		Target:     NewToolTarget("s", "other"),
		Operation:  OperationTypeTool,
		Action:     ActionAny,
		Permission: PermissionAllow,
	}
	rules = cm.addOrUpdateRule(rules, r3)
	require.Len(t, rules, 2)
}
