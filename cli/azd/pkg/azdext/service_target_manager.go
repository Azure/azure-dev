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

// FrameworkServiceProvider defines the interface for framework service logic.
type FrameworkServiceProvider interface {
	Initialize(ctx context.Context, serviceConfig *ServiceConfig) error
	RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) ([]*ExternalTool, error)
	Requirements() (*FrameworkRequirements, error)
	Restore(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		progress ProgressReporter,
	) (*ServiceRestoreResult, error)
	Build(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress ProgressReporter,
	) (*ServiceBuildResult, error)
	Package(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress ProgressReporter,
	) (*ServicePackageResult, error)
}

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

// FrameworkServiceManager handles registration and request forwarding for a framework service provider.
type FrameworkServiceManager struct {
	client *AzdClient
	stream FrameworkService_StreamClient
}

// NewFrameworkServiceManager creates a new FrameworkServiceManager for an AzdClient.
func NewFrameworkServiceManager(client *AzdClient) *FrameworkServiceManager {
	return &FrameworkServiceManager{
		client: client,
	}
}

// Register registers a framework service provider with the specified language name.
func (m *FrameworkServiceManager) Register(ctx context.Context, provider FrameworkServiceProvider, language string) error {
	client := m.client.FrameworkService()
	stream, err := client.Stream(ctx)
	if err != nil {
		return err
	}

	m.stream = stream

	// Send registration request
	err = stream.Send(&FrameworkServiceMessage{
		RequestId: "register",
		MessageType: &FrameworkServiceMessage_RegisterFrameworkServiceRequest{
			RegisterFrameworkServiceRequest: &RegisterFrameworkServiceRequest{
				Language: language,
			},
		},
	})
	if err != nil {
		return err
	}

	// Wait for registration response
	resp, err := stream.Recv()
	if err != nil {
		return err
	}

	if resp.Error != nil {
		return fmt.Errorf("framework service registration error: %s", resp.Error.Message)
	}

	if resp.GetRegisterFrameworkServiceResponse() == nil {
		return fmt.Errorf("expected RegisterFrameworkServiceResponse, got %T", resp.GetMessageType())
	}

	// Start handling the framework service stream

	// Add a small delay to ensure the stream handler is ready before the server can use the stream
	ready := make(chan struct{})
	go func() {
		close(ready) // Signal that we're about to start
		m.handleFrameworkServiceStream(ctx, provider)
	}()
	<-ready // Wait for the goroutine to start

	return nil
}

// handleFrameworkServiceStream handles the bidirectional stream for framework service operations
func (m *FrameworkServiceManager) handleFrameworkServiceStream(ctx context.Context, provider FrameworkServiceProvider) {
	for {
		select {
		case <-ctx.Done():
			log.Println("Context cancelled by caller, exiting framework service stream")
			return
		default:
			msg, err := m.stream.Recv()
			if err != nil {
				log.Printf("framework service stream closed: %v", err)
				return
			}
			// Process message synchronously to avoid race condition with stream.Recv()
			resp := m.buildFrameworkServiceResponseMsg(ctx, provider, msg)
			if resp != nil {
				if err := m.stream.Send(resp); err != nil {
					log.Printf("failed to send framework service response: %v", err)
				} else {
					// Don't immediately go back to stream.Recv() - let the receiver process first
					time.Sleep(200 * time.Millisecond)
				}
			} else {
				log.Printf("buildFrameworkServiceResponseMsg returned nil response")
			}
		}
	}
}

// Close closes the framework service manager stream.
func (m *FrameworkServiceManager) Close() error {
	if m.stream != nil {
		return m.stream.CloseSend()
	}
	return nil
}

