// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext/preview"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
)

// serviceTargetPreviewManager serves experimental deployment previews on a dedicated v1beta stream.
// Ordinary service target lifecycle requests continue to use the stable ServiceTargetManager stream.
type serviceTargetPreviewManager struct {
	extensionId  string
	client       *AzdClient
	brokerLogger *log.Logger
	broker       *grpcbroker.MessageBroker[v1beta.ServiceTargetMessage]
	factories    map[string]ServiceTargetFactory
	mu           sync.RWMutex
}

func newServiceTargetPreviewManager(
	extensionId string,
	client *AzdClient,
	brokerLogger *log.Logger,
) *serviceTargetPreviewManager {
	return &serviceTargetPreviewManager{
		extensionId:  extensionId,
		client:       client,
		brokerLogger: brokerLogger,
		factories:    map[string]ServiceTargetFactory{},
	}
}

func (m *serviceTargetPreviewManager) ensureStream(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.broker != nil {
		return nil
	}

	stream, err := m.client.BetaServiceTarget().Stream(ctx)
	if err != nil {
		return fmt.Errorf("failed to create service target preview stream: %w", err)
	}

	m.broker = grpcbroker.NewMessageBroker(stream, NewServiceTargetPreviewEnvelope(), m.extensionId, m.brokerLogger)
	if err := m.broker.On(m.onPreview); err != nil {
		return fmt.Errorf("failed to register preview handler: %w", err)
	}

	return nil
}

// Register advertises deployment preview support for a host that is also registered on the stable stream.
func (m *serviceTargetPreviewManager) Register(ctx context.Context, factory ServiceTargetFactory, hostType string) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	m.factories[hostType] = factory
	m.mu.Unlock()

	resp, err := m.broker.SendAndWait(ctx, &v1beta.ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &v1beta.ServiceTargetMessage_RegisterServiceTargetRequest{
			RegisterServiceTargetRequest: &v1beta.RegisterServiceTargetRequest{
				Host:            hostType,
				SupportsPreview: true,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("service target preview registration failed: %w", err)
	}

	if resp.GetRegisterServiceTargetResponse() == nil {
		return fmt.Errorf("expected RegisterServiceTargetResponse, got %T", resp.GetMessageType())
	}

	return nil
}

func (m *serviceTargetPreviewManager) Receive(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}

	return m.broker.Run(ctx)
}

func (m *serviceTargetPreviewManager) Ready(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}

	return m.broker.Ready(ctx)
}

func (m *serviceTargetPreviewManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.broker != nil {
		m.broker.Close()
		m.broker = nil
	}

	return nil
}

// onPreview runs each preview on a fresh provider without Initialize or deployment preparation.
func (m *serviceTargetPreviewManager) onPreview(
	ctx context.Context,
	req *v1beta.ServiceTargetPreviewRequest,
) (*v1beta.ServiceTargetMessage, error) {
	serviceConfig := req.GetServiceConfig()
	if serviceConfig == nil {
		return nil, errors.New("service config is required for preview request")
	}

	m.mu.RLock()
	factory := m.factories[serviceConfig.Host]
	m.mu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("no preview factory registered for service target: %s", serviceConfig.Host)
	}

	provider, ok := factory().(preview.ServiceTargetPreviewProvider)
	if !ok {
		return nil, fmt.Errorf("service target '%s' does not support deployment preview", serviceConfig.Host)
	}

	result, err := provider.Preview(ctx, serviceConfig)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("service target '%s' returned a nil deployment preview result", serviceConfig.Host)
	}

	return &v1beta.ServiceTargetMessage{
		MessageType: &v1beta.ServiceTargetMessage_PreviewResponse{
			PreviewResponse: &v1beta.ServiceTargetPreviewResponse{Result: result},
		},
	}, nil
}
