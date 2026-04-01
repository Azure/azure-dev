// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestUxMiddleware_ErrAbortedByUser_SwallowsError(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())
	featureManager := &alpha.FeatureManager{}
	ux := NewUxMiddleware(&Options{}, mockContext.Console, featureManager)

	result, err := ux.Run(*mockContext.Context, func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, internal.ErrAbortedByUser
	})

	// Error should be swallowed (exit code 0)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestUxMiddleware_ErrAbortedByUser_ChildAction_PassesThrough(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())
	childCtx := WithChildAction(*mockContext.Context)
	featureManager := &alpha.FeatureManager{}
	ux := NewUxMiddleware(&Options{}, mockContext.Console, featureManager)

	result, err := ux.Run(childCtx, func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, internal.ErrAbortedByUser
	})

	// For child actions, error should pass through unchanged
	require.ErrorIs(t, err, internal.ErrAbortedByUser)
	require.Nil(t, result)
}

func TestUxMiddleware_OtherErrors_NotSwallowed(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())
	featureManager := &alpha.FeatureManager{}
	ux := NewUxMiddleware(&Options{}, mockContext.Console, featureManager)
	someErr := errors.New("deployment failed")

	_, err := ux.Run(*mockContext.Context, func(ctx context.Context) (*actions.ActionResult, error) {
		return nil, someErr
	})

	// Other errors should still be returned
	require.ErrorIs(t, err, someErr)
}

func TestUxMiddleware_Success_ShowsActionResult(t *testing.T) {
	t.Parallel()
	mockContext := mocks.NewMockContext(context.Background())
	featureManager := &alpha.FeatureManager{}
	ux := NewUxMiddleware(&Options{}, mockContext.Console, featureManager)

	actionResult := &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "All done!",
		},
	}

	result, err := ux.Run(*mockContext.Context, func(ctx context.Context) (*actions.ActionResult, error) {
		return actionResult, nil
	})

	require.NoError(t, err)
	require.Equal(t, actionResult, result)
}
