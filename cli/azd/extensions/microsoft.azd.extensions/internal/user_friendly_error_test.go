// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserFriendlyError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *UserFriendlyError
		expected string
	}{
		{
			name: "BasicMessage",
			err: &UserFriendlyError{
				ErrorMessage: "something went wrong",
				UserDetails:  "Try running the command again",
			},
			expected: "something went wrong",
		},
		{
			name: "EmptyMessage",
			err: &UserFriendlyError{
				ErrorMessage: "",
				UserDetails:  "details only",
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.err.Error())
		})
	}
}

func TestUserFriendlyError_GetUserDetails(t *testing.T) {
	tests := []struct {
		name     string
		err      *UserFriendlyError
		expected string
	}{
		{
			name: "WithDetails",
			err: &UserFriendlyError{
				ErrorMessage: "error",
				UserDetails:  "Run azd init first",
			},
			expected: "Run azd init first",
		},
		{
			name: "EmptyDetails",
			err: &UserFriendlyError{
				ErrorMessage: "error",
				UserDetails:  "",
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.err.GetUserDetails())
		})
	}
}

func TestUserFriendlyError_ImplementsErrorInterface(t *testing.T) {
	ufe := NewUserFriendlyError("test error", "test details")
	var err error = ufe
	require.Error(t, err)
	require.Equal(t, "test error", err.Error())
}

func TestNewUserFriendlyError(t *testing.T) {
	err := NewUserFriendlyError("the error", "the details")
	require.NotNil(t, err)
	require.Equal(t, "the error", err.ErrorMessage)
	require.Equal(t, "the details", err.UserDetails)
	require.Equal(t, "the error", err.Error())
	require.Equal(t, "the details", err.GetUserDetails())
}

func TestNewUserFriendlyErrorf(t *testing.T) {
	tests := []struct {
		name            string
		errorMessage    string
		userDetails     string
		args            []any
		expectedMessage string
		expectedDetails string
	}{
		{
			name:            "WithFormatArgs",
			errorMessage:    "build failed",
			userDetails:     "Run %s in %s directory",
			args:            []any{"go build", "/src"},
			expectedMessage: "build failed",
			expectedDetails: "Run go build in /src directory",
		},
		{
			name:            "NoFormatArgs",
			errorMessage:    "error",
			userDetails:     "plain details",
			args:            nil,
			expectedMessage: "error",
			expectedDetails: "plain details",
		},
		{
			name:            "SingleArg",
			errorMessage:    "not found",
			userDetails:     "file %q does not exist",
			args:            []any{"config.yaml"},
			expectedMessage: "not found",
			expectedDetails: `file "config.yaml" does not exist`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewUserFriendlyErrorf(
				tt.errorMessage, tt.userDetails, tt.args...,
			)
			require.NotNil(t, err)
			require.Equal(t, tt.expectedMessage, err.Error())
			require.Equal(t, tt.expectedDetails, err.GetUserDetails())
		})
	}
}

func TestUserFriendlyError_ErrorsAs(t *testing.T) {
	ufe := NewUserFriendlyError("wrapped", "details")
	wrapped := errors.Join(errors.New("context"), ufe)

	var target *UserFriendlyError
	require.True(t, errors.As(wrapped, &target))
	require.Equal(t, "wrapped", target.ErrorMessage)
	require.Equal(t, "details", target.UserDetails)
}
