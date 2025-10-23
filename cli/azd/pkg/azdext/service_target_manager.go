// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/google/uuid"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// ProgressReporter defines a function type for reporting progress updates from extensions
type ProgressReporter func(message string)

var (
	ServiceTargetFactoryKey = func(config *ServiceConfig) string {
		return string(config.Host)
	}
)

// ServiceTargetProvider defines the interface for service target logic.
type ServiceTargetProvider interface {
	Initialize(ctx context.Context, serviceConfig *ServiceConfig) error
	Endpoints(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		targetResource *TargetResource,
	) ([]string, error)
	GetTargetResource(
		ctx context.Context,
		subscriptionId string,
		serviceConfig *ServiceConfig,
		defaultResolver func() (*TargetResource, error),
	) (*TargetResource, error)
	Package(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress ProgressReporter,
	) (*ServicePackageResult, error)
	Publish(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		targetResource *TargetResource,
		publishOptions *PublishOptions,
		progress ProgressReporter,
	) (*ServicePublishResult, error)
	Deploy(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		targetResource *TargetResource,
		progress ProgressReporter,
	) (*ServiceDeployResult, error)
}

// ServiceTargetManager handles registration and provisioning request forwarding for a provider.
type ServiceTargetManager struct {
	client           *AzdClient
	stream           ServiceTargetService_StreamClient
	componentManager *ComponentManager[ServiceTargetProvider]
}

// NewServiceTargetManager creates a new ServiceTargetManager for an AzdClient.
func NewServiceTargetManager(client *AzdClient) *ServiceTargetManager {
	return &ServiceTargetManager{
		client:           client,
		componentManager: NewComponentManager[ServiceTargetProvider](ServiceTargetFactoryKey, "service target"),
	}
}

// Close terminates the underlying gRPC stream if it's been initialized.
func (m *ServiceTargetManager) Close() error {
	if m.stream != nil {
		if err := m.stream.CloseSend(); err != nil {
			return err
		}
	}

	return m.componentManager.Close()
}

// Register registers the provider with the server, waits for the response,
// then starts background handling of provisioning requests.
func (m *ServiceTargetManager) Register(ctx context.Context, factory ServiceTargetFactory, hostType string) error {
	stream, err := m.client.ServiceTarget().Stream(ctx)
	if err != nil {
		return err
	}

	m.stream = stream
	m.componentManager.RegisterFactory(hostType, factory)

	registerReq := &ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &ServiceTargetMessage_RegisterServiceTargetRequest{
			RegisterServiceTargetRequest: &RegisterServiceTargetRequest{
				Host: hostType,
			},
		},
	}
	if err := m.stream.Send(registerReq); err != nil {
		return err
	}

	msg, err := m.stream.Recv()
	if errors.Is(err, io.EOF) {
		log.Println("Stream closed by client")
		return nil
	}
	if err != nil {
		return err
	}

	regResponse := msg.GetRegisterServiceTargetResponse()
	if regResponse != nil {
		go m.handleServiceTargetStream(ctx)
		return nil
	}

	return status.Errorf(codes.FailedPrecondition, "expected RegisterProviderResponse, got %T", msg.GetMessageType())
}

func (m *ServiceTargetManager) handleServiceTargetStream(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			log.Println("Context cancelled by caller, exiting service target stream")
			return
		default:
			msg, err := m.stream.Recv()
			if err != nil {
				log.Printf("service target stream closed: %v", err)
				return
			}
			go func(msg *ServiceTargetMessage) {
				resp := m.buildServiceTargetResponseMsg(ctx, msg)
				if resp != nil {
					if err := m.stream.Send(resp); err != nil {
						log.Printf("failed to send service target response: %v", err)
					}
				}
			}(msg)
		}
	}
}

