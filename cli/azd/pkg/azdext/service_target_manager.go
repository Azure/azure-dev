// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"io"
	"log"
	"time"

	"github.com/google/uuid"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// ProgressReporter defines a function type for reporting progress updates from extensions
type ProgressReporter func(message string)

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
		frameworkPackageOutput *ServicePackageResult,
		progress ProgressReporter,
	) (*ServicePackageResult, error)
	Publish(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		packageResult *ServicePackageResult,
		targetResource *TargetResource,
		publishOptions *PublishOptions,
		progress ProgressReporter,
	) (*ServicePublishResult, error)
	Deploy(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		packageResult *ServicePackageResult,
		publishResult *ServicePublishResult,
		targetResource *TargetResource,
		progress ProgressReporter,
	) (*ServiceDeployResult, error)
}

// ServiceTargetManager handles registration and provisioning request forwarding for a provider.
type ServiceTargetManager struct {
	client *AzdClient
	stream ServiceTargetService_StreamClient
}

// NewServiceTargetManager creates a new ServiceTargetManager for an AzdClient.
func NewServiceTargetManager(client *AzdClient) *ServiceTargetManager {
	return &ServiceTargetManager{
		client: client,
	}
}

// Close terminates the underlying gRPC stream if it's been initialized.
func (m *ServiceTargetManager) Close() error {
	if m.stream != nil {
		return m.stream.CloseSend()
	}

	return nil
}

// Register registers the provider with the server, waits for the response,
// then starts background handling of provisioning requests.
func (m *ServiceTargetManager) Register(ctx context.Context, provider ServiceTargetProvider, hostType string) error {
	stream, err := m.client.ServiceTarget().Stream(ctx)
	if err != nil {
		return err
	}

	m.stream = stream

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
		go m.handleServiceTargetStream(ctx, provider)
		return nil
	}

	return status.Errorf(codes.FailedPrecondition, "expected RegisterProviderResponse, got %T", msg.GetMessageType())
}

func (m *ServiceTargetManager) handleServiceTargetStream(ctx context.Context, provider ServiceTargetProvider) {
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
				resp := buildServiceTargetResponseMsg(ctx, provider, msg, m.stream)
				if resp != nil {
					if err := m.stream.Send(resp); err != nil {
						log.Printf("failed to send service target response: %v", err)
					}
				}
			}(msg)
		}
	}
}

func buildServiceTargetResponseMsg(
	ctx context.Context,
	provider ServiceTargetProvider,
	msg *ServiceTargetMessage,
	stream ServiceTargetService_StreamClient,
) *ServiceTargetMessage {
	var resp *ServiceTargetMessage
	switch r := msg.MessageType.(type) {
	case *ServiceTargetMessage_InitializeRequest:
		initReq := r.InitializeRequest
		var serviceConfig *ServiceConfig
		if initReq != nil {
			serviceConfig = initReq.ServiceConfig
		}

		err := provider.Initialize(ctx, serviceConfig)
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
			if err := stream.Send(progressMsg); err != nil {
				log.Printf("failed to send progress message: %v", err)
			}
		}

		result, err := provider.Package(
			ctx,
			r.PackageRequest.ServiceConfig,
			r.PackageRequest.FrameworkPackage,
			progressReporter,
		)
		resp = &ServiceTargetMessage{
			RequestId: msg.RequestId,
			MessageType: &ServiceTargetMessage_PackageResponse{
				PackageResponse: &ServiceTargetPackageResponse{PackageResult: result},
			},
		}
		if err != nil {
			resp.Error = &ServiceTargetErrorMessage{
				Message: err.Error(),
			}
		}
	case *ServiceTargetMessage_PublishRequest:
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
			if err := stream.Send(progressMsg); err != nil {
				log.Printf("failed to send progress message: %v", err)
			}
		}

		result, err := provider.Publish(
			ctx,
			r.PublishRequest.ServiceConfig,
			r.PublishRequest.ServicePackage,
			r.PublishRequest.TargetResource,
			r.PublishRequest.PublishOptions,
			progressReporter,
		)
		resp = &ServiceTargetMessage{
			RequestId: msg.RequestId,
			MessageType: &ServiceTargetMessage_PublishResponse{
				PublishResponse: &ServiceTargetPublishResponse{PublishResult: result},
			},
		}
		if err != nil {
			resp.Error = &ServiceTargetErrorMessage{
				Message: err.Error(),
			}
		}
	case *ServiceTargetMessage_DeployRequest:
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
			if err := stream.Send(progressMsg); err != nil {
				log.Printf("failed to send progress message: %v", err)
			}
		}

		result, err := provider.Deploy(
			ctx,
			r.DeployRequest.ServiceConfig,
			r.DeployRequest.ServicePackage,
			r.DeployRequest.ServicePublish,
			r.DeployRequest.TargetResource,
			progressReporter,
		)
		resp = &ServiceTargetMessage{
			RequestId: msg.RequestId,
			MessageType: &ServiceTargetMessage_DeployResponse{
				DeployResponse: &ServiceTargetDeployResponse{DeployResult: result},
			},
		}
		if err != nil {
			resp.Error = &ServiceTargetErrorMessage{
				Message: err.Error(),
			}
		}
	case *ServiceTargetMessage_EndpointsRequest:
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
