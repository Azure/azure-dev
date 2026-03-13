// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFromAiService(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		fallbackCode string
		wantCategory azdext.LocalErrorCategory
		wantCode     string
	}{
		{
			name:         "Unauthenticated returns Auth with not_logged_in",
			err:          status.Error(codes.Unauthenticated, "not logged in, run `azd auth login` to login"),
			fallbackCode: "model_catalog_failed",
			wantCategory: azdext.LocalErrorCategoryAuth,
			wantCode:     CodeNotLoggedIn,
		},
		{
			name:         "Unauthenticated returns Auth with login_expired",
			err:          status.Error(codes.Unauthenticated, "AADSTS70043: token expired\nlogin expired, run `azd auth login`"),
			fallbackCode: "model_catalog_failed",
			wantCategory: azdext.LocalErrorCategoryAuth,
			wantCode:     CodeLoginExpired,
		},
		{
			name:         "Unauthenticated returns Auth with generic auth_failed",
			err:          status.Error(codes.Unauthenticated, "insufficient permissions for this operation"),
			fallbackCode: "model_catalog_failed",
			wantCategory: azdext.LocalErrorCategoryAuth,
			wantCode:     CodeAuthFailed,
		},
		{
			name:         "Other gRPC error returns Internal",
			err:          status.Error(codes.InvalidArgument, "missing subscription"),
			fallbackCode: "model_catalog_failed",
			wantCategory: azdext.LocalErrorCategoryInternal,
			wantCode:     "model_catalog_failed",
		},
		{
			name:         "Canceled returns User cancellation",
			err:          status.Error(codes.Canceled, "cancelled"),
			fallbackCode: "model_catalog_failed",
			wantCategory: azdext.LocalErrorCategoryUser,
			wantCode:     CodeCancelled,
		},
		{
			name:         "Nil returns nil",
			err:          nil,
			fallbackCode: "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FromAiService(tt.err, tt.fallbackCode)
			if tt.err == nil {
				assert.Nil(t, result)
				return
			}

			var localErr *azdext.LocalError
			require.ErrorAs(t, result, &localErr)
			assert.Equal(t, tt.wantCategory, localErr.Category)
			assert.Equal(t, tt.wantCode, localErr.Code)
		})
	}
}

func TestFromPrompt(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		contextMsg   string
		wantCategory azdext.LocalErrorCategory
		wantCode     string
		wantContain  string
	}{
		{
			name:         "Auth error returns structured Auth error with context",
			err:          status.Error(codes.Unauthenticated, "not logged in, run `azd auth login` to login"),
			contextMsg:   "failed to prompt for subscription",
			wantCategory: azdext.LocalErrorCategoryAuth,
			wantCode:     CodeNotLoggedIn,
			wantContain:  "failed to prompt for subscription",
		},
		{
			name:         "Login expired returns structured Auth error with context",
			err:          status.Error(codes.Unauthenticated, "AADSTS70043: token expired"),
			contextMsg:   "failed to prompt for location",
			wantCategory: azdext.LocalErrorCategoryAuth,
			wantCode:     CodeLoginExpired,
			wantContain:  "failed to prompt for location",
		},
		{
			name:         "Cancellation returns User error",
			err:          context.Canceled,
			contextMsg:   "subscription selection was cancelled",
			wantCategory: azdext.LocalErrorCategoryUser,
			wantCode:     CodeCancelled,
		},
		{
			name:        "Non-auth error returns wrapped error",
			err:         status.Error(codes.Internal, "server error"),
			contextMsg:  "failed to prompt for subscription",
			wantContain: "failed to prompt for subscription",
		},
		{
			name: "Nil returns nil",
			err:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FromPrompt(tt.err, tt.contextMsg)
			if tt.err == nil {
				assert.Nil(t, result)
				return
			}

			if tt.wantCategory != "" {
				var localErr *azdext.LocalError
				require.ErrorAs(t, result, &localErr)
				assert.Equal(t, tt.wantCategory, localErr.Category)
				assert.Equal(t, tt.wantCode, localErr.Code)
			}
			if tt.wantContain != "" {
				assert.Contains(t, result.Error(), tt.wantContain)
			}
		})
	}
}