func (m *ServiceTargetManager) buildServiceTargetResponseMsg(
	ctx context.Context,
	msg *ServiceTargetMessage,
) *ServiceTargetMessage {
	var resp *ServiceTargetMessage
	switch r := msg.MessageType.(type) {
	case *ServiceTargetMessage_InitializeRequest:
		initReq := r.InitializeRequest
		var serviceConfig *ServiceConfig
		if initReq != nil {
			serviceConfig = initReq.ServiceConfig
		}

		if serviceConfig == nil {
			resp = &ServiceTargetMessage{
				RequestId: msg.RequestId,
				Error: &ServiceTargetErrorMessage{
					Message: "service config is required for initialize request",
				},
			}
			return resp
		}

		// Create new instance using componentManager
		_, err := m.componentManager.GetOrCreateInstance(ctx, serviceConfig)
		resp = &ServiceTargetMessage{
			RequestId: msg.RequestId,
			MessageType: &ServiceTargetMessage_InitializeResponse{
				InitializeResponse: &ServiceTargetInitializeResponse{},
			},
		}
		if err != nil {
			resp.Error = &ServiceTargetErrorMessage{
				Message: err.Error(),
			}
		}
	case *ServiceTargetMessage_GetTargetResourceRequest:
		serviceConfig := r.GetTargetResourceRequest.ServiceConfig
		if serviceConfig == nil {
			resp = &ServiceTargetMessage{
				RequestId: msg.RequestId,
				Error: &ServiceTargetErrorMessage{
					Message: "service config is required for get target resource request",
				},
			}
			return resp
		}

		provider, err := m.componentManager.GetInstance(serviceConfig.Name)
		if err != nil {
			resp = &ServiceTargetMessage{
				RequestId: msg.RequestId,
				Error: &ServiceTargetErrorMessage{
					Message: fmt.Sprintf("no provider instance found for service: %s. Initialize must be called first",
						serviceConfig.Name),
				},
			}
			return resp
		}

		// Create a callback that returns the default target resource or error
		defaultResolver := func() (*TargetResource, error) {
			// Check if default resolution had an error
			if r.GetTargetResourceRequest.DefaultError != "" {
				return nil, errors.New(r.GetTargetResourceRequest.DefaultError)
			}
			// Return the default target resource (may be nil if not computed)
			defaultTarget := r.GetTargetResourceRequest.DefaultTargetResource
			return defaultTarget, nil
		}

		result, err := provider.GetTargetResource(
			ctx,
			r.GetTargetResourceRequest.SubscriptionId,
			r.GetTargetResourceRequest.ServiceConfig,
			defaultResolver,
		)
		resp = &ServiceTargetMessage{
			RequestId: msg.RequestId,
			MessageType: &ServiceTargetMessage_GetTargetResourceResponse{
				GetTargetResourceResponse: &GetTargetResourceResponse{TargetResource: result},
			},
		}
		if err != nil {
			resp.Error = &ServiceTargetErrorMessage{
				Message: err.Error(),
			}
		}
	case *ServiceTargetMessage_PackageRequest:
		serviceConfig := r.PackageRequest.ServiceConfig
		if serviceConfig == nil {
			resp = &ServiceTargetMessage{
				RequestId: msg.RequestId,
				Error: &ServiceTargetErrorMessage{
					Message: "service config is required for package request",
				},
			}
			return resp
		}

		provider, err := m.componentManager.GetInstance(serviceConfig.Name)
		if err != nil {
			resp = &ServiceTargetMessage{
				RequestId: msg.RequestId,
				Error: &ServiceTargetErrorMessage{
					Message: fmt.Sprintf("no provider instance found for service: %s. Initialize must be called first",
						serviceConfig.Name),
				},
			}
			return resp
		}

		progressReporter := func(message string) {
			progressMsg := &ServiceTargetMessage{
				RequestId: msg.RequestId,
				MessageType: &ServiceTargetMessage_ProgressMessage{
					ProgressMessage: &ServiceTargetProgressMessage{
						RequestId: msg.RequestId,
						Message:   message,
						Timestamp: time.Now().UnixMilli(),
					},
				},
			}
			if err := m.stream.Send(progressMsg); err != nil {
				log.Printf("failed to send progress message: %v", err)
			}
		}

		result, err := provider.Package(
			ctx,
			r.PackageRequest.ServiceConfig,
			r.PackageRequest.ServiceContext,
			progressReporter,
		)
		resp = &ServiceTargetMessage{
			RequestId: msg.RequestId,
			MessageType: &ServiceTargetMessage_PackageResponse{
				PackageResponse: &ServiceTargetPackageResponse{Result: result},
			},
		}
		if err != nil {
			resp.Error = &ServiceTargetErrorMessage{
				Message: err.Error(),
			}
		}
	case *ServiceTargetMessage_PublishRequest:
		serviceConfig := r.PublishRequest.ServiceConfig
		if serviceConfig == nil {
			resp = &ServiceTargetMessage{
				RequestId: msg.RequestId,
				Error: &ServiceTargetErrorMessage{
					Message: "service config is required for publish request",
				},
			}
			return resp
		}

		provider, err := m.componentManager.GetInstance(serviceConfig.Name)
		if err != nil {
			resp = &ServiceTargetMessage{
				RequestId: msg.RequestId,
				Error: &ServiceTargetErrorMessage{
					Message: fmt.Sprintf("no provider instance found for service: %s. Initialize must be called first",
						serviceConfig.Name),
				},
			}
			return resp
		}

		progressReporter := func(message string) {
			progressMsg := &ServiceTargetMessage{
				RequestId: msg.RequestId,
				MessageType: &ServiceTargetMessage_ProgressMessage{
					ProgressMessage: &ServiceTargetProgressMessage{
						RequestId: msg.RequestId,
						Message:   message,
						Timestamp: time.Now().UnixMilli(),
					},
				},
			}
			if err := m.stream.Send(progressMsg); err != nil {
				log.Printf("failed to send progress message: %v", err)
			}
		}

		result, err := provider.Publish(
			ctx,
			r.PublishRequest.ServiceConfig,
			r.PublishRequest.ServiceContext,
			r.PublishRequest.TargetResource,
			r.PublishRequest.PublishOptions,
			progressReporter,
		)
		resp = &ServiceTargetMessage{
			RequestId: msg.RequestId,
			MessageType: &ServiceTargetMessage_PublishResponse{
				PublishResponse: &ServiceTargetPublishResponse{Result: result},
			},
		}
		if err != nil {
			resp.Error = &ServiceTargetErrorMessage{
				Message: err.Error(),
			}
		}
	case *ServiceTargetMessage_DeployRequest:
		serviceConfig := r.DeployRequest.ServiceConfig
		if serviceConfig == nil {
			resp = &ServiceTargetMessage{
				RequestId: msg.RequestId,
				Error: &ServiceTargetErrorMessage{
					Message: "service config is required for deploy request",
				},
			}
			return resp
		}

		provider, err := m.componentManager.GetInstance(serviceConfig.Name)
		if err != nil {
			resp = &ServiceTargetMessage{
				RequestId: msg.RequestId,
				Error: &ServiceTargetErrorMessage{
					Message: fmt.Sprintf("no provider instance found for service: %s. Initialize must be called first",
						serviceConfig.Name),
				},
			}
			return resp
		}

		// Create a progress reporter that sends progress messages back to core
		progressReporter := func(message string) {
			progressMsg := &ServiceTargetMessage{
				RequestId: msg.RequestId,
				MessageType: &ServiceTargetMessage_ProgressMessage{
					ProgressMessage: &ServiceTargetProgressMessage{
						RequestId: msg.RequestId,
						Message:   message,
						Timestamp: time.Now().UnixMilli(),
					},
				},
			}
			if err := m.stream.Send(progressMsg); err != nil {
				log.Printf("failed to send progress message: %v", err)
			}
		}

		result, err := provider.Deploy(
			ctx,
			r.DeployRequest.ServiceConfig,
			r.DeployRequest.ServiceContext,
			r.DeployRequest.TargetResource,
			progressReporter,
		)
		resp = &ServiceTargetMessage{
			RequestId: msg.RequestId,
			MessageType: &ServiceTargetMessage_DeployResponse{
				DeployResponse: &ServiceTargetDeployResponse{Result: result},
			},
		}
		if err != nil {
			resp.Error = &ServiceTargetErrorMessage{
				Message: err.Error(),
			}
		}
	case *ServiceTargetMessage_EndpointsRequest:
		serviceConfig := r.EndpointsRequest.ServiceConfig
		if serviceConfig == nil {
			resp = &ServiceTargetMessage{
				RequestId: msg.RequestId,
				Error: &ServiceTargetErrorMessage{
					Message: "service config is required for endpoints request",
				},
			}
			return resp
		}

		provider, err := m.componentManager.GetInstance(serviceConfig.Name)
		if err != nil {
			resp = &ServiceTargetMessage{
				RequestId: msg.RequestId,
				Error: &ServiceTargetErrorMessage{
					Message: fmt.Sprintf("no provider instance found for service: %s. Initialize must be called first",
						serviceConfig.Name),
				},
			}
			return resp
		}

		endpoints, err := provider.Endpoints(
			ctx,
			r.EndpointsRequest.ServiceConfig,
			r.EndpointsRequest.TargetResource,
		)
		resp = &ServiceTargetMessage{
			RequestId: msg.RequestId,
			MessageType: &ServiceTargetMessage_EndpointsResponse{
				EndpointsResponse: &ServiceTargetEndpointsResponse{Endpoints: endpoints},
			},
		}
		if err != nil {
			resp.Error = &ServiceTargetErrorMessage{
				Message: err.Error(),
			}
		}
	default:
		log.Printf("Unknown or unhandled service target message type: %T", r)
	}
	return resp
}