// buildFrameworkServiceResponseMsg handles individual framework service requests and builds responses
func (m *FrameworkServiceManager) buildFrameworkServiceResponseMsg(
	ctx context.Context,
	provider FrameworkServiceProvider,
	msg *FrameworkServiceMessage,
) *FrameworkServiceMessage {
	var resp *FrameworkServiceMessage
	switch r := msg.MessageType.(type) {
	case *FrameworkServiceMessage_InitializeRequest:
		initReq := r.InitializeRequest
		var serviceConfig *ServiceConfig
		if initReq != nil {
			serviceConfig = initReq.ServiceConfig
		}

		err := provider.Initialize(ctx, serviceConfig)

		resp = &FrameworkServiceMessage{
			RequestId: msg.RequestId,
			MessageType: &FrameworkServiceMessage_InitializeResponse{
				InitializeResponse: &FrameworkServiceInitializeResponse{},
			},
		}
		if err != nil {
			resp.Error = &FrameworkServiceErrorMessage{
				Message: err.Error(),
			}
		}

	case *FrameworkServiceMessage_RequiredExternalToolsRequest:

		reqReq := r.RequiredExternalToolsRequest
		var serviceConfig *ServiceConfig
		if reqReq != nil {
			serviceConfig = reqReq.ServiceConfig
		}

		tools, err := provider.RequiredExternalTools(ctx, serviceConfig)
		resp = &FrameworkServiceMessage{
			RequestId: msg.RequestId,
			MessageType: &FrameworkServiceMessage_RequiredExternalToolsResponse{
				RequiredExternalToolsResponse: &FrameworkServiceRequiredExternalToolsResponse{
					Tools: tools,
				},
			},
		}
		if err != nil {
			resp.Error = &FrameworkServiceErrorMessage{
				Message: err.Error(),
			}
		}

	case *FrameworkServiceMessage_RequirementsRequest:
		requirements, err := provider.Requirements()
		resp = &FrameworkServiceMessage{
			RequestId: msg.RequestId,
			MessageType: &FrameworkServiceMessage_RequirementsResponse{
				RequirementsResponse: &FrameworkServiceRequirementsResponse{
					Requirements: requirements,
				},
			},
		}
		if err != nil {
			resp.Error = &FrameworkServiceErrorMessage{
				Message: err.Error(),
			}
		}

	case *FrameworkServiceMessage_RestoreRequest:
		progressReporter := func(message string) {
			progressMsg := &FrameworkServiceMessage{
				RequestId: msg.RequestId,
				MessageType: &FrameworkServiceMessage_ProgressMessage{
					ProgressMessage: &FrameworkServiceProgressMessage{
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

		restoreReq := r.RestoreRequest
		var serviceConfig *ServiceConfig
		if restoreReq != nil {
			serviceConfig = restoreReq.ServiceConfig
		}

		result, err := provider.Restore(ctx, serviceConfig, progressReporter)
		resp = &FrameworkServiceMessage{
			RequestId: msg.RequestId,
			MessageType: &FrameworkServiceMessage_RestoreResponse{
				RestoreResponse: &FrameworkServiceRestoreResponse{
					RestoreResult: result,
				},
			},
		}
		if err != nil {
			resp.Error = &FrameworkServiceErrorMessage{
				Message: err.Error(),
			}
		}

	case *FrameworkServiceMessage_BuildRequest:
		progressReporter := func(message string) {
			progressMsg := &FrameworkServiceMessage{
				RequestId: msg.RequestId,
				MessageType: &FrameworkServiceMessage_ProgressMessage{
					ProgressMessage: &FrameworkServiceProgressMessage{
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

		buildReq := r.BuildRequest
		var serviceConfig *ServiceConfig
		var serviceContext *ServiceContext
		if buildReq != nil {
			serviceConfig = buildReq.ServiceConfig
			serviceContext = buildReq.ServiceContext
		}

		result, err := provider.Build(ctx, serviceConfig, serviceContext, progressReporter)
		resp = &FrameworkServiceMessage{
			RequestId: msg.RequestId,
			MessageType: &FrameworkServiceMessage_BuildResponse{
				BuildResponse: &FrameworkServiceBuildResponse{
					Result: result,
				},
			},
		}
		if err != nil {
			resp.Error = &FrameworkServiceErrorMessage{
				Message: err.Error(),
			}
		}

	case *FrameworkServiceMessage_PackageRequest:
		progressReporter := func(message string) {
			progressMsg := &FrameworkServiceMessage{
				RequestId: msg.RequestId,
				MessageType: &FrameworkServiceMessage_ProgressMessage{
					ProgressMessage: &FrameworkServiceProgressMessage{
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

		packageReq := r.PackageRequest
		var serviceConfig *ServiceConfig
		var serviceContext *ServiceContext
		if packageReq != nil {
			serviceConfig = packageReq.ServiceConfig
			serviceContext = packageReq.ServiceContext
		}

		result, err := provider.Package(ctx, serviceConfig, serviceContext, progressReporter)
		resp = &FrameworkServiceMessage{
			RequestId: msg.RequestId,
			MessageType: &FrameworkServiceMessage_PackageResponse{
				PackageResponse: &FrameworkServicePackageResponse{
					PackageResult: result,
				},
			},
		}
		if err != nil {
			resp.Error = &FrameworkServiceErrorMessage{
				Message: err.Error(),
			}
		}

	default:
		resp = &FrameworkServiceMessage{
			RequestId: msg.RequestId,
			Error: &FrameworkServiceErrorMessage{
				Message: fmt.Sprintf("unsupported message type: %T", r),
			},
		}
	}

	return resp
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
