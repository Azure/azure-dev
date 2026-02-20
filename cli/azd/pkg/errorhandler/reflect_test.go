// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test error types for reflection matching
type testDeploymentError struct {
	Details *testErrorDetails
	Title   string
}

func (e *testDeploymentError) Error() string {
	if e.Title != "" {
		return e.Title
	}
	return "deployment failed"
}

type testErrorDetails struct {
	Code    string
	Message string
}

type testAuthError struct {
	ErrorCode string
	inner     error
}

func (e *testAuthError) Error() string { return "auth failed" }
func (e *testAuthError) Unwrap() error { return e.inner }

type testWrappedError struct {
	msg   string
	inner error
}

func (e *testWrappedError) Error() string { return e.msg }
func (e *testWrappedError) Unwrap() error { return e.inner }

func TestFindErrorByTypeName(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		typeName string
		found    bool
	}{
		{
			name:     "direct match",
			err:      &testDeploymentError{Title: "test"},
			typeName: "testDeploymentError",
			found:    true,
		},
		{
			name:     "no match",
			err:      errors.New("plain error"),
			typeName: "testDeploymentError",
			found:    false,
		},
		{
			name: "wrapped match",
			err: &testWrappedError{
				msg:   "outer",
				inner: &testDeploymentError{Title: "inner"},
			},
			typeName: "testDeploymentError",
			found:    true,
		},
		{
			name: "deeply wrapped match",
			err: &testWrappedError{
				msg: "outer",
				inner: &testWrappedError{
					msg:   "middle",
					inner: &testAuthError{ErrorCode: "AUTH001"},
				},
			},
			typeName: "testAuthError",
			found:    true,
		},
		{
			name:     "wrong type name",
			err:      &testDeploymentError{Title: "test"},
			typeName: "SomeOtherError",
			found:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := findErrorByTypeName(tt.err, tt.typeName)
			assert.Equal(t, tt.found, ok)
			if tt.found {
				assert.NotNil(t, result)
			}
		})
	}
}

func TestResolvePropertyPath(t *testing.T) {
	err := &testDeploymentError{
		Title: "Deployment Failed",
		Details: &testErrorDetails{
			Code:    "InsufficientQuota",
			Message: "Not enough quota",
		},
	}

	tests := []struct {
		name     string
		target   any
		path     string
		expected string
		found    bool
	}{
		{
			name:     "simple field",
			target:   err,
			path:     "Title",
			expected: "Deployment Failed",
			found:    true,
		},
		{
			name:     "nested field",
			target:   err,
			path:     "Details.Code",
			expected: "InsufficientQuota",
			found:    true,
		},
		{
			name:     "nested message field",
			target:   err,
			path:     "Details.Message",
			expected: "Not enough quota",
			found:    true,
		},
		{
			name:   "nonexistent field",
			target: err,
			path:   "NonExistent",
			found:  false,
		},
		{
			name:   "nonexistent nested field",
			target: err,
			path:   "Details.NonExistent",
			found:  false,
		},
		{
			name: "nil pointer in path",
			target: &testDeploymentError{
				Title:   "test",
				Details: nil,
			},
			path:  "Details.Code",
			found: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := resolvePropertyPath(tt.target, tt.path)
			assert.Equal(t, tt.found, ok)
			if tt.found {
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestMatchProperties(t *testing.T) {
	err := &testDeploymentError{
		Title: "test",
		Details: &testErrorDetails{
			Code:    "InsufficientQuota",
			Message: "Not enough",
		},
	}

	tests := []struct {
		name       string
		target     any
		properties map[string]string
		expected   bool
	}{
		{
			name:       "single property match",
			target:     err,
			properties: map[string]string{"Details.Code": "InsufficientQuota"},
			expected:   true,
		},
		{
			name:   "multiple properties all match",
			target: err,
			properties: map[string]string{
				"Details.Code":    "InsufficientQuota",
				"Details.Message": "Not enough",
			},
			expected: true,
		},
		{
			name:       "property value mismatch",
			target:     err,
			properties: map[string]string{"Details.Code": "WrongCode"},
			expected:   false,
		},
		{
			name:       "nonexistent property",
			target:     err,
			properties: map[string]string{"Bogus.Path": "value"},
			expected:   false,
		},
		{
			name:       "empty properties matches",
			target:     err,
			properties: map[string]string{},
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchProperties(tt.target, tt.properties)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestResolvePropertyPath_UnexportedField(t *testing.T) {
	err := &testAuthError{ErrorCode: "AUTH001"}
	// 'inner' is unexported — should not be accessible
	_, ok := resolvePropertyPath(err, "inner")
	require.False(t, ok)
}
