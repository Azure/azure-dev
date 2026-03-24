// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/watch"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// copilotService implements the CopilotServiceServer gRPC interface.
// It is a thin routing layer that maps session IDs to CopilotAgent instances.
// All metrics and file change state lives on the agent itself.
type copilotService struct {
	azdext.UnimplementedCopilotServiceServer

	agentFactory agent.AgentFactory

	mu       sync.RWMutex
	sessions map[string]agent.Agent
}

// NewCopilotService creates a new CopilotService gRPC server.
func NewCopilotService(agentFactory agent.AgentFactory) azdext.CopilotServiceServer {
	return &copilotService{
		agentFactory: agentFactory,
		sessions:     make(map[string]agent.Agent),
	}
}

// Initialize starts the Copilot client, verifies authentication, and resolves
// model/reasoning configuration. Does not create a session. Idempotent.
func (s *copilotService) Initialize(
	ctx context.Context, req *azdext.InitializeCopilotRequest,
) (*azdext.InitializeCopilotResponse, error) {
	var opts []agent.AgentOption
	if req.Model != "" {
		opts = append(opts, agent.WithModel(req.Model))
	}
	if req.ReasoningEffort != "" {
		opts = append(opts, agent.WithReasoningEffort(req.ReasoningEffort))
	}

	tempAgent, err := s.agentFactory.Create(ctx, opts...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create agent: %v", err)
	}
	defer tempAgent.Stop()

	result, err := tempAgent.Initialize(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to initialize copilot: %v", err)
	}

	return &azdext.InitializeCopilotResponse{
		Model:           result.Model,
		ReasoningEffort: result.ReasoningEffort,
		IsFirstRun:      result.IsFirstRun,
	}, nil
}

// ListSessions returns available Copilot sessions for a working directory.
func (s *copilotService) ListSessions(
	ctx context.Context, req *azdext.ListCopilotSessionsRequest,
) (*azdext.ListCopilotSessionsResponse, error) {
	cwd := req.WorkingDirectory
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, status.Errorf(codes.Internal,
				"failed to get working directory: %v", err)
		}
	}

	tempAgent, err := s.agentFactory.Create(ctx, agent.WithHeadless(true))
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to create agent for listing sessions: %v", err)
	}
	defer tempAgent.Stop()

	sessions, err := tempAgent.ListSessions(ctx, cwd)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list sessions: %v", err)
	}

	protoSessions := make([]*azdext.CopilotSessionMetadata, len(sessions))
	for i, session := range sessions {
		summary := ""
		if session.Summary != nil {
			summary = *session.Summary
		}
		protoSessions[i] = &azdext.CopilotSessionMetadata{
			SessionId:    session.SessionID,
			ModifiedTime: session.ModifiedTime,
			Summary:      summary,
		}
	}

	return &azdext.ListCopilotSessionsResponse{Sessions: protoSessions}, nil
}

// SendMessage sends a prompt to the Copilot agent. On the first call a new
// session is created using the provided configuration. If session_id is set
// and matches a managed session, that session is reused. If session_id is set
// but not found, it is treated as an SDK session ID to resume.
func (s *copilotService) SendMessage(
	ctx context.Context, req *azdext.SendCopilotMessageRequest,
) (*azdext.SendCopilotMessageResponse, error) {
	if req.Prompt == "" {
		return nil, status.Error(codes.InvalidArgument, "prompt cannot be empty")
	}

	copilotAgent, isNew, isResume, err := s.resolveOrCreateAgent(ctx, req)
	if err != nil {
		return nil, err
	}

	var sendOpts []agent.SendOption
	if isResume {
		sendOpts = append(sendOpts, agent.WithSessionID(req.SessionId))
	}

	result, err := copilotAgent.SendMessage(ctx, req.Prompt, sendOpts...)
	if err != nil {
		// Clean up newly created agents that failed on first message
		if isNew {
			copilotAgent.Stop()
		}
		return nil, status.Errorf(codes.Internal, "copilot agent error: %v", err)
	}

	// Store the agent by its SDK session ID for reuse
	sessionID := result.SessionID
	s.mu.Lock()
	s.sessions[sessionID] = copilotAgent
	s.mu.Unlock()

	return &azdext.SendCopilotMessageResponse{
		SessionId:   sessionID,
		Usage:       convertUsageMetrics(result.Usage),
		FileChanges: convertFileChanges(result.FileChanges),
	}, nil
}

