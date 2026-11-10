// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
)

// InfraGenerationProvider writes source files and returns a replacement infrastructure entry.
// Generate must leave project configuration unchanged and honor the overwrite option.
type InfraGenerationProvider interface {
	Generate(ctx context.Context, request *v1beta.InfraGenerateRequest) (*v1beta.InfraGenerateResponse, error)
}

// InfraGenerationProviderRegistration associates a provider name with its generator.
type InfraGenerationProviderRegistration struct {
	Name     string
	Provider InfraGenerationProvider
}

type infraGenerationRegistrar interface {
	serviceReceiver
	Register(context.Context, string, InfraGenerationProvider) error
	Close() error
}

type infraGenerationManager struct {
	client *AzdClient
	id     string
	logger *log.Logger
	mu     sync.Mutex // protects stream initialization and closure
	broker *grpcbroker.MessageBroker[v1beta.InfraGenerationMessage]
	// providers routes generation and completion requests by entry.provider.
	providers *providerRegistry[InfraGenerationProvider]
}

func newInfraGenerationManager(id string, client *AzdClient, logger *log.Logger) *infraGenerationManager {
	return &infraGenerationManager{
		id: id, client: client, logger: logger,
		providers: newProviderRegistry[InfraGenerationProvider]("infrastructure generation"),
	}
}

func (m *infraGenerationManager) ensureStream(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.broker != nil {
		return nil
	}
	stream, err := m.client.InfraGeneration().Stream(ctx)
	if err != nil {
		return fmt.Errorf("opening infrastructure generation stream: %w", err)
	}
	broker := grpcbroker.NewMessageBroker(stream, &InfraGenerationEnvelope{}, m.id, m.logger)
	if err := broker.On(m.generate); err != nil {
		broker.Close()
		return err
	}
	m.broker = broker
	return nil
}

func (m *infraGenerationManager) Register(ctx context.Context, name string, provider InfraGenerationProvider) error {
	if strings.TrimSpace(name) == "" || provider == nil {
		return fmt.Errorf("infrastructure generation requires a provider name and implementation")
	}
	if err := m.ensureStream(ctx); err != nil {
		return err
	}
	return m.providers.Register(ctx, name, provider, m.advertiseProvider)
}

func (m *infraGenerationManager) advertiseProvider(ctx context.Context, name string) error {
	response, err := m.broker.SendAndWait(ctx, &v1beta.InfraGenerationMessage{
		RequestId: uuid.NewString(),
		MessageType: &v1beta.InfraGenerationMessage_RegisterRequest{
			RegisterRequest: &v1beta.RegisterInfraGenerationProviderRequest{Name: name},
		},
	})
	if err != nil {
		return err
	}
	if response == nil || response.GetRegisterResponse() == nil {
		return fmt.Errorf("expected infrastructure generation registration response")
	}
	return nil
}

func (m *infraGenerationManager) Receive(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}
	return m.broker.Run(ctx)
}

func (m *infraGenerationManager) Ready(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}
	return m.broker.Ready(ctx)
}

func (m *infraGenerationManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.broker != nil {
		m.broker.Close()
	}
	return nil
}

func (m *infraGenerationManager) provider(request *v1beta.InfraGenerateRequest) (InfraGenerationProvider, error) {
	if request == nil || request.Entry == nil || request.Options == nil {
		return nil, fmt.Errorf("generation requires an entry and a non-nil options message")
	}
	provider, ok := m.providers.Lookup(request.Entry.Provider)
	if !ok {
		return nil, fmt.Errorf("infrastructure generation provider %q is not registered", request.Entry.Provider)
	}
	return provider, nil
}

func (m *infraGenerationManager) generate(
	ctx context.Context, request *v1beta.InfraGenerateRequest,
) (*v1beta.InfraGenerationMessage, error) {
	provider, err := m.provider(request)
	if err != nil {
		return nil, err
	}
	result, err := provider.Generate(ctx, request)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Entry == nil || strings.TrimSpace(result.Entry.Provider) == "" {
		return nil, fmt.Errorf("infrastructure generator returned no replacement provider entry")
	}
	return &v1beta.InfraGenerationMessage{
		MessageType: &v1beta.InfraGenerationMessage_GenerateResponse{GenerateResponse: result},
	}, nil
}
