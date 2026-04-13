// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"testing"

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

	result, err := checkFn(context.Background(), valCtx)
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

	results, err := checkFn(context.Background(), valCtx)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, PreflightCheckError, results[0].Severity)
	require.Contains(t, results[0].Message, "missing role assignment permissions")
}
