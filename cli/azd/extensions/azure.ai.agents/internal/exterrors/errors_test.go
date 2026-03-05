// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsAuthError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "Unauthenticated gRPC error",
			err:  status.Error(codes.Unauthenticated, "not logged in"),
			want: true,
		},
		{
			name: "Other gRPC error",
			err:  status.Error(codes.NotFound, "not found"),
			want: false,
		},
		{
			name: "Plain error",
			err:  errors.New("something went wrong"),
			want: false,
		},
		{
			name: "Nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsAuthError(tt.err))
		})
	}
}

func TestFromAzdHost_AuthError(t *testing.T) {
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
			result := FromAzdHost(tt.err, tt.fallbackCode)
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
			name:       "Non-auth error returns wrapped error",
			err:        status.Error(codes.Internal, "server error"),
			contextMsg: "failed to prompt for subscription",
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