// resolveOrCreateAgent finds an existing managed agent, creates a new one,
// or prepares one for SDK session resumption.
func (s *copilotService) resolveOrCreateAgent(
	ctx context.Context, req *azdext.SendCopilotMessageRequest,
) (copilotAgent agent.Agent, isNew bool, isResume bool, err error) {
	if req.SessionId != "" {
		// Try to reuse an existing managed session
		s.mu.RLock()
		existing, ok := s.sessions[req.SessionId]
		s.mu.RUnlock()

		if ok {
			return existing, false, false, nil
		}

		// Not in our map — treat as an SDK session ID to resume
		isResume = true
	}

	// Create a new agent
	opts := buildAgentOptions(
		req.Model, req.ReasoningEffort, req.SystemMessage,
		req.Mode, req.Debug, req.Headless,
	)

	copilotAgent, err = s.agentFactory.Create(ctx, opts...)
	if err != nil {
		return nil, false, false, status.Errorf(codes.Internal,
			"failed to create copilot agent: %v", err)
	}

	return copilotAgent, true, isResume, nil
}

// GetUsageMetrics returns cumulative usage metrics cached on the agent.
func (s *copilotService) GetUsageMetrics(
	ctx context.Context, req *azdext.GetCopilotUsageMetricsRequest,
) (*azdext.GetCopilotUsageMetricsResponse, error) {
	copilotAgent, err := s.getAgent(req.SessionId)
	if err != nil {
		return nil, err
	}

	metrics := copilotAgent.GetMetrics()
	return &azdext.GetCopilotUsageMetricsResponse{
		Usage: convertUsageMetrics(metrics.Usage),
	}, nil
}

// GetFileChanges returns accumulated file changes cached on the agent.
func (s *copilotService) GetFileChanges(
	ctx context.Context, req *azdext.GetCopilotFileChangesRequest,
) (*azdext.GetCopilotFileChangesResponse, error) {
	copilotAgent, err := s.getAgent(req.SessionId)
	if err != nil {
		return nil, err
	}

	metrics := copilotAgent.GetMetrics()
	return &azdext.GetCopilotFileChangesResponse{
		FileChanges: convertFileChanges(metrics.FileChanges),
	}, nil
}

// GetMessages returns the session event log from the Copilot SDK.
func (s *copilotService) GetMessages(
	ctx context.Context, req *azdext.GetCopilotMessagesRequest,
) (*azdext.GetCopilotMessagesResponse, error) {
	copilotAgent, err := s.getAgent(req.SessionId)
	if err != nil {
		return nil, err
	}

	events, err := copilotAgent.GetMessages(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get messages: %v", err)
	}

	protoEvents := make([]*azdext.CopilotSessionEvent, len(events))
	for i, event := range events {
		protoEvents[i] = convertSessionEvent(event)
	}

	return &azdext.GetCopilotMessagesResponse{Events: protoEvents}, nil
}

// StopSession stops and cleans up a Copilot agent session.
func (s *copilotService) StopSession(
	ctx context.Context, req *azdext.StopCopilotSessionRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	copilotAgent, ok := s.sessions[req.SessionId]
	if ok {
		delete(s.sessions, req.SessionId)
	}
	s.mu.Unlock()

	if !ok {
		return nil, status.Errorf(codes.NotFound, "session %q not found", req.SessionId)
	}

	if err := copilotAgent.Stop(); err != nil {
		log.Printf("[copilot-service] session %q stop error: %v", req.SessionId, err)
	}
	return &azdext.EmptyResponse{}, nil
}

