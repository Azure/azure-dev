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
	"google.golang.org/protobuf/proto"
)

// BetaServiceTargetManager exposes the high-level service target provider API over the beta contract.
type BetaServiceTargetManager struct {
	extensionID string
	client      *AzdClient
	handler     *ServiceTargetManager
	broker      *grpcbroker.MessageBroker[v1beta.ServiceTargetMessage]
	logger      *log.Logger
	mu          sync.RWMutex
}

// NewBetaServiceTargetManager creates a beta service target manager.
func NewBetaServiceTargetManager(
	extensionID string,
	client *AzdClient,
	brokerLogger *log.Logger,
) *BetaServiceTargetManager {
	return &BetaServiceTargetManager{
		extensionID: extensionID,
		client:      client,
		handler: &ServiceTargetManager{
			componentManager: NewComponentManager[ServiceTargetProvider](ServiceTargetFactoryKey, "service target"),
		},
		logger: brokerLogger,
	}
}

// Close terminates the beta stream and closes provider instances.
func (m *BetaServiceTargetManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.broker != nil {
		m.broker.Close()
		m.broker = nil
	}
	return m.handler.componentManager.Close()
}

func (m *BetaServiceTargetManager) ensureStream(ctx context.Context) error {
	m.mu.RLock()
	if m.broker != nil {
		m.mu.RUnlock()
		return nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.broker != nil {
		return nil
	}

	stream, err := m.client.BetaServiceTarget().Stream(ctx)
	if err != nil {
		return fmt.Errorf("failed to create beta service target stream: %w", err)
	}
	m.broker = grpcbroker.NewMessageBroker(stream, NewBetaServiceTargetEnvelope(), m.extensionID, m.logger)

	handlers := []struct {
		name    string
		handler any
	}{
		{"initialize", m.onInitialize},
		{"get target resource", m.onGetTargetResource},
		{"package", m.onPackage},
		{"publish", m.onPublish},
		{"deploy", m.onDeploy},
		{"preview", m.onPreview},
		{"endpoints", m.onEndpoints},
	}
	for _, entry := range handlers {
		if err := m.broker.On(entry.handler); err != nil {
			return fmt.Errorf("failed to register %s handler: %w", entry.name, err)
		}
	}
	return nil
}

// Register registers a preview-capable provider through the beta service target contract.
func (m *BetaServiceTargetManager) Register(
	ctx context.Context,
	factory ServiceTargetFactory,
	hostType string,
) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}
	m.handler.componentManager.RegisterFactory(hostType, factory)

	response, err := m.broker.SendAndWait(ctx, &v1beta.ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &v1beta.ServiceTargetMessage_RegisterServiceTargetRequest{
			RegisterServiceTargetRequest: &v1beta.RegisterServiceTargetRequest{
				Host:            hostType,
				SupportsPreview: true,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("service target registration failed: %w", err)
	}
	if response.GetRegisterServiceTargetResponse() == nil {
		return fmt.Errorf("expected RegisterServiceTargetResponse, got %T", response.GetMessageType())
	}
	return nil
}

// Receive runs the beta message broker.
func (m *BetaServiceTargetManager) Receive(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}
	return m.broker.Run(ctx)
}

// Ready waits until the beta message broker is receiving messages.
func (m *BetaServiceTargetManager) Ready(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}
	return m.broker.Ready(ctx)
}

func convertServiceTargetMessage(source, destination proto.Message) error {
	wire, err := proto.Marshal(source)
	if err != nil {
		return err
	}
	return proto.Unmarshal(wire, destination)
}

func betaRequest[T proto.Message](request proto.Message, destination T) (T, error) {
	if err := convertServiceTargetMessage(request, destination); err != nil {
		var zero T
		return zero, err
	}
	return destination, nil
}

