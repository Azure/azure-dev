// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProvisionValidationCheckFn_SkipsWhenNoRoleAssignments(t *testing.T) {
	called := false
	checkFn := ProvisionValidationCheckFn(func(
		ctx context.Context,
		valCtx *validationContext,
	) ([]ProvisionValidationCheckResult, error) {
		called = true
		if !valCtx.Props.HasRoleAssignments {
			return nil, nil
		}
		return []ProvisionValidationCheckResult{{
			Severity: ProvisionValidationCheckError,
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

func TestProvisionValidationCheckFn_ReportsErrorWhenRoleAssignments(t *testing.T) {
	checkFn := ProvisionValidationCheckFn(func(
		ctx context.Context,
		valCtx *validationContext,
	) ([]ProvisionValidationCheckResult, error) {
		if !valCtx.Props.HasRoleAssignments {
			return nil, nil
		}
		return []ProvisionValidationCheckResult{{
			Severity: ProvisionValidationCheckError,
			Message:  "missing role assignment permissions",
		}}, nil
	})

	valCtx := &validationContext{
		Props: resourcesProperties{HasRoleAssignments: true},
	}

	results, err := checkFn(t.Context(), valCtx)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, ProvisionValidationCheckError, results[0].Severity)
	require.Contains(t, results[0].Message, "missing role assignment permissions")
}