// getAgent retrieves a managed agent by session ID.
func (s *copilotService) getAgent(sessionID string) (agent.Agent, error) {
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	copilotAgent, ok := s.sessions[sessionID]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "session %q not found", sessionID)
	}

	return copilotAgent, nil
}

// buildAgentOptions constructs AgentOption slice from request fields.
func buildAgentOptions(
	model, reasoningEffort, systemMessage, mode string, debug, headless bool,
) []agent.AgentOption {
	opts := []agent.AgentOption{
		agent.WithHeadless(headless),
	}
	if model != "" {
		opts = append(opts, agent.WithModel(model))
	}
	if reasoningEffort != "" {
		opts = append(opts, agent.WithReasoningEffort(reasoningEffort))
	}
	if systemMessage != "" {
		opts = append(opts, agent.WithSystemMessage(systemMessage))
	}
	if mode != "" {
		opts = append(opts, agent.WithMode(agent.AgentMode(mode)))
	}
	if debug {
		opts = append(opts, agent.WithDebug(true))
	}
	return opts
}

// convertUsageMetrics converts internal UsageMetrics to the proto representation.
func convertUsageMetrics(usage agent.UsageMetrics) *azdext.CopilotUsageMetrics {
	return &azdext.CopilotUsageMetrics{
		Model:           usage.Model,
		InputTokens:     usage.InputTokens,
		OutputTokens:    usage.OutputTokens,
		TotalTokens:     usage.TotalTokens(),
		BillingRate:     usage.BillingRate,
		PremiumRequests: usage.PremiumRequests,
		DurationMs:      usage.DurationMS,
	}
}

// convertFileChanges converts internal FileChanges to the proto representation.
func convertFileChanges(changes []watch.FileChange) []*azdext.CopilotFileChange {
	if len(changes) == 0 {
		return nil
	}

	cwd, _ := os.Getwd()
	protoChanges := make([]*azdext.CopilotFileChange, len(changes))

	for i, change := range changes {
		path := change.Path
		if cwd != "" {
			if rel, err := filepath.Rel(cwd, change.Path); err == nil {
				path = rel
			}
		}
		protoChanges[i] = &azdext.CopilotFileChange{
			Path:       path,
			ChangeType: convertFileChangeType(change.ChangeType),
		}
	}

	return protoChanges
}

// convertFileChangeType converts internal FileChangeType to the proto enum.
func convertFileChangeType(ct watch.FileChangeType) azdext.CopilotFileChangeType {
	switch ct {
	case watch.FileCreated:
		return azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_CREATED
	case watch.FileModified:
		return azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_MODIFIED
	case watch.FileDeleted:
		return azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_DELETED
	default:
		return azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_UNSPECIFIED
	}
}

// convertSessionEvent converts a Copilot SDK SessionEvent to the proto representation.
// Event data is marshaled to JSON then converted to google.protobuf.Struct for
// dynamic, schema-free transport.
func convertSessionEvent(event agent.SessionEvent) *azdext.CopilotSessionEvent {
	protoEvent := &azdext.CopilotSessionEvent{
		Type:      string(event.Type),
		Timestamp: event.Timestamp.Format("2006-01-02T15:04:05.000Z"),
	}

	// Marshal event.Data to JSON, then to protobuf Struct
	jsonBytes, err := json.Marshal(event.Data)
	if err != nil {
		log.Printf("[copilot-service] failed to marshal event data: %v", err)
		return protoEvent
	}

	var dataMap map[string]any
	if err := json.Unmarshal(jsonBytes, &dataMap); err != nil {
		log.Printf("[copilot-service] failed to unmarshal event data to map: %v", err)
		return protoEvent
	}

	protoStruct, err := structpb.NewStruct(dataMap)
	if err != nil {
		log.Printf("[copilot-service] failed to create protobuf struct: %v", err)
		return protoEvent
	}

	protoEvent.Data = protoStruct
	return protoEvent
}