func betaResponse(response *ServiceTargetMessage, err error) (*v1beta.ServiceTargetMessage, error) {
	if err != nil {
		return nil, err
	}
	result := new(v1beta.ServiceTargetMessage)
	if err := convertServiceTargetMessage(response, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (m *BetaServiceTargetManager) onInitialize(
	ctx context.Context,
	request *v1beta.ServiceTargetInitializeRequest,
) (*v1beta.ServiceTargetMessage, error) {
	stable, err := betaRequest(request, new(ServiceTargetInitializeRequest))
	if err != nil {
		return nil, err
	}
	return betaResponse(m.handler.onInitialize(ctx, stable))
}

func (m *BetaServiceTargetManager) onGetTargetResource(
	ctx context.Context,
	request *v1beta.GetTargetResourceRequest,
) (*v1beta.ServiceTargetMessage, error) {
	stable, err := betaRequest(request, new(GetTargetResourceRequest))
	if err != nil {
		return nil, err
	}
	return betaResponse(m.handler.onGetTargetResource(ctx, stable))
}

func (m *BetaServiceTargetManager) onPackage(
	ctx context.Context,
	request *v1beta.ServiceTargetPackageRequest,
	progress grpcbroker.ProgressFunc,
) (*v1beta.ServiceTargetMessage, error) {
	stable, err := betaRequest(request, new(ServiceTargetPackageRequest))
	if err != nil {
		return nil, err
	}
	return betaResponse(m.handler.onPackage(ctx, stable, progress))
}

func (m *BetaServiceTargetManager) onPublish(
	ctx context.Context,
	request *v1beta.ServiceTargetPublishRequest,
	progress grpcbroker.ProgressFunc,
) (*v1beta.ServiceTargetMessage, error) {
	stable, err := betaRequest(request, new(ServiceTargetPublishRequest))
	if err != nil {
		return nil, err
	}
	return betaResponse(m.handler.onPublish(ctx, stable, progress))
}

func (m *BetaServiceTargetManager) onDeploy(
	ctx context.Context,
	request *v1beta.ServiceTargetDeployRequest,
	progress grpcbroker.ProgressFunc,
) (*v1beta.ServiceTargetMessage, error) {
	stable, err := betaRequest(request, new(ServiceTargetDeployRequest))
	if err != nil {
		return nil, err
	}
	return betaResponse(m.handler.onDeploy(ctx, stable, progress))
}

func (m *BetaServiceTargetManager) onPreview(
	ctx context.Context,
	request *v1beta.ServiceTargetPreviewRequest,
) (*v1beta.ServiceTargetMessage, error) {
	if request.GetServiceConfig() == nil {
		return nil, errors.New("service config is required for preview request")
	}
	serviceConfig, err := betaRequest(request.GetServiceConfig(), new(ServiceConfig))
	if err != nil {
		return nil, err
	}
	provider, err := m.handler.componentManager.CreateInstance(serviceConfig)
	if err != nil {
		return nil, err
	}
	previewProvider, ok := provider.(preview.ServiceTargetPreviewProvider)
	if !ok {
		return nil, fmt.Errorf("service target '%s' does not support deployment preview", serviceConfig.Host)
	}
	result, err := previewProvider.Preview(ctx, request.GetServiceConfig())
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("service target '%s' returned a nil deployment preview result", serviceConfig.Host)
	}
	return &v1beta.ServiceTargetMessage{
		MessageType: &v1beta.ServiceTargetMessage_PreviewResponse{
			PreviewResponse: &v1beta.ServiceTargetPreviewResponse{
				Result: result,
			},
		},
	}, nil
}

func (m *BetaServiceTargetManager) onEndpoints(
	ctx context.Context,
	request *v1beta.ServiceTargetEndpointsRequest,
) (*v1beta.ServiceTargetMessage, error) {
	stable, err := betaRequest(request, new(ServiceTargetEndpointsRequest))
	if err != nil {
		return nil, err
	}
	return betaResponse(m.handler.onEndpoints(ctx, stable))
}
