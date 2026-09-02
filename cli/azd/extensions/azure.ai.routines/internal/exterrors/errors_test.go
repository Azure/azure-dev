// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── constructor helpers ──────────────────────────────────────────────────────

func TestValidation_Category(t *testing.T) {
	t.Parallel()
	err := Validation(CodeInvalidParameter, "bad input", "fix it")
	var le *azdext.LocalError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, azdext.LocalErrorCategoryValidation, le.Category)
	assert.Equal(t, CodeInvalidParameter, le.Code)
	assert.Equal(t, "bad input", le.Message)
	assert.Equal(t, "fix it", le.Suggestion)
}

func TestDependency_Category(t *testing.T) {
	t.Parallel()
	err := Dependency(CodeFileNotFound, "file missing", "check path")
	var le *azdext.LocalError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, azdext.LocalErrorCategoryDependency, le.Category)
	assert.Equal(t, CodeFileNotFound, le.Code)
}

func TestAuth_Category(t *testing.T) {
	t.Parallel()
	err := Auth(CodeAuthFailed, "not authenticated", "run azd auth login")
	var le *azdext.LocalError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, azdext.LocalErrorCategoryAuth, le.Category)
}

func TestInternal_Category(t *testing.T) {
	t.Parallel()
	err := Internal("some_op", "unexpected failure")
	var le *azdext.LocalError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, azdext.LocalErrorCategoryInternal, le.Category)
}

func TestCancelled_Category(t *testing.T) {
	t.Parallel()
	err := Cancelled("operation cancelled by user")
	var le *azdext.LocalError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, azdext.LocalErrorCategoryUser, le.Category)
	assert.Equal(t, CodeCancelled, le.Code)
}

// ─── ServiceFromAzure ─────────────────────────────────────────────────────────

func TestServiceFromAzure_ResponseError(t *testing.T) {
	t.Parallel()
	azErr := &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "RoutineNotFound"}
	err := ServiceFromAzure(azErr, OpGetRoutine)
	var svcErr *azdext.ServiceError
	require.ErrorAs(t, err, &svcErr)
	assert.Equal(t, http.StatusNotFound, svcErr.StatusCode)
	assert.Contains(t, svcErr.ErrorCode, OpGetRoutine)
	assert.Contains(t, svcErr.ErrorCode, "RoutineNotFound")
}

func TestServiceFromAzure_ResponseError_EmptyCode(t *testing.T) {
	t.Parallel()
	// When ErrorCode is empty the status code is used as the code suffix.
	azErr := &azcore.ResponseError{StatusCode: http.StatusInternalServerError}
	err := ServiceFromAzure(azErr, OpListRoutines)
	var svcErr *azdext.ServiceError
	require.ErrorAs(t, err, &svcErr)
	assert.Contains(t, svcErr.ErrorCode, "500")
}

func TestServiceFromAzure_Cancellation(t *testing.T) {
	t.Parallel()
	err := ServiceFromAzure(context.Canceled, OpDeleteRoutine)
	var le *azdext.LocalError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, azdext.LocalErrorCategoryUser, le.Category)
	assert.Equal(t, CodeCancelled, le.Code)
}

func TestServiceFromAzure_GenericError(t *testing.T) {
	t.Parallel()
	err := ServiceFromAzure(assert.AnError, OpCreateRoutine)
	var le *azdext.LocalError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, azdext.LocalErrorCategoryInternal, le.Category)
}

// ─── ServiceFromStatus ────────────────────────────────────────────────────────

func TestServiceFromStatus(t *testing.T) {
	t.Parallel()
	err := ServiceFromStatus(http.StatusNotFound, OpGetRoutine, "routine not found")
	var svcErr *azdext.ServiceError
	require.ErrorAs(t, err, &svcErr)
	assert.Equal(t, http.StatusNotFound, svcErr.StatusCode)
	assert.Contains(t, svcErr.ErrorCode, OpGetRoutine)
	assert.Contains(t, svcErr.Message, "routine not found")
}

// ─── IsNotFound / IsConflict ──────────────────────────────────────────────────

func TestIsNotFound_ResponseError(t *testing.T) {
	t.Parallel()
	assert.True(t, IsNotFound(&azcore.ResponseError{StatusCode: http.StatusNotFound}))
	assert.False(t, IsNotFound(&azcore.ResponseError{StatusCode: http.StatusOK}))
}

func TestIsNotFound_ServiceError(t *testing.T) {
	t.Parallel()
	assert.True(t, IsNotFound(&azdext.ServiceError{StatusCode: http.StatusNotFound}))
	assert.False(t, IsNotFound(&azdext.ServiceError{StatusCode: http.StatusConflict}))
}

func TestIsConflict_ResponseError(t *testing.T) {
	t.Parallel()
	assert.True(t, IsConflict(&azcore.ResponseError{StatusCode: http.StatusConflict}))
	assert.False(t, IsConflict(&azcore.ResponseError{StatusCode: http.StatusNotFound}))
}

// ─── IsCancellation ───────────────────────────────────────────────────────────

func TestIsCancellation(t *testing.T) {
	t.Parallel()
	assert.True(t, IsCancellation(context.Canceled))
	assert.False(t, IsCancellation(assert.AnError))
}

// ─── WrapAuthError ────────────────────────────────────────────────────────────

func TestWrapAuthError_401_NotLoggedIn(t *testing.T) {
	t.Parallel()
	azErr := &azcore.ResponseError{StatusCode: http.StatusUnauthorized, ErrorCode: "not logged in, run `azd auth login` to login"}
	err := WrapAuthError(azErr, OpGetRoutine)
	var le *azdext.LocalError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, azdext.LocalErrorCategoryAuth, le.Category)
	assert.Equal(t, CodeNotLoggedIn, le.Code)
}

func TestWrapAuthError_401_LoginExpired(t *testing.T) {
	t.Parallel()
	azErr := &azcore.ResponseError{StatusCode: http.StatusUnauthorized, ErrorCode: "AADSTS70043: token expired"}
	err := WrapAuthError(azErr, OpGetRoutine)
	var le *azdext.LocalError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, CodeLoginExpired, le.Code)
}

func TestWrapAuthError_NonAuth_DelegatesToServiceFromAzure(t *testing.T) {
	t.Parallel()
	azErr := &azcore.ResponseError{StatusCode: http.StatusForbidden}
	err := WrapAuthError(azErr, OpGetRoutine)
	var svcErr *azdext.ServiceError
	require.ErrorAs(t, err, &svcErr)
	assert.Equal(t, http.StatusForbidden, svcErr.StatusCode)
}
