// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/stretchr/testify/require"
)

func TestPreflightCheckFn_SkipsWhenNoRoleAssignments(t *testing.T) {
	called := false
	checkFn := PreflightCheckFn(func(
		ctx context.Context,
		valCtx *validationContext,
	) ([]PreflightCheckResult, error) {
		called = true
		if !valCtx.Props.HasRoleAssignments {
			return nil, nil
		}
		return []PreflightCheckResult{{
			Severity: PreflightCheckError,
			Message:  "missing permissions",
		}}, nil
	})

	valCtx := &validationContext{
		Props: resourcesProperties{HasRoleAssignments: false},
	}

	result, err := checkFn(t.Context(), valCtx)
	require.NoError(t, err)
	require.True(t, called)
	require.Nil(t, result)
}

func TestPreflightCheckFn_ReportsErrorWhenRoleAssignments(t *testing.T) {
	checkFn := PreflightCheckFn(func(
		ctx context.Context,
		valCtx *validationContext,
	) ([]PreflightCheckResult, error) {
		if !valCtx.Props.HasRoleAssignments {
			return nil, nil
		}
		return []PreflightCheckResult{{
			Severity: PreflightCheckError,
			Message:  "missing role assignment permissions",
		}}, nil
	})

	valCtx := &validationContext{
		Props: resourcesProperties{HasRoleAssignments: true},
	}

	results, err := checkFn(t.Context(), valCtx)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, PreflightCheckError, results[0].Severity)
	require.Contains(t, results[0].Message, "missing role assignment permissions")
}

func TestRoleAssignmentUnverifiedWarning(t *testing.T) {
	cause := errors.New("graph API timeout")
	results := roleAssignmentUnverifiedWarning("sub-123", cause)

	require.Len(t, results, 1)
	require.Equal(t, PreflightCheckWarning, results[0].Severity)
	require.Equal(t, "role_assignment_unverified", results[0].DiagnosticID)
	require.Contains(t, results[0].Message, "graph API timeout")
	require.Contains(t, results[0].Message, "sub-123")
	require.Contains(t, results[0].Message, "roleAssignments/write")
}

func TestCheckRoleAssignmentPermissions_NoRoleAssignments(t *testing.T) {
	p := &BicepProvider{}
	valCtx := &validationContext{
		Props: resourcesProperties{HasRoleAssignments: false},
	}

	results, err := p.checkRoleAssignmentPermissions(t.Context(), valCtx)
	require.NoError(t, err)
	require.Nil(t, results)
}

func TestCheckRoleAssignmentPermissions_DIFailure(t *testing.T) {
	// An empty IoC container with no PermissionsService registered should produce
	// an "unverified" warning, not silently skip the check.
	container := ioc.NewNestedContainer(nil)
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "sub-456",
	})

	p := &BicepProvider{
		env:            env,
		serviceLocator: container,
	}

	valCtx := &validationContext{
		Props: resourcesProperties{HasRoleAssignments: true},
	}

	results, err := p.checkRoleAssignmentPermissions(t.Context(), valCtx)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, PreflightCheckWarning, results[0].Severity)
	require.Equal(t, "role_assignment_unverified", results[0].DiagnosticID)
	require.Contains(t, results[0].Message, "sub-456")
}

func TestWarningsDuplicateArmErrors(t *testing.T) {
	tests := []struct {
		name    string
		results []PreflightCheckResult
		want    bool
	}{
		{
			name:    "empty results → false (nothing to skip for)",
			results: nil,
			want:    false,
		},
		{
			name: "only role_assignment_missing → true",
			results: []PreflightCheckResult{
				{Severity: PreflightCheckWarning, DiagnosticID: "role_assignment_missing"},
			},
			want: true,
		},
		{
			name: "only role_assignment_unverified → true",
			results: []PreflightCheckResult{
				{Severity: PreflightCheckWarning, DiagnosticID: "role_assignment_unverified"},
			},
			want: true,
		},
		{
			name: "only role_assignment_conditional → true",
			results: []PreflightCheckResult{
				{Severity: PreflightCheckWarning, DiagnosticID: "role_assignment_conditional"},
			},
			want: true,
		},
		{
			name: "mixed role assignment diagnostics → true",
			results: []PreflightCheckResult{
				{Severity: PreflightCheckWarning, DiagnosticID: "role_assignment_missing"},
				{Severity: PreflightCheckWarning, DiagnosticID: "role_assignment_conditional"},
			},
			want: true,
		},
		{
			name: "ai_model_quota only → false (ARM validation should still run)",
			results: []PreflightCheckResult{
				{Severity: PreflightCheckWarning, DiagnosticID: "ai_model_quota_exceeded"},
			},
			want: false,
		},
		{
			name: "role assignment + ai quota → false (quota isn't ARM-duplicated)",
			results: []PreflightCheckResult{
				{Severity: PreflightCheckWarning, DiagnosticID: "role_assignment_missing"},
				{Severity: PreflightCheckWarning, DiagnosticID: "ai_model_quota_exceeded"},
			},
			want: false,
		},
		{
			name: "errors are ignored (only warnings counted)",
			results: []PreflightCheckResult{
				{Severity: PreflightCheckError, DiagnosticID: "something_else"},
				{Severity: PreflightCheckWarning, DiagnosticID: "role_assignment_missing"},
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := warningsDuplicateArmErrors(tc.results)
			require.Equal(t, tc.want, got)
		})
	}
}