func TestWrapVariants_PreserveCause(t *testing.T) {
	cause := io.ErrUnexpectedEOF

	t.Run("ValidationWrap preserves cause", func(t *testing.T) {
		err := ValidationWrap(cause, "bad_input", "invalid", "fix it")
		var localErr *azdext.LocalError
		require.ErrorAs(t, err, &localErr)
		assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
		assert.Equal(t, "bad_input", localErr.Code)
		assert.Equal(t, "fix it", localErr.Suggestion)
		assert.True(t, errors.Is(err, io.ErrUnexpectedEOF), "cause should be reachable via errors.Is")
	})

	t.Run("DependencyWrap preserves cause", func(t *testing.T) {
		err := DependencyWrap(cause, "missing", "not found", "create it")
		assert.True(t, errors.Is(err, io.ErrUnexpectedEOF))
	})

	t.Run("CompatibilityWrap preserves cause", func(t *testing.T) {
		err := CompatibilityWrap(cause, "old_version", "too old", "upgrade")
		assert.True(t, errors.Is(err, io.ErrUnexpectedEOF))
	})

	t.Run("AuthWrap preserves cause", func(t *testing.T) {
		err := AuthWrap(cause, "auth_fail", "not authed", "login")
		assert.True(t, errors.Is(err, io.ErrUnexpectedEOF))
	})

	t.Run("ConfigurationWrap preserves cause", func(t *testing.T) {
		err := ConfigurationWrap(cause, "bad_cfg", "config error", "fix config")
		assert.True(t, errors.Is(err, io.ErrUnexpectedEOF))
	})

	t.Run("InternalWrap preserves cause", func(t *testing.T) {
		err := InternalWrap(cause, "internal_fail", "unexpected")
		assert.True(t, errors.Is(err, io.ErrUnexpectedEOF))
	})
}

func TestErrorChain_OutermostStructuredErrorWins(t *testing.T) {
	// Simulate a chain: inner Internal error wrapped by outer Validation error.
	// The outermost structured error should be the one detected by WrapError.
	inner := Internal("inner_code", "inner failure")
	outer := ValidationWrap(inner, "outer_code", "outer failure", "fix the outer thing")

	// errors.As finds the outermost LocalError first
	var localErr *azdext.LocalError
	require.ErrorAs(t, outer, &localErr)
	assert.Equal(t, "outer_code", localErr.Code)
	assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)

	// The inner error is reachable via Unwrap chain
	var innerLocal *azdext.LocalError
	require.ErrorAs(t, localErr.Cause, &innerLocal)
	assert.Equal(t, "inner_code", innerLocal.Code)
	assert.Equal(t, azdext.LocalErrorCategoryInternal, innerLocal.Category)
}

func TestErrorChain_WrapErrorPicksOutermostStructured(t *testing.T) {
	// Build chain: ServiceError → (cause) → LocalError (inner)
	innerLocal := Internal("inner_local", "inner local error")
	outerService := &azdext.ServiceError{
		Message:     "service failed",
		ErrorCode:   "SvcFail",
		StatusCode:  500,
		ServiceName: "test.azure.com",
		Cause:       innerLocal,
	}

	// WrapError should pick the outermost ServiceError
	protoErr := azdext.WrapError(outerService)
	require.NotNil(t, protoErr)
	assert.Equal(t, azdext.ErrorOrigin_ERROR_ORIGIN_SERVICE, protoErr.GetOrigin())
	assert.Equal(t, "service failed", protoErr.GetMessage())

	svcDetail := protoErr.GetServiceError()
	require.NotNil(t, svcDetail)
	assert.Equal(t, "SvcFail", svcDetail.GetErrorCode())
}

func TestServiceFromAzure_PreservesCause(t *testing.T) {
	// When wrapping a non-Azure error, the cause should be preserved.
	cause := fmt.Errorf("connection refused")
	result := ServiceFromAzure(cause, "get_project")

	var localErr *azdext.LocalError
	require.ErrorAs(t, result, &localErr)
	assert.Equal(t, "get_project", localErr.Code)
	assert.True(t, errors.Is(result, cause), "original cause should be reachable")
}

func TestFromAiService_PreservesCause(t *testing.T) {
	grpcErr := status.Error(codes.InvalidArgument, "bad request")
	result := FromAiService(grpcErr, "model_catalog_failed")

	var localErr *azdext.LocalError
	require.ErrorAs(t, result, &localErr)
	assert.Equal(t, azdext.LocalErrorCategoryInternal, localErr.Category)
	assert.NotNil(t, localErr.Cause, "cause should be preserved for debugging")
}
