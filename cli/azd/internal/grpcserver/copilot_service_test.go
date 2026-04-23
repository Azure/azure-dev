// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/watch"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
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

func (m *MockAgent) GetMessages(ctx context.Context) ([]agent.SessionEvent, error) {
	args := m.Called(ctx)
	if result := args.Get(0); result != nil {
		return result.([]agent.SessionEvent), args.Error(1)
	}
	return nil, args.Error(1)
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

func TestCopilotService_GetMessages_ValidSession(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}

	now := time.Now()
	content := "Hello, I can help with that."
	toolName := "read_file"

	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("SendMessage", mock.Anything, "test", mock.Anything).Return(&agent.AgentResult{
		SessionID: "msg-session",
	}, nil)
	mockAgent.On("GetMessages", mock.Anything).Return([]agent.SessionEvent{
		{
			Type:      copilot.AssistantMessage,
			Timestamp: now,
			Data:      copilot.Data{Content: &content},
		},
		{
			Type:      copilot.ToolExecutionStart,
			Timestamp: now.Add(time.Second),
			Data:      copilot.Data{ToolName: &toolName},
		},
	}, nil)

	svc := NewCopilotService(factory)
	ctx := t.Context()

	_, err := svc.SendMessage(ctx, &azdext.SendCopilotMessageRequest{Prompt: "test"})
	require.NoError(t, err)

	resp, err := svc.GetMessages(ctx, &azdext.GetCopilotMessagesRequest{
		SessionId: "msg-session",
	})

	require.NoError(t, err)
	require.Len(t, resp.Events, 2)
	require.Equal(t, "assistant.message", resp.Events[0].Type)
	require.Equal(t, "tool.execution_start", resp.Events[1].Type)

	// Verify data struct contains the content field
	contentVal := resp.Events[0].Data.Fields["content"].GetStringValue()
	require.Equal(t, "Hello, I can help with that.", contentVal)

	toolVal := resp.Events[1].Data.Fields["toolName"].GetStringValue()
	require.Equal(t, "read_file", toolVal)
}

func TestCopilotService_GetMessages_UnknownSession(t *testing.T) {
	t.Parallel()
	svc := NewCopilotService(&MockAgentFactory{})
	ctx := t.Context()

	_, err := svc.GetMessages(ctx, &azdext.GetCopilotMessagesRequest{
		SessionId: "nonexistent",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestCopilotService_GetMessages_RoundTrip(t *testing.T) {
	t.Parallel()

	// Build realistic SDK events with multiple data fields
	content := "I'll create the infrastructure files for your app."
	model := "gpt-4o"
	inputTokens := float64(500)
	outputTokens := float64(200)
	toolName := "write"
	filePath := "infra/main.bicep"
	intent := "Creating infrastructure"

	originalEvents := []agent.SessionEvent{
		{
			ID:        "evt-1",
			Type:      copilot.AssistantMessage,
			Timestamp: time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC),
			Data:      copilot.Data{Content: &content},
		},
		{
			ID:        "evt-2",
			Type:      copilot.AssistantUsage,
			Timestamp: time.Date(2026, 3, 18, 12, 0, 1, 0, time.UTC),
			Data: copilot.Data{
				Model:        &model,
				InputTokens:  &inputTokens,
				OutputTokens: &outputTokens,
			},
		},
		{
			ID:        "evt-3",
			Type:      copilot.ToolExecutionStart,
			Timestamp: time.Date(2026, 3, 18, 12, 0, 2, 0, time.UTC),
			Data: copilot.Data{
				ToolName: &toolName,
				Path:     &filePath,
				Intent:   &intent,
			},
		},
	}

	// Convert to proto (simulating gRPC transport)
	protoEvents := make([]*azdext.CopilotSessionEvent, len(originalEvents))
	for i, event := range originalEvents {
		protoEvents[i] = convertSessionEvent(event)
	}

	// Verify proto types and timestamps
	require.Equal(t, "assistant.message", protoEvents[0].Type)
	require.Equal(t, "assistant.usage", protoEvents[1].Type)
	require.Equal(t, "tool.execution_start", protoEvents[2].Type)

	// Round-trip: convert proto Struct back to SDK Data type
	for i, protoEvent := range protoEvents {
		// Marshal proto Struct to JSON
		jsonBytes, err := protojson.Marshal(protoEvent.Data)
		require.NoError(t, err, "failed to marshal proto struct for event %d", i)

		// Unmarshal JSON into SDK Data type
		var roundTripped copilot.Data
		err = json.Unmarshal(jsonBytes, &roundTripped)
		require.NoError(t, err, "failed to unmarshal to SDK Data for event %d", i)

		// Verify the round-tripped data matches the original
		switch originalEvents[i].Type {
		case copilot.AssistantMessage:
			require.NotNil(t, roundTripped.Content)
			require.Equal(t, *originalEvents[i].Data.Content, *roundTripped.Content)

		case copilot.AssistantUsage:
			require.NotNil(t, roundTripped.Model)
			require.Equal(t, *originalEvents[i].Data.Model, *roundTripped.Model)
			require.NotNil(t, roundTripped.InputTokens)
			require.Equal(t, *originalEvents[i].Data.InputTokens, *roundTripped.InputTokens)
			require.NotNil(t, roundTripped.OutputTokens)
			require.Equal(t, *originalEvents[i].Data.OutputTokens, *roundTripped.OutputTokens)

		case copilot.ToolExecutionStart:
			require.NotNil(t, roundTripped.ToolName)
			require.Equal(t, *originalEvents[i].Data.ToolName, *roundTripped.ToolName)
			require.NotNil(t, roundTripped.Path)
			require.Equal(t, *originalEvents[i].Data.Path, *roundTripped.Path)
		}
	}
}
