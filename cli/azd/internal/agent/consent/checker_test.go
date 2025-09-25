// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package consent

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockConsentManager is a mock implementation of ConsentManager for testing
type MockConsentManager struct {
	mock.Mock
}

func (m *MockConsentManager) CheckConsent(ctx context.Context, request ConsentRequest) (*ConsentDecision, error) {
	args := m.Called(ctx, request)
	return args.Get(0).(*ConsentDecision), args.Error(1)
}

func (m *MockConsentManager) GrantConsent(ctx context.Context, rule ConsentRule) error {
	args := m.Called(ctx, rule)
	return args.Error(0)
}

func (m *MockConsentManager) ListConsentRules(ctx context.Context, options ...FilterOption) ([]ConsentRule, error) {
	args := m.Called(ctx, options)
	return args.Get(0).([]ConsentRule), args.Error(1)
}

func (m *MockConsentManager) ClearConsentRules(ctx context.Context, options ...FilterOption) error {
	args := m.Called(ctx, options)
	return args.Error(0)
}

func (m *MockConsentManager) IsProjectScopeAvailable(ctx context.Context) bool {
	args := m.Called(ctx)
	return args.Bool(0)
}

func (m *MockConsentManager) WrapTool(tool common.AnnotatedTool) common.AnnotatedTool {
	args := m.Called(tool)
	return args.Get(0).(common.AnnotatedTool)
}

func (m *MockConsentManager) WrapTools(tools []common.AnnotatedTool) []common.AnnotatedTool {
	args := m.Called(tools)
	return args.Get(0).([]common.AnnotatedTool)
}

func TestConsentChecker_formatToolDescriptionWithAnnotations(t *testing.T) {
	tests := []struct {
		name        string
		toolDesc    string
		annotations mcp.ToolAnnotation
		expected    string
	}{
		{
			name:        "EmptyDescription",
			toolDesc:    "",
			annotations: mcp.ToolAnnotation{},
			expected:    "No description available",
		},
		{
			name:        "SimpleDescription",
			toolDesc:    "A simple test tool",
			annotations: mcp.ToolAnnotation{},
			expected:    "A simple test tool",
		},
		{
			name:     "DescriptionWithReadOnly",
			toolDesc: "A test tool",
			annotations: mcp.ToolAnnotation{
				ReadOnlyHint: func() *bool { b := true; return &b }(),
			},
			expected: "A test tool\n\nTool characteristics:\n• Read-only operation",
		},
		{
			name:     "DescriptionWithDestructive",
			toolDesc: "A test tool",
			annotations: mcp.ToolAnnotation{
				DestructiveHint: func() *bool { b := true; return &b }(),
			},
			expected: "A test tool\n\nTool characteristics:\n• Potentially destructive operation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockManager := &MockConsentManager{}
			checker := NewConsentChecker(mockManager, "test-server")

			result := checker.formatToolDescriptionWithAnnotations(tt.toolDesc, tt.annotations)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewConsentChecker(t *testing.T) {
	mockManager := &MockConsentManager{}
	serverName := "test-server"

	checker := NewConsentChecker(mockManager, serverName)

	assert.NotNil(t, checker)
	assert.Equal(t, mockManager, checker.consentMgr)
	assert.Equal(t, serverName, checker.serverName)
}
