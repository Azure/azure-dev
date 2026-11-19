// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
)

// BetaInputHandler contributes options for a missing string input. Returning nil declines the input.
// Handlers must respect cancellation and must not prompt, persist values, or provision resources.
type BetaInputHandler func(context.Context, *v1beta.InputDefinition) (*v1beta.ResolveInputResponse, error)

type betaInputRegistration struct {
	kinds   []string
	handler BetaInputHandler
}

type betaInputManager struct {
	client      *AzdClient
	extensionID string
	logger      *log.Logger
	mu          sync.Mutex
	broker      *grpcbroker.MessageBroker[v1beta.InputMessage]
	handlersMu  sync.RWMutex
	handlers    map[string]BetaInputHandler
}

func newBetaInputManager(client *AzdClient, extensionID string, logger *log.Logger) *betaInputManager {
	return &betaInputManager{
		client: client, extensionID: extensionID, logger: logger,
		handlers: make(map[string]BetaInputHandler),
	}
}

func (m *betaInputManager) ensureStream(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.broker != nil {
		return nil
	}
	stream, err := m.client.InputsBeta().Stream(ctx)
	if err != nil {
		return fmt.Errorf("opening beta input stream: %w", err)
	}
	broker := grpcbroker.NewMessageBroker(stream, NewBetaInputEnvelope(), m.extensionID, m.logger)
	if err := broker.On(m.resolve); err != nil {
		broker.Close()
		return err
	}
	m.broker = broker
	return nil
}

func (m *betaInputManager) Receive(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}
	return m.broker.Run(ctx)
}

func (m *betaInputManager) Ready(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}
	return m.broker.Ready(ctx)
}

func (m *betaInputManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.broker != nil {
		m.broker.Close()
	}
	return nil
}

func (m *betaInputManager) Register(ctx context.Context, registration betaInputRegistration) error {
	if registration.handler == nil || len(registration.kinds) == 0 {
		return errors.New("an input handler and at least one semantic kind are required")
	}
	if err := m.ensureStream(ctx); err != nil {
		return err
	}
	m.handlersMu.Lock()
	seen := make(map[string]bool)
	for _, kind := range registration.kinds {
		if strings.TrimSpace(kind) == "" || seen[kind] || m.handlers[kind] != nil {
			m.handlersMu.Unlock()
			return fmt.Errorf("empty or duplicate input kind %q", kind)
		}
		seen[kind] = true
	}
	for _, kind := range registration.kinds {
		m.handlers[kind] = registration.handler
	}
	m.handlersMu.Unlock()

	response, err := m.broker.SendAndWait(ctx, &v1beta.InputMessage{
		RequestId: uuid.NewString(),
		MessageType: &v1beta.InputMessage_RegisterInputProviderRequest{
			RegisterInputProviderRequest: &v1beta.RegisterInputProviderRequest{Kinds: slices.Clone(registration.kinds)},
		},
	})
	if err == nil && response.GetRegisterInputProviderResponse() == nil {
		err = errors.New("expected input provider registration acknowledgment")
	}
	if err != nil {
		m.handlersMu.Lock()
		for _, kind := range registration.kinds {
			delete(m.handlers, kind)
		}
		m.handlersMu.Unlock()
		return fmt.Errorf("registering input provider: %w", err)
	}
	return nil
}

func (m *betaInputManager) resolve(
	ctx context.Context, request *v1beta.ResolveInputRequest,
) (*v1beta.InputMessage, error) {
	if request.GetInput() == nil {
		return nil, errors.New("input definition is required")
	}
	m.handlersMu.RLock()
	handler := m.handlers[request.Input.Kind]
	m.handlersMu.RUnlock()
	if handler == nil {
		return nil, fmt.Errorf("no input handler for kind %q", request.Input.Kind)
	}
	response, err := handler(ctx, request.Input)
	if err != nil {
		return nil, err
	}
	if response == nil {
		response = &v1beta.ResolveInputResponse{}
	}
	return &v1beta.InputMessage{
		MessageType: &v1beta.InputMessage_ResolveInputResponse{
			ResolveInputResponse: response,
		},
	}, nil
}
