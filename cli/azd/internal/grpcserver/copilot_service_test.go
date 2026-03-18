// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/watch"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Mocks ---

type MockAgent struct {
	mock.Mock
}

func (m *MockAgent) Initialize(ctx context.Context, opts ...agent.InitOption) (*agent.InitResult, error) {
	args := m.Called(ctx, opts)
	if result := args.Get(0); result != nil {
		return result.(*agent.InitResult), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockAgent) SendMessage(
	ctx context.Context, prompt string, opts ...agent.SendOption,
) (*agent.AgentResult, error) {
	args := m.Called(ctx, prompt, opts)
	if result := args.Get(0); result != nil {
		return result.(*agent.AgentResult), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockAgent) SendMessageWithRetry(
	ctx context.Context, prompt string, opts ...agent.SendOption,
) (*agent.AgentResult, error) {
	args := m.Called(ctx, prompt, opts)
	if result := args.Get(0); result != nil {
		return result.(*agent.AgentResult), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockAgent) ListSessions(ctx context.Context, cwd string) ([]agent.SessionMetadata, error) {
	args := m.Called(ctx, cwd)
	if result := args.Get(0); result != nil {
		return result.([]agent.SessionMetadata), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockAgent) GetMetrics() agent.AgentMetrics {
	args := m.Called()
	return args.Get(0).(agent.AgentMetrics)
}

func (m *MockAgent) SessionID() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockAgent) Stop() error {
	args := m.Called()
	return args.Error(0)
}

type MockAgentFactory struct {
	mock.Mock
}

func (m *MockAgentFactory) Create(ctx context.Context, opts ...agent.AgentOption) (agent.Agent, error) {
	args := m.Called(ctx, opts)
	if result := args.Get(0); result != nil {
		return result.(agent.Agent), args.Error(1)
	}
	return nil, args.Error(1)
}

// --- Tests ---

func TestCopilotService_SendMessage_NewSession(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}

	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("SendMessage", mock.Anything, "hello", mock.Anything).Return(&agent.AgentResult{
		SessionID: "sdk-session-123",
		Usage:     agent.UsageMetrics{Model: "gpt-4o", InputTokens: 100, OutputTokens: 50},
	}, nil)

	svc := NewCopilotService(factory)
	ctx := t.Context()

	resp, err := svc.SendMessage(ctx, &azdext.SendCopilotMessageRequest{
		Prompt:   "hello",
		Headless: true,
	})

	require.NoError(t, err)
	require.Equal(t, "sdk-session-123", resp.SessionId)
	require.Equal(t, "gpt-4o", resp.Usage.Model)
	require.Equal(t, float64(100), resp.Usage.InputTokens)
	require.Equal(t, float64(50), resp.Usage.OutputTokens)
	require.Equal(t, float64(150), resp.Usage.TotalTokens)

	factory.AssertExpectations(t)
	mockAgent.AssertExpectations(t)
}

func TestCopilotService_SendMessage_ReuseSession(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}

	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil).Once()
	mockAgent.On("SendMessage", mock.Anything, "first", mock.Anything).Return(&agent.AgentResult{
		SessionID: "sdk-session-456",
		Usage:     agent.UsageMetrics{InputTokens: 50},
	}, nil).Once()
	mockAgent.On("SendMessage", mock.Anything, "second", mock.Anything).Return(&agent.AgentResult{
		SessionID: "sdk-session-456",
		Usage:     agent.UsageMetrics{InputTokens: 75},
	}, nil).Once()

	svc := NewCopilotService(factory)
	ctx := t.Context()

	// First call — creates session
	resp1, err := svc.SendMessage(ctx, &azdext.SendCopilotMessageRequest{
		Prompt: "first",
	})
	require.NoError(t, err)
	require.Equal(t, "sdk-session-456", resp1.SessionId)

	// Second call — reuses session (no new Create call)
	resp2, err := svc.SendMessage(ctx, &azdext.SendCopilotMessageRequest{
		Prompt:    "second",
		SessionId: "sdk-session-456",
	})
	require.NoError(t, err)
	require.Equal(t, "sdk-session-456", resp2.SessionId)

	// Factory.Create should only be called once
	factory.AssertNumberOfCalls(t, "Create", 1)
}

func TestCopilotService_SendMessage_ResumeSDKSession(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}

	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("SendMessage", mock.Anything, "resuming", mock.Anything).Return(&agent.AgentResult{
		SessionID: "external-sdk-id",
		Usage:     agent.UsageMetrics{InputTokens: 200},
	}, nil)

	svc := NewCopilotService(factory)
	ctx := t.Context()

	// Pass an unknown session_id — treated as SDK session to resume
	resp, err := svc.SendMessage(ctx, &azdext.SendCopilotMessageRequest{
		Prompt:    "resuming",
		SessionId: "external-sdk-id",
	})

	require.NoError(t, err)
	require.Equal(t, "external-sdk-id", resp.SessionId)
	factory.AssertExpectations(t)
}

