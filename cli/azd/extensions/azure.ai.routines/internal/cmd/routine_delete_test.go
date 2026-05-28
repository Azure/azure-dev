// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"azure.ai.routines/internal/exterrors"
	"azure.ai.routines/internal/pkg/routines"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRoutineDeleter is a stub implementation of [routineDeleter] used to
// drive deleteRoutineWithExistenceCheck in unit tests. It records every call
// the SUT makes so the tests can assert ordering ("did we GET before DELETE?")
// and the specific routine name passed through.
type fakeRoutineDeleter struct {
	getRoutine    func(ctx context.Context, name string) (*routines.Routine, error)
	deleteRoutine func(ctx context.Context, name string) error

	getCalls    []string
	deleteCalls []string
}

func (f *fakeRoutineDeleter) GetRoutine(
	ctx context.Context, name string,
) (*routines.Routine, error) {
	f.getCalls = append(f.getCalls, name)
	if f.getRoutine == nil {
		return &routines.Routine{Name: name}, nil
	}
	return f.getRoutine(ctx, name)
}

func (f *fakeRoutineDeleter) DeleteRoutine(ctx context.Context, name string) error {
	f.deleteCalls = append(f.deleteCalls, name)
	if f.deleteRoutine == nil {
		return nil
	}
	return f.deleteRoutine(ctx, name)
}

// notFoundResponseError builds an azcore.ResponseError shaped like a real 404
// from the Foundry data plane. exterrors.IsNotFound unwraps it via errors.As.
func notFoundResponseError() error {
	return &azcore.ResponseError{
		ErrorCode:  "NotFound",
		StatusCode: 404,
	}
}

// TestDeleteRoutineWithExistenceCheck_HappyPath verifies the typical flow
// where the routine exists: GET succeeds, then DELETE is issued.
func TestDeleteRoutineWithExistenceCheck_HappyPath(t *testing.T) {
	t.Parallel()
	f := &fakeRoutineDeleter{}
	err := deleteRoutineWithExistenceCheck(context.Background(), f, "my-routine", "table")
	require.NoError(t, err)
	assert.Equal(t, []string{"my-routine"}, f.getCalls,
		"GET should be issued before DELETE to check existence")
	assert.Equal(t, []string{"my-routine"}, f.deleteCalls)
}

// TestDeleteRoutineWithExistenceCheck_NotFoundSurfacedAs404 is the core
// regression guard for issue #8421 Bug 7: deleting a routine that doesn't
// exist must surface the same shape of "not found" error as `routine show`
// and `routine dispatch`, instead of silently printing "deleted".
func TestDeleteRoutineWithExistenceCheck_NotFoundSurfacedAs404(t *testing.T) {
	t.Parallel()
	f := &fakeRoutineDeleter{
		getRoutine: func(_ context.Context, _ string) (*routines.Routine, error) {
			return nil, notFoundResponseError()
		},
	}
	err := deleteRoutineWithExistenceCheck(
		context.Background(), f, "does-not-exist", "table")
	require.Error(t, err)

	var svcErr *azdext.ServiceError
	require.True(t, errors.As(err, &svcErr),
		"error must be a structured azdext.ServiceError so callers can branch on it")
	assert.Equal(t, 404, svcErr.StatusCode,
		"status code must reflect 'not found' so the rest of the verb surface stays consistent")
	assert.Contains(t, svcErr.Message, "does-not-exist",
		"error message must echo the routine name the user asked to delete")
	assert.True(t, exterrors.IsNotFound(err),
		"IsNotFound must continue to recognize the resulting error")

	assert.Empty(t, f.deleteCalls,
		"DELETE must NOT be issued once the existence check fails")
}

// TestDeleteRoutineWithExistenceCheck_GetTransientError ensures transient
// errors from the existence GET (e.g. 5xx, network) propagate untouched
// rather than being silently treated as "not found".
func TestDeleteRoutineWithExistenceCheck_GetTransientError(t *testing.T) {
	t.Parallel()
	f := &fakeRoutineDeleter{
		getRoutine: func(_ context.Context, _ string) (*routines.Routine, error) {
			return nil, &azcore.ResponseError{
				ErrorCode:  "InternalError",
				StatusCode: 500,
			}
		},
	}
	err := deleteRoutineWithExistenceCheck(context.Background(), f, "r", "table")
	require.Error(t, err)
	assert.False(t, exterrors.IsNotFound(err),
		"a 5xx during the existence GET must not be mistaken for not-found")
	assert.Empty(t, f.deleteCalls)
}

// TestDeleteRoutineWithExistenceCheck_DeleteRaceLost handles the TOCTOU race
// where the routine was deleted between the existence GET and the DELETE: if
// DELETE returns 404, the verb still surfaces a structured 404 (matching the
// behavior when the routine did not exist at GET time).
func TestDeleteRoutineWithExistenceCheck_DeleteRaceLost(t *testing.T) {
	t.Parallel()
	f := &fakeRoutineDeleter{
		deleteRoutine: func(_ context.Context, _ string) error {
			return notFoundResponseError()
		},
	}
	err := deleteRoutineWithExistenceCheck(context.Background(), f, "r", "table")
	require.Error(t, err)
	assert.True(t, exterrors.IsNotFound(err))
}
