// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPreservesStructuredErrors(t *testing.T) {
	hostError, err := status.New(codes.FailedPrecondition, "host message").WithDetails(
		&azdext.ActionableErrorDetail{Suggestion: "host suggestion"},
	)
	require.NoError(t, err)
	for _, original := range []error{
		&azdext.LocalError{
			Code: "original_code", Category: azdext.LocalErrorCategoryValidation,
			Message: "original message", Suggestion: "original suggestion",
		},
		&azdext.ServiceError{
			ErrorCode: "Conflict", StatusCode: 409, ServiceName: "example.test",
			Message: "service message", Suggestion: "service suggestion",
		},
		&azdext.ToolError{ToolName: "azd", Kind: azdext.ToolErrorKindMissing, Message: "tool message"},
		hostError.Err(),
	} {
		t.Run(fmt.Sprintf("%T", original), func(t *testing.T) {
			wrapped := fmt.Errorf("operation: %w", original)
			assert.Same(t, wrapped, FromError(wrapped))
			assert.Equal(t, azdext.WrapError(wrapped), azdext.WrapError(FromError(wrapped)))
		})
	}
	assert.Nil(t, FromError(nil))
}

func TestErrorCategories(t *testing.T) {
	for _, tt := range []struct {
		name     string
		err      error
		code     string
		category azdext.LocalErrorCategory
	}{
		{"cancel", context.Canceled, CodeCancelled, azdext.LocalErrorCategoryUser},
		{"grpc cancel", status.Error(codes.Canceled, "private"), CodeCancelled, azdext.LocalErrorCategoryUser},
		{"timeout", context.DeadlineExceeded, CodeDeadlineExceeded, azdext.LocalErrorCategoryInternal},
		{"grpc timeout", status.Error(codes.DeadlineExceeded, "private"),
			CodeDeadlineExceeded, azdext.LocalErrorCategoryInternal},
		{"auth", status.Error(codes.Unauthenticated, "private"),
			CodeAuthenticationFailed, azdext.LocalErrorCategoryAuth},
		{"forbidden", status.Error(codes.PermissionDenied, "private"),
			CodeAuthenticationFailed, azdext.LocalErrorCategoryAuth},
		{"unavailable", status.Error(codes.Unavailable, "private"),
			CodeHostUnavailable, azdext.LocalErrorCategoryDependency},
		{"unknown", errors.New("private"), CodeUnexpected, azdext.LocalErrorCategoryInternal},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := FromError(fmt.Errorf("operation: %w", tt.err))
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, tt.code, local.Code)
			assert.Equal(t, tt.category, local.Category)
			assert.NotContains(t, local.Error(), "private")
			if errors.Is(tt.err, context.Canceled) {
				assert.ErrorIs(t, err, context.Canceled)
			}
			if errors.Is(tt.err, context.DeadlineExceeded) {
				assert.ErrorIs(t, err, context.DeadlineExceeded)
			}
		})
	}
}

func TestAzureErrorDoesNotExposeResponseBody(t *testing.T) {
	original := &azcore.ResponseError{
		StatusCode: http.StatusForbidden,
		ErrorCode:  "Forbidden",
		RawResponse: &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{"Authorization": []string{"Bearer private-token"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"private-body-detail"}}`)),
		},
	}
	converted, ok := errors.AsType[*azdext.ServiceError](FromError(original))
	require.True(t, ok)
	assert.Equal(t, "Forbidden", converted.ErrorCode)
	assert.Equal(t, http.StatusForbidden, converted.StatusCode)
	assert.NotContains(t, converted.Error(), "private-token")
	assert.NotContains(t, converted.Error(), "private-body-detail")
}

func TestHostUnavailable(t *testing.T) {
	local, ok := errors.AsType[*azdext.LocalError](HostUnavailable())
	require.True(t, ok)
	assert.Equal(t, CodeHostUnavailable, local.Code)
	assert.Equal(t, azdext.LocalErrorCategoryDependency, local.Category)
	assert.Contains(t, local.Suggestion, "azd ai cu")
	assert.False(t, strings.Contains(local.Message, "AZD_ACCESS_TOKEN"))
}

func TestMissingTenant(t *testing.T) {
	local, ok := errors.AsType[*azdext.LocalError](MissingTenant())
	require.True(t, ok)
	assert.Equal(t, CodeMissingTenant, local.Code)
	assert.Equal(t, azdext.LocalErrorCategoryDependency, local.Category)
	assert.Contains(t, local.Suggestion, "azd environment")
}