func TestCopilotService_SendMessage_EmptyPrompt(t *testing.T) {
	t.Parallel()
	svc := NewCopilotService(&MockAgentFactory{})
	ctx := t.Context()

	_, err := svc.SendMessage(ctx, &azdext.SendCopilotMessageRequest{
		Prompt: "",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "prompt cannot be empty")
}

func TestCopilotService_GetUsageMetrics_ValidSession(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}

	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("SendMessage", mock.Anything, "test", mock.Anything).Return(&agent.AgentResult{
		SessionID: "metrics-session",
		Usage:     agent.UsageMetrics{InputTokens: 100},
	}, nil)
	mockAgent.On("GetMetrics").Return(agent.AgentMetrics{
		Usage: agent.UsageMetrics{
			Model: "gpt-4o", InputTokens: 500, OutputTokens: 250, DurationMS: 3000,
		},
	})

	svc := NewCopilotService(factory)
	ctx := t.Context()

	// Create a session first
	_, err := svc.SendMessage(ctx, &azdext.SendCopilotMessageRequest{Prompt: "test"})
	require.NoError(t, err)

	// Get metrics
	resp, err := svc.GetUsageMetrics(ctx, &azdext.GetCopilotUsageMetricsRequest{
		SessionId: "metrics-session",
	})

	require.NoError(t, err)
	require.Equal(t, "gpt-4o", resp.Usage.Model)
	require.Equal(t, float64(500), resp.Usage.InputTokens)
	require.Equal(t, float64(250), resp.Usage.OutputTokens)
	require.Equal(t, float64(3000), resp.Usage.DurationMs)
}

func TestCopilotService_GetUsageMetrics_UnknownSession(t *testing.T) {
	t.Parallel()
	svc := NewCopilotService(&MockAgentFactory{})
	ctx := t.Context()

	_, err := svc.GetUsageMetrics(ctx, &azdext.GetCopilotUsageMetricsRequest{
		SessionId: "nonexistent",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestCopilotService_GetFileChanges_ValidSession(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}

	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("SendMessage", mock.Anything, "test", mock.Anything).Return(&agent.AgentResult{
		SessionID: "files-session",
	}, nil)
	mockAgent.On("GetMetrics").Return(agent.AgentMetrics{
		FileChanges: watch.FileChanges{
			{Path: "main.go", ChangeType: watch.FileModified},
			{Path: "new.txt", ChangeType: watch.FileCreated},
		},
	})

	svc := NewCopilotService(factory)
	ctx := t.Context()

	_, err := svc.SendMessage(ctx, &azdext.SendCopilotMessageRequest{Prompt: "test"})
	require.NoError(t, err)

	resp, err := svc.GetFileChanges(ctx, &azdext.GetCopilotFileChangesRequest{
		SessionId: "files-session",
	})

	require.NoError(t, err)
	require.Len(t, resp.FileChanges, 2)
	require.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_MODIFIED,
		resp.FileChanges[0].ChangeType)
	require.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_CREATED,
		resp.FileChanges[1].ChangeType)
}

func TestCopilotService_StopSession_Valid(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}

	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("SendMessage", mock.Anything, "test", mock.Anything).Return(&agent.AgentResult{
		SessionID: "stop-session",
	}, nil)
	mockAgent.On("Stop").Return(nil)

	svc := NewCopilotService(factory)
	ctx := t.Context()

	_, err := svc.SendMessage(ctx, &azdext.SendCopilotMessageRequest{Prompt: "test"})
	require.NoError(t, err)

	_, err = svc.StopSession(ctx, &azdext.StopCopilotSessionRequest{
		SessionId: "stop-session",
	})

	require.NoError(t, err)
	mockAgent.AssertCalled(t, "Stop")

	// Session should be gone now
	_, err = svc.GetUsageMetrics(ctx, &azdext.GetCopilotUsageMetricsRequest{
		SessionId: "stop-session",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestCopilotService_StopSession_UnknownSession(t *testing.T) {
	t.Parallel()
	svc := NewCopilotService(&MockAgentFactory{})
	ctx := t.Context()

	_, err := svc.StopSession(ctx, &azdext.StopCopilotSessionRequest{
		SessionId: "nonexistent",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestCopilotService_Initialize(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}

	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("Initialize", mock.Anything, mock.Anything).Return(&agent.InitResult{
		Model:           "gpt-4o",
		ReasoningEffort: "medium",
		IsFirstRun:      true,
	}, nil)
	mockAgent.On("Stop").Return(nil)

	svc := NewCopilotService(factory)
	ctx := t.Context()

	resp, err := svc.Initialize(ctx, &azdext.InitializeCopilotRequest{
		Model:           "gpt-4o",
		ReasoningEffort: "medium",
	})

	require.NoError(t, err)
	require.Equal(t, "gpt-4o", resp.Model)
	require.Equal(t, "medium", resp.ReasoningEffort)
	require.True(t, resp.IsFirstRun)
}

func TestCopilotService_GetUsageMetrics_EmptySessionId(t *testing.T) {
	t.Parallel()
	svc := NewCopilotService(&MockAgentFactory{})
	ctx := t.Context()

	_, err := svc.GetUsageMetrics(ctx, &azdext.GetCopilotUsageMetricsRequest{
		SessionId: "",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "session_id is required")
}

func TestCopilotService_SendMessage_WithFileChanges(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}

	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("SendMessage", mock.Anything, "make changes", mock.Anything).Return(&agent.AgentResult{
		SessionID: "fc-session",
		Usage:     agent.UsageMetrics{InputTokens: 100},
		FileChanges: []watch.FileChange{
			{Path: "created.go", ChangeType: watch.FileCreated},
			{Path: "deleted.go", ChangeType: watch.FileDeleted},
		},
	}, nil)

	svc := NewCopilotService(factory)
	ctx := t.Context()

	resp, err := svc.SendMessage(ctx, &azdext.SendCopilotMessageRequest{
		Prompt: "make changes",
	})

	require.NoError(t, err)
	require.Len(t, resp.FileChanges, 2)
	require.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_CREATED,
		resp.FileChanges[0].ChangeType)
	require.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_DELETED,
		resp.FileChanges[1].ChangeType)
}
