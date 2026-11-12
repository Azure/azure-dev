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

// serviceTargetPreviewManager serves deployment previews on a dedicated stream.
// Ordinary service target lifecycle requests continue to use the stable ServiceTargetManager stream.
type serviceTargetPreviewManager struct {
	extensionId  string
	client       *AzdClient
	brokerLogger *log.Logger
	broker       *grpcbroker.MessageBroker[ServiceTargetMessage]
	factories    map[string]ServiceTargetFactory
	beta         bool
	mu           sync.RWMutex
}

func newBetaServiceTargetPreviewManager(
	extensionId string,
	client *AzdClient,
	brokerLogger *log.Logger,
) *serviceTargetPreviewManager {
	manager := newServiceTargetPreviewManager(extensionId, client, brokerLogger)
	manager.beta = true
	return manager
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

	var stream grpcbroker.BidiStream[ServiceTargetMessage]
	var err error
	if m.beta {
		betaStream, streamErr := m.client.BetaServiceTarget().Stream(ctx)
		err = streamErr
		if err == nil {
			stream = &betaServiceTargetPreviewStream{BidiStream: betaStream}
		}
	} else {
		stream, err = m.client.ServiceTarget().Stream(ctx)
	}
	if err != nil {
		return fmt.Errorf("failed to create service target preview stream: %w", err)
	}

	m.broker = grpcbroker.NewMessageBroker(stream, NewServiceTargetEnvelope(), m.extensionId, m.brokerLogger)
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

	resp, err := m.broker.SendAndWait(ctx, &ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &ServiceTargetMessage_RegisterServiceTargetRequest{
			RegisterServiceTargetRequest: &RegisterServiceTargetRequest{
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
	req *ServiceTargetPreviewRequest,
) (*ServiceTargetMessage, error) {
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

	instance := factory()
	var provider ServiceTargetPreviewProvider
	if m.beta {
		//nolint:staticcheck // Honor the retained beta registration's provider interface.
		if betaProvider, ok := instance.(preview.ServiceTargetPreviewProvider); ok {
			provider = &betaServiceTargetPreviewProvider{provider: betaProvider}
		}
	} else {
		provider, _ = instance.(ServiceTargetPreviewProvider)
	}
	if provider == nil {
		return nil, fmt.Errorf("service target '%s' does not support deployment preview", serviceConfig.Host)
	}

	result, err := provider.Preview(ctx, serviceConfig)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("service target '%s' returned a nil deployment preview result", serviceConfig.Host)
	}

	return &ServiceTargetMessage{
		MessageType: &ServiceTargetMessage_PreviewResponse{
			PreviewResponse: &ServiceTargetPreviewResponse{Result: result},
		},
	}, nil
}

// betaServiceTargetPreviewStream preserves the deprecated beta wire route while sharing stable dispatch.
type betaServiceTargetPreviewStream struct {
	grpcbroker.BidiStream[v1beta.ServiceTargetMessage]
}

func (s *betaServiceTargetPreviewStream) Send(message *ServiceTargetMessage) error {
	betaMessage := &v1beta.ServiceTargetMessage{}
	if err := transcodeServiceTargetMessage(message, betaMessage); err != nil {
		return fmt.Errorf("encoding beta service target preview message: %w", err)
	}
	return s.BidiStream.Send(betaMessage)
}

func (s *betaServiceTargetPreviewStream) Recv() (*ServiceTargetMessage, error) {
	betaMessage, err := s.BidiStream.Recv()
	if err != nil {
		return nil, err
	}
	message := &ServiceTargetMessage{}
	if err := transcodeServiceTargetMessage(betaMessage, message); err != nil {
		return nil, fmt.Errorf("decoding beta service target preview message: %w", err)
	}
	return message, nil
}

// betaServiceTargetPreviewProvider preserves the deprecated provider interface without duplicating preview dispatch.
type betaServiceTargetPreviewProvider struct {
	provider preview.ServiceTargetPreviewProvider //nolint:staticcheck // Retain beta provider compatibility.
}

func (p *betaServiceTargetPreviewProvider) Preview(
	ctx context.Context,
	config *ServiceConfig,
) (*ServiceDeployPreviewResult, error) {
	betaConfig := &v1beta.ServiceConfig{}
	if err := transcodeServiceTargetMessage(config, betaConfig); err != nil {
		return nil, fmt.Errorf("encoding beta preview service config: %w", err)
	}
	betaResult, err := p.provider.Preview(ctx, betaConfig)
	if err != nil || betaResult == nil {
		return nil, err
	}
	result := &ServiceDeployPreviewResult{}
	if err := transcodeServiceTargetMessage(betaResult, result); err != nil {
		return nil, fmt.Errorf("decoding beta deployment preview result: %w", err)
	}
	return result, nil
}
