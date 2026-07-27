// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/watch"
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
			Type:      copilot.SessionEventTypeAssistantMessage,
			Timestamp: now,
			Data:      &copilot.AssistantMessageData{Content: content},
		},
		{
			Type:      copilot.SessionEventTypeToolExecutionStart,
			Timestamp: now.Add(time.Second),
			Data:      &copilot.ToolExecutionStartData{ToolName: toolName},
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

	originalEvents := []agent.SessionEvent{
		{
			ID:        "evt-1",
			Type:      copilot.SessionEventTypeAssistantMessage,
			Timestamp: time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC),
			Data:      &copilot.AssistantMessageData{Content: content},
		},
		{
			ID:        "evt-2",
			Type:      copilot.SessionEventTypeAssistantUsage,
			Timestamp: time.Date(2026, 3, 18, 12, 0, 1, 0, time.UTC),
			Data: &copilot.AssistantUsageData{
				Model:        model,
				InputTokens:  &inputTokens,
				OutputTokens: &outputTokens,
			},
		},
		{
			ID:        "evt-3",
			Type:      copilot.SessionEventTypeToolExecutionStart,
			Timestamp: time.Date(2026, 3, 18, 12, 0, 2, 0, time.UTC),
			Data: &copilot.ToolExecutionStartData{
				ToolName: toolName,
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

	// Round-trip: convert proto Struct back to JSON and verify key fields
	for i, protoEvent := range protoEvents {
		// Marshal proto Struct to JSON
		jsonBytes, err := protojson.Marshal(protoEvent.Data)
		require.NoError(t, err, "failed to marshal proto struct for event %d", i)

		// Unmarshal JSON into a generic map for verification (copilot.Data no longer exists)
		var roundTripped map[string]any
		err = json.Unmarshal(jsonBytes, &roundTripped)
		require.NoError(t, err, "failed to unmarshal to map for event %d", i)

		// Verify the round-tripped data matches the original
		switch originalEvents[i].Type {
		case copilot.SessionEventTypeAssistantMessage:
			require.Equal(t, content, roundTripped["content"])

		case copilot.SessionEventTypeAssistantUsage:
			require.Equal(t, model, roundTripped["model"])
			require.Equal(t, inputTokens, roundTripped["inputTokens"])
			require.Equal(t, outputTokens, roundTripped["outputTokens"])

		case copilot.SessionEventTypeToolExecutionStart:
			require.Equal(t, toolName, roundTripped["toolName"])
		}
	}
}

func TestCopilotService_ListSessions_Success(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}

	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("ListSessions", mock.Anything, mock.AnythingOfType("string")).Return([]agent.SessionMetadata{
		{SessionID: "s1", ModifiedTime: "2024-01-01T00:00:00Z", Summary: new("Summary 1")},
		{SessionID: "s2", ModifiedTime: "2024-01-02T00:00:00Z", Summary: nil},
	}, nil)
	mockAgent.On("Stop").Return(nil)

	svc := NewCopilotService(factory)
	resp, err := svc.ListSessions(t.Context(), &azdext.ListCopilotSessionsRequest{
		WorkingDirectory: t.TempDir(),
	})
	require.NoError(t, err)
	require.Len(t, resp.Sessions, 2)
	require.Equal(t, "s1", resp.Sessions[0].SessionId)
	require.Equal(t, "Summary 1", resp.Sessions[0].Summary)
	require.Equal(t, "s2", resp.Sessions[1].SessionId)
	require.Equal(t, "", resp.Sessions[1].Summary)
}

func TestCopilotService_ListSessions_FactoryError(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	factory.On("Create", mock.Anything, mock.Anything).Return(nil, errors.New("factory fail"))

	svc := NewCopilotService(factory)
	_, err := svc.ListSessions(t.Context(), &azdext.ListCopilotSessionsRequest{
		WorkingDirectory: t.TempDir(),
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	require.Equal(t, codes.Internal, st.Code())
}

func TestCopilotService_ListSessions_AgentError(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}
	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("ListSessions", mock.Anything, mock.AnythingOfType("string")).Return(nil, errors.New("list fail"))
	mockAgent.On("Stop").Return(nil)

	svc := NewCopilotService(factory)
	_, err := svc.ListSessions(t.Context(), &azdext.ListCopilotSessionsRequest{
		WorkingDirectory: t.TempDir(),
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	require.Equal(t, codes.Internal, st.Code())
}

func TestCopilotService_Initialize_Success(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}
	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("Initialize", mock.Anything, mock.Anything).Return(&agent.InitResult{
		Model:           "gpt-4o",
		ReasoningEffort: "high",
		IsFirstRun:      true,
	}, nil)
	mockAgent.On("Stop").Return(nil)

	svc := NewCopilotService(factory)
	resp, err := svc.Initialize(t.Context(), &azdext.InitializeCopilotRequest{
		Model:           "gpt-4o",
		ReasoningEffort: "high",
	})
	require.NoError(t, err)
	require.Equal(t, "gpt-4o", resp.Model)
	require.Equal(t, "high", resp.ReasoningEffort)
	require.True(t, resp.IsFirstRun)
}

func TestCopilotService_Initialize_FactoryError(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	factory.On("Create", mock.Anything, mock.Anything).Return(nil, errors.New("init fail"))

	svc := NewCopilotService(factory)
	_, err := svc.Initialize(t.Context(), &azdext.InitializeCopilotRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	require.Equal(t, codes.Internal, st.Code())
}

func TestCopilotService_Initialize_AgentError(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}
	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("Initialize", mock.Anything, mock.Anything).Return(nil, errors.New("init error"))
	mockAgent.On("Stop").Return(nil)

	svc := NewCopilotService(factory)
	_, err := svc.Initialize(t.Context(), &azdext.InitializeCopilotRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	require.Equal(t, codes.Internal, st.Code())
}

func TestCopilotService_SendMessage_FactoryFailCleanup(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	factory.On("Create", mock.Anything, mock.Anything).Return(nil, errors.New("create fail"))

	svc := NewCopilotService(factory)
	_, err := svc.SendMessage(t.Context(), &azdext.SendCopilotMessageRequest{
		Prompt:   "test",
		Headless: true,
	})
	require.Error(t, err)
}

func TestCopilotService_SendMessage_AgentErrorCleansUp(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}
	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("SendMessage", mock.Anything, "fail", mock.Anything).Return(nil, errors.New("send fail"))
	mockAgent.On("Stop").Return(nil)

	svc := NewCopilotService(factory)
	_, err := svc.SendMessage(t.Context(), &azdext.SendCopilotMessageRequest{
		Prompt:   "fail",
		Headless: true,
	})
	require.Error(t, err)
	mockAgent.AssertCalled(t, "Stop") // cleanup on failure
}

func TestCopilotService_GetUsageMetrics_NotFound(t *testing.T) {
	t.Parallel()
	svc := NewCopilotService(&MockAgentFactory{})
	_, err := svc.GetUsageMetrics(t.Context(), &azdext.GetCopilotUsageMetricsRequest{
		SessionId: "nonexistent",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestCopilotService_GetFileChanges_NotFound(t *testing.T) {
	t.Parallel()
	svc := NewCopilotService(&MockAgentFactory{})
	_, err := svc.GetFileChanges(t.Context(), &azdext.GetCopilotFileChangesRequest{
		SessionId: "nonexistent",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestCopilotService_GetMessages_NotFound(t *testing.T) {
	t.Parallel()
	svc := NewCopilotService(&MockAgentFactory{})
	_, err := svc.GetMessages(t.Context(), &azdext.GetCopilotMessagesRequest{
		SessionId: "nonexistent",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestCopilotService_StopSession_NotFound(t *testing.T) {
	t.Parallel()
	svc := NewCopilotService(&MockAgentFactory{})
	_, err := svc.StopSession(t.Context(), &azdext.StopCopilotSessionRequest{
		SessionId: "nonexistent",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestCopilotService_GetUsageMetrics_WithSession(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}
	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("SendMessage", mock.Anything, "hi", mock.Anything).Return(&agent.AgentResult{
		SessionID: "session-metrics",
		Usage:     agent.UsageMetrics{Model: "gpt-4o", InputTokens: 10, OutputTokens: 5},
	}, nil)
	mockAgent.On("GetMetrics").Return(agent.AgentMetrics{
		Usage: agent.UsageMetrics{Model: "gpt-4o", InputTokens: 10, OutputTokens: 5},
	})

	svc := NewCopilotService(factory)

	// First create a session
	_, err := svc.SendMessage(t.Context(), &azdext.SendCopilotMessageRequest{
		Prompt:   "hi",
		Headless: true,
	})
	require.NoError(t, err)

	// Now get metrics
	resp, err := svc.GetUsageMetrics(t.Context(), &azdext.GetCopilotUsageMetricsRequest{
		SessionId: "session-metrics",
	})
	require.NoError(t, err)
	require.Equal(t, "gpt-4o", resp.Usage.Model)
}

func TestCopilotService_StopSession_WithSession(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}
	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("SendMessage", mock.Anything, "hi", mock.Anything).Return(&agent.AgentResult{
		SessionID: "session-stop",
		Usage:     agent.UsageMetrics{},
	}, nil)
	mockAgent.On("Stop").Return(nil)

	svc := NewCopilotService(factory)

	_, err := svc.SendMessage(t.Context(), &azdext.SendCopilotMessageRequest{
		Prompt:   "hi",
		Headless: true,
	})
	require.NoError(t, err)

	resp, err := svc.StopSession(t.Context(), &azdext.StopCopilotSessionRequest{
		SessionId: "session-stop",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	mockAgent.AssertCalled(t, "Stop")
}

func TestCopilotService_GetFileChanges_WithSession(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}
	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("SendMessage", mock.Anything, "hi", mock.Anything).Return(&agent.AgentResult{
		SessionID: "session-files",
		Usage:     agent.UsageMetrics{},
	}, nil)
	mockAgent.On("GetMetrics").Return(agent.AgentMetrics{})

	svc := NewCopilotService(factory)

	_, err := svc.SendMessage(t.Context(), &azdext.SendCopilotMessageRequest{
		Prompt:   "hi",
		Headless: true,
	})
	require.NoError(t, err)

	resp, err := svc.GetFileChanges(t.Context(), &azdext.GetCopilotFileChangesRequest{
		SessionId: "session-files",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
}

func TestCopilotService_GetMessages_WithSession(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}
	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("SendMessage", mock.Anything, "hi", mock.Anything).Return(&agent.AgentResult{
		SessionID: "session-msgs",
		Usage:     agent.UsageMetrics{},
	}, nil)
	mockAgent.On("GetMessages", mock.Anything).Return([]agent.SessionEvent{
		{Type: "message"},
	}, nil)

	svc := NewCopilotService(factory)

	_, err := svc.SendMessage(t.Context(), &azdext.SendCopilotMessageRequest{
		Prompt:   "hi",
		Headless: true,
	})
	require.NoError(t, err)

	resp, err := svc.GetMessages(t.Context(), &azdext.GetCopilotMessagesRequest{
		SessionId: "session-msgs",
	})
	require.NoError(t, err)
	require.Len(t, resp.Events, 1)
}

func TestCopilotService_SendMessage_ResumeWithSessionId(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}
	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("SendMessage", mock.Anything, "resume msg", mock.Anything).Return(&agent.AgentResult{
		SessionID: "external-session-id",
		Usage:     agent.UsageMetrics{},
	}, nil)

	svc := NewCopilotService(factory)

	// Send with a session ID that doesn't exist - should create new and treat as resume
	resp, err := svc.SendMessage(t.Context(), &azdext.SendCopilotMessageRequest{
		Prompt:    "resume msg",
		SessionId: "external-session-id",
		Headless:  true,
	})
	require.NoError(t, err)
	require.Equal(t, "external-session-id", resp.SessionId)
}

// Ensure context is propagated for testing listSessions with empty working directory
func TestCopilotService_ListSessions_EmptyWorkingDir(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}
	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("ListSessions", mock.Anything, mock.AnythingOfType("string")).Return(
		[]agent.SessionMetadata{}, nil)
	mockAgent.On("Stop").Return(nil)

	svc := NewCopilotService(factory)
	resp, err := svc.ListSessions(t.Context(), &azdext.ListCopilotSessionsRequest{
		WorkingDirectory: "", // empty, should use os.Getwd()
	})
	require.NoError(t, err)
	require.Empty(t, resp.Sessions)
}

// Mock to test resolveOrCreateAgent method through the service
func TestCopilotService_ResolveOrCreateAgent_Existing(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}

	// First create a session by sending a message
	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil).Once()
	mockAgent.On("SendMessage", mock.Anything, "first", mock.Anything).Return(&agent.AgentResult{
		SessionID: "reuse-session",
		Usage:     agent.UsageMetrics{},
	}, nil).Once()

	// Second message should reuse the same agent (no new Create call)
	mockAgent.On("SendMessage", mock.Anything, "second", mock.Anything).Return(&agent.AgentResult{
		SessionID: "reuse-session",
		Usage:     agent.UsageMetrics{},
	}, nil).Once()

	svc := NewCopilotService(factory)

	_, err := svc.SendMessage(t.Context(), &azdext.SendCopilotMessageRequest{
		Prompt:   "first",
		Headless: true,
	})
	require.NoError(t, err)

	_, err = svc.SendMessage(t.Context(), &azdext.SendCopilotMessageRequest{
		Prompt:    "second",
		SessionId: "reuse-session",
		Headless:  true,
	})
	require.NoError(t, err)

	// Create should only be called once
	factory.AssertNumberOfCalls(t, "Create", 1)
}

func TestCopilotService_GetMessages_AgentError(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}
	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("SendMessage", mock.Anything, "hi", mock.Anything).Return(&agent.AgentResult{
		SessionID: "session-err",
		Usage:     agent.UsageMetrics{},
	}, nil)
	mockAgent.On("GetMessages", mock.Anything).Return(nil, errors.New("messages fail"))

	svc := NewCopilotService(factory)

	_, err := svc.SendMessage(t.Context(), &azdext.SendCopilotMessageRequest{
		Prompt:   "hi",
		Headless: true,
	})
	require.NoError(t, err)

	_, err = svc.GetMessages(t.Context(), &azdext.GetCopilotMessagesRequest{
		SessionId: "session-err",
	})
	require.Error(t, err)
}

// Verify getAgent returns error for unknown session
func TestCopilotService_getAgent_Unknown(t *testing.T) {
	t.Parallel()
	svc := &copilotService{
		sessions: make(map[string]agent.Agent),
	}
	_, err := svc.getAgent("nonexistent")
	require.Error(t, err)
	st, _ := status.FromError(err)
	require.Equal(t, codes.NotFound, st.Code())
}

// Verify getAgent returns empty session ID error
func TestCopilotService_getAgent_EmptyID(t *testing.T) {
	t.Parallel()
	svc := &copilotService{
		sessions: make(map[string]agent.Agent),
	}
	_, err := svc.getAgent("")
	require.Error(t, err)
	st, _ := status.FromError(err)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

// Verify getAgent returns known session
func TestCopilotService_getAgent_Found(t *testing.T) {
	t.Parallel()
	mockAgent := &MockAgent{}
	svc := &copilotService{
		sessions: map[string]agent.Agent{
			"known": mockAgent,
		},
	}
	a, err := svc.getAgent("known")
	require.NoError(t, err)
	require.Equal(t, mockAgent, a)
}

// Verify resolveOrCreateAgent handles missing session gracefully - creates new
func TestCopilotService_resolveOrCreateAgent_NewSession(t *testing.T) {
	t.Parallel()
	factory := &MockAgentFactory{}
	mockAgent := &MockAgent{}
	factory.On("Create", mock.Anything, mock.Anything).Return(mockAgent, nil)

	svc := &copilotService{
		agentFactory: factory,
		sessions:     make(map[string]agent.Agent),
	}

	a, isNew, isResume, err := svc.resolveOrCreateAgent(
		t.Context(),
		&azdext.SendCopilotMessageRequest{Prompt: "test", Headless: true},
	)
	require.NoError(t, err)
	require.NotNil(t, a)
	require.True(t, isNew)
	require.False(t, isResume)
}
