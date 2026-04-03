// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
		context.Background(),
		&azdext.SendCopilotMessageRequest{Prompt: "test", Headless: true},
	)
	require.NoError(t, err)
	require.NotNil(t, a)
	require.True(t, isNew)
	require.False(t, isResume)
}
