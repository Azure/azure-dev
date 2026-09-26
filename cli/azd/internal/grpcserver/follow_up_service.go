// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"strings"
	"sync"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	errFollowUpInvocationNotFound = errors.New("follow-up invocation not found")
	errFollowUpExtensionMismatch  = errors.New("follow-up extension mismatch")
	errFollowUpNotAllowed         = errors.New("follow-up is not allowed")
	errFollowUpInvocationClosed   = errors.New("follow-up invocation is closed")
)

type followUpInvocation struct {
	mu          sync.Mutex
	extensionID string
	allow       bool
	closed      bool
	hasText     bool
	text        string
}

type followUpManager struct {
	mu          sync.Mutex
	invocations map[string]*followUpInvocation
}

// NewFollowUpManager creates the invocation-scoped follow-up store.
func NewFollowUpManager() *followUpManager {
	return &followUpManager{
		invocations: make(map[string]*followUpInvocation),
	}
}

func (m *followUpManager) Begin(extensionID, eventName string) string {
	invocationID := uuid.NewString()
	m.mu.Lock()
	m.invocations[invocationID] = &followUpInvocation{
		extensionID: extensionID,
		allow:       strings.HasPrefix(eventName, "post"),
	}
	m.mu.Unlock()
	return invocationID
}

func (m *followUpManager) Set(
	invocationID,
	extensionID,
	text string,
) error {
	m.mu.Lock()
	invocation, ok := m.invocations[invocationID]
	if !ok {
		m.mu.Unlock()
		return errFollowUpInvocationNotFound
	}
	invocation.mu.Lock()
	m.mu.Unlock()
	defer invocation.mu.Unlock()

	if invocation.extensionID != extensionID {
		return errFollowUpExtensionMismatch
	}
	if invocation.closed {
		return errFollowUpInvocationClosed
	}
	if !invocation.allow {
		return errFollowUpNotAllowed
	}

	invocation.hasText = true
	invocation.text = text
	return nil
}

func (m *followUpManager) Commit(invocationID string) (string, bool) {
	return m.finish(invocationID, true)
}

func (m *followUpManager) Discard(invocationID string) {
	_, _ = m.finish(invocationID, false)
}

func (m *followUpManager) finish(invocationID string, commit bool) (string, bool) {
	m.mu.Lock()
	invocation, ok := m.invocations[invocationID]
	if !ok {
		m.mu.Unlock()
		return "", false
	}
	delete(m.invocations, invocationID)
	invocation.mu.Lock()
	m.mu.Unlock()
	defer invocation.mu.Unlock()

	invocation.closed = true
	if !commit {
		return "", false
	}
	return invocation.text, invocation.hasText
}

type followUpService struct {
	v1beta.UnimplementedFollowUpServiceServer
	manager *followUpManager
}

// NewFollowUpService creates the host follow-up contribution service.
func NewFollowUpService(manager *followUpManager) v1beta.FollowUpServiceServer {
	return &followUpService{manager: manager}
}

func (s *followUpService) SetFollowUp(
	ctx context.Context,
	req *v1beta.SetFollowUpRequest,
) (*v1beta.SetFollowUpResponse, error) {
	if req == nil || req.InvocationId == "" {
		return nil, status.Error(codes.InvalidArgument, "invocation_id is required")
	}

	claims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "extension claims are required")
	}

	if err := s.manager.Set(req.InvocationId, claims.Subject, req.Text); err != nil {
		switch {
		case errors.Is(err, errFollowUpExtensionMismatch):
			return nil, status.Error(codes.PermissionDenied, "invocation belongs to another extension")
		case errors.Is(err, errFollowUpInvocationNotFound),
			errors.Is(err, errFollowUpInvocationClosed):
			return nil, status.Error(codes.FailedPrecondition, "invocation is no longer active")
		case errors.Is(err, errFollowUpNotAllowed):
			return nil, status.Error(
				codes.FailedPrecondition,
				"follow-up contributions are only allowed for project post handlers",
			)
		default:
			return nil, status.Error(codes.Internal, "failed to set follow-up")
		}
	}

	return &v1beta.SetFollowUpResponse{}, nil
}
