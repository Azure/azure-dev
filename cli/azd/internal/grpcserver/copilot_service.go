// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/watch"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// sessionCounter generates unique session handles without external dependencies.
var sessionCounter atomic.Int64

// managedCopilotSession tracks an active Copilot agent session and its associated resources.
type managedCopilotSession struct {
	agent           *agent.CopilotAgent
	watcher         watch.Watcher
	watchCancel     context.CancelFunc
	resumeSessionID string // SDK session ID for resumption in SendMessage
	workingDir      string // working directory for this session
}

// copilotService implements the CopilotServiceServer gRPC interface.
// It manages agent sessions, delegating to CopilotAgent for all operations.
type copilotService struct {
	azdext.UnimplementedCopilotServiceServer

	agentFactory *agent.CopilotAgentFactory

	mu       sync.RWMutex
	sessions map[string]*managedCopilotSession
}

// NewCopilotService creates a new CopilotService gRPC server.
func NewCopilotService(agentFactory *agent.CopilotAgentFactory) azdext.CopilotServiceServer {
	return &copilotService{
		agentFactory: agentFactory,
		sessions:     make(map[string]*managedCopilotSession),
	}
}

// CreateSession creates a new Copilot agent session with the given configuration.
func (s *copilotService) CreateSession(
	ctx context.Context, req *azdext.CreateCopilotSessionRequest,
) (*azdext.CreateCopilotSessionResponse, error) {
	opts := buildAgentOptions(
		req.Model, req.ReasoningEffort, req.SystemMessage, req.Mode, req.Debug, req.Headless,
	)

	copilotAgent, err := s.agentFactory.Create(ctx, opts...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create copilot agent: %v", err)
	}

	cwd := req.WorkingDirectory
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	watchCtx, watchCancel := context.WithCancel(context.Background()) //nolint:gosec // cancel stored in session
	watcher, watchErr := watch.NewWatcher(watchCtx)
	if watchErr != nil {
		log.Printf("[copilot-service] file watcher unavailable: %v", watchErr)
	}

	sessionHandle := fmt.Sprintf("copilot-%d", sessionCounter.Add(1))

	s.mu.Lock()
	s.sessions[sessionHandle] = &managedCopilotSession{
		agent:       copilotAgent,
		watcher:     watcher,
		watchCancel: watchCancel,
		workingDir:  cwd,
	}
	s.mu.Unlock()

	return &azdext.CreateCopilotSessionResponse{
		SessionId: sessionHandle,
	}, nil
}

// ResumeSession resumes an existing Copilot session by ID.
func (s *copilotService) ResumeSession(
	ctx context.Context, req *azdext.ResumeCopilotSessionRequest,
) (*azdext.ResumeCopilotSessionResponse, error) {
	if req.SessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}

	opts := buildAgentOptions(
		req.Model, req.ReasoningEffort, req.SystemMessage, req.Mode, req.Debug, req.Headless,
	)

	copilotAgent, err := s.agentFactory.Create(ctx, opts...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create copilot agent: %v", err)
	}

	watchCtx, watchCancel := context.WithCancel(context.Background()) //nolint:gosec // cancel stored in session
	watcher, watchErr := watch.NewWatcher(watchCtx)
	if watchErr != nil {
		log.Printf("[copilot-service] file watcher unavailable: %v", watchErr)
	}

	sessionHandle := fmt.Sprintf("copilot-%d", sessionCounter.Add(1))

	s.mu.Lock()
	s.sessions[sessionHandle] = &managedCopilotSession{
		agent:           copilotAgent,
		watcher:         watcher,
		watchCancel:     watchCancel,
		resumeSessionID: req.SessionId,
	}
	s.mu.Unlock()

	return &azdext.ResumeCopilotSessionResponse{
		SessionId: sessionHandle,
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
			return nil, status.Errorf(codes.Internal, "failed to get working directory: %v", err)
		}
	}

	// Create a temporary agent to list sessions
	tempAgent, err := s.agentFactory.Create(ctx, agent.WithHeadless(true))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create agent for listing sessions: %v", err)
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

	return &azdext.ListCopilotSessionsResponse{
		Sessions: protoSessions,
	}, nil
}

