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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

func TestProjectAuthoring_PreservesPartialSuccessContext(t *testing.T) {
	t.Parallel()

	err := ProjectAuthoring(
		"routine \"nightly-summary\" was created but could not be added to azure.yaml",
		"run `azd ai routine update nightly-summary --add-to-project`",
		assert.AnError,
	)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, azdext.LocalErrorCategoryInternal, localErr.Category)
	assert.Equal(t, CodeProjectAuthoringFailed, localErr.Code)
	assert.Contains(t, localErr.Message, "was created")
	assert.Contains(t, localErr.Message, assert.AnError.Error())
	assert.Contains(t, localErr.Suggestion, "routine update nightly-summary --add-to-project")
}

func TestProjectAuthoring_PreservesLocalErrorClassification(t *testing.T) {
	t.Parallel()

	cause := Validation(CodeInvalidRoutineManifest, "invalid $ref", "fix the reference")
	err := ProjectAuthoring("remote operation succeeded", "retry update", cause)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, CodeInvalidRoutineManifest, localErr.Code)
	assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
	assert.Contains(t, localErr.Message, "remote operation succeeded")
	assert.Contains(t, localErr.Message, "invalid $ref")
	assert.Contains(t, localErr.Suggestion, "fix the reference")
	assert.Contains(t, localErr.Suggestion, "retry update")
}

func TestProjectAuthoring_PreservesHostActionableError(t *testing.T) {
	t.Parallel()

	statusWithDetails, err := status.New(codes.FailedPrecondition, "invalid project configuration").WithDetails(
		&azdext.ActionableErrorDetail{
			Suggestion: "fix the condition",
			Links: []*azdext.ErrorLink{
				{Url: "https://example.com/help", Title: "Project configuration help"},
			},
		},
	)
	require.NoError(t, err)

	wrapped := ProjectAuthoring("remote operation succeeded", "retry update", statusWithDetails.Err())
	var localErr *azdext.LocalError
	require.ErrorAs(t, wrapped, &localErr)
	assert.Contains(t, localErr.Message, "invalid project configuration")
	assert.Contains(t, localErr.Suggestion, "fix the condition")
	assert.Contains(t, localErr.Suggestion, "retry update")
	require.Len(t, localErr.Links, 1)
	assert.Equal(t, "https://example.com/help", localErr.Links[0].URL)
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
	azErr := &azcore.ResponseError{
		StatusCode: http.StatusUnauthorized,
		ErrorCode:  "not logged in, run `azd auth login` to login",
	}
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