// Initialize performs first-run configuration for a session.
func (s *copilotService) Initialize(
	ctx context.Context, req *azdext.InitializeCopilotRequest,
) (*azdext.InitializeCopilotResponse, error) {
	managed, err := s.getSession(req.SessionId)
	if err != nil {
		return nil, err
	}

	// Apply model/reasoning overrides if provided
	if req.Model != "" {
		agent.WithModel(req.Model)(managed.agent)
	}
	if req.ReasoningEffort != "" {
		agent.WithReasoningEffort(req.ReasoningEffort)(managed.agent)
	}

	result, err := managed.agent.Initialize(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to initialize agent: %v", err)
	}

	return &azdext.InitializeCopilotResponse{
		Model:           result.Model,
		ReasoningEffort: result.ReasoningEffort,
		IsFirstRun:      result.IsFirstRun,
	}, nil
}

// SendMessage sends a prompt to the agent and blocks until processing completes.
func (s *copilotService) SendMessage(
	ctx context.Context, req *azdext.SendCopilotMessageRequest,
) (*azdext.SendCopilotMessageResponse, error) {
	managed, err := s.getSession(req.SessionId)
	if err != nil {
		return nil, err
	}

	if req.Prompt == "" {
		return nil, status.Error(codes.InvalidArgument, "prompt cannot be empty")
	}

	// Pass the resume session ID if this session was created via ResumeSession
	var sendOpts []agent.SendOption
	if managed.resumeSessionID != "" {
		sendOpts = append(sendOpts, agent.WithSessionID(managed.resumeSessionID))
		// Clear after first use — subsequent calls reuse the same session
		managed.resumeSessionID = ""
	}

	result, err := managed.agent.SendMessage(ctx, req.Prompt, sendOpts...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "copilot agent error: %v", err)
	}

	return &azdext.SendCopilotMessageResponse{
		SessionId: req.SessionId,
		Usage:     convertUsageMetrics(result.Usage),
	}, nil
}

// GetUsageMetrics returns cumulative usage metrics for a session.
func (s *copilotService) GetUsageMetrics(
	ctx context.Context, req *azdext.GetCopilotUsageMetricsRequest,
) (*azdext.GetCopilotUsageMetricsResponse, error) {
	managed, err := s.getSession(req.SessionId)
	if err != nil {
		return nil, err
	}

	return &azdext.GetCopilotUsageMetricsResponse{
		Usage: convertUsageMetrics(managed.agent.GetCumulativeUsage()),
	}, nil
}

// GetFileChanges returns files created, modified, or deleted during the session.
func (s *copilotService) GetFileChanges(
	ctx context.Context, req *azdext.GetCopilotFileChangesRequest,
) (*azdext.GetCopilotFileChangesResponse, error) {
	managed, err := s.getSession(req.SessionId)
	if err != nil {
		return nil, err
	}

	if managed.watcher == nil {
		return &azdext.GetCopilotFileChangesResponse{}, nil
	}

	changes := managed.watcher.GetFileChanges()
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

	return &azdext.GetCopilotFileChangesResponse{
		FileChanges: protoChanges,
	}, nil
}

// StopSession stops and cleans up a Copilot agent session.
func (s *copilotService) StopSession(
	ctx context.Context, req *azdext.StopCopilotSessionRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	managed, ok := s.sessions[req.SessionId]
	if ok {
		delete(s.sessions, req.SessionId)
	}
	s.mu.Unlock()

	if !ok {
		return nil, status.Errorf(codes.NotFound, "session %q not found", req.SessionId)
	}

	if managed.watchCancel != nil {
		managed.watchCancel()
	}
	managed.agent.Stop()

	return &azdext.EmptyResponse{}, nil
}

// getSession retrieves a managed session by its handle.
func (s *copilotService) getSession(sessionID string) (*managedCopilotSession, error) {
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	managed, ok := s.sessions[sessionID]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "session %q not found", sessionID)
	}

	return managed, nil
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
