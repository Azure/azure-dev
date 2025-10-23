// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"log"
	"time"
)

var (
	FrameworkServiceFactoryKey = func(config *ServiceConfig) string {
		return string(config.Language)
	}
)

// FrameworkServiceProvider defines the interface for framework service logic.
type FrameworkServiceProvider interface {
	Initialize(ctx context.Context, serviceConfig *ServiceConfig) error
	RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) ([]*ExternalTool, error)
	Requirements() (*FrameworkRequirements, error)
	Restore(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
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

// FrameworkServiceManager handles registration and request forwarding for a framework service provider.
type FrameworkServiceManager struct {
	client           *AzdClient
	stream           FrameworkService_StreamClient
	componentManager *ComponentManager[FrameworkServiceProvider]
}

// NewFrameworkServiceManager creates a new FrameworkServiceManager for an AzdClient.
func NewFrameworkServiceManager(client *AzdClient) *FrameworkServiceManager {
	return &FrameworkServiceManager{
		client:           client,
		componentManager: NewComponentManager[FrameworkServiceProvider](FrameworkServiceFactoryKey, "framework service"),
	}
}

// Register registers a framework service provider with the specified language name.
func (m *FrameworkServiceManager) Register(
	ctx context.Context,
	factory FrameworkServiceFactory,
	language string,
) error {
	client := m.client.FrameworkService()
	stream, err := client.Stream(ctx)
	if err != nil {
		return err
	}

	m.stream = stream
	m.componentManager.RegisterFactory(language, factory)

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
		m.handleFrameworkServiceStream(ctx)
	}()
	<-ready // Wait for the goroutine to start

	return nil
}

// handleFrameworkServiceStream handles the bidirectional stream for framework service operations
func (m *FrameworkServiceManager) handleFrameworkServiceStream(ctx context.Context) {
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
			resp := m.buildFrameworkServiceResponseMsg(ctx, msg)
			if resp != nil {
				if err := m.stream.Send(resp); err != nil {
					log.Printf("failed to send framework service response: %v", err)
				}
			}
		}
	}
}

// Close closes the framework service manager stream.
func (m *FrameworkServiceManager) Close() error {
	if m.stream != nil {
		if err := m.stream.CloseSend(); err != nil {
			return err
		}
	}
	return m.componentManager.Close()
}

// buildFrameworkServiceResponseMsg handles individual framework service requests and builds responses
func (m *FrameworkServiceManager) buildFrameworkServiceResponseMsg(
	ctx context.Context,
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

		if serviceConfig == nil {
			resp = &FrameworkServiceMessage{
				RequestId: msg.RequestId,
				Error: &FrameworkServiceErrorMessage{
					Message: "service config is required for initialize request",
				},
			}
			return resp
		}

		// Create new instance using baseManager
		_, err := m.componentManager.GetOrCreateInstance(ctx, serviceConfig)

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

		if serviceConfig == nil {
			resp = &FrameworkServiceMessage{
				RequestId: msg.RequestId,
				Error: &FrameworkServiceErrorMessage{
					Message: "service config is required for required external tools request",
				},
			}
			return resp
		}

		provider, err := m.componentManager.GetInstance(serviceConfig.Name)
		if err != nil {
			resp = &FrameworkServiceMessage{
				RequestId: msg.RequestId,
				Error: &FrameworkServiceErrorMessage{
					Message: fmt.Sprintf("no provider instance found for service: %s. Initialize must be called first",
						serviceConfig.Name),
				},
			}
			return resp
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
		// Requirements don't depend on a specific service, so we can use any available instance
		provider, err := m.componentManager.GetAnyInstance()
		if err != nil {
			resp = &FrameworkServiceMessage{
				RequestId: msg.RequestId,
				Error: &FrameworkServiceErrorMessage{
					Message: "no provider instances available. Initialize must be called first",
				},
			}
			return resp
		}

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
		restoreReq := r.RestoreRequest
		var serviceConfig *ServiceConfig
		var serviceContext *ServiceContext
		if restoreReq != nil {
			serviceConfig = restoreReq.ServiceConfig
			serviceContext = restoreReq.ServiceContext
		}

		if serviceConfig == nil {
			resp = &FrameworkServiceMessage{
				RequestId: msg.RequestId,
				Error: &FrameworkServiceErrorMessage{
					Message: "service config is required for restore request",
				},
			}
			return resp
		}

		provider, err := m.componentManager.GetInstance(serviceConfig.Name)
		if err != nil {
			resp = &FrameworkServiceMessage{
				RequestId: msg.RequestId,
				Error: &FrameworkServiceErrorMessage{
					Message: fmt.Sprintf("no provider instance found for service: %s. Initialize must be called first",
						serviceConfig.Name),
				},
			}
			return resp
		}

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

		result, err := provider.Restore(ctx, serviceConfig, serviceContext, progressReporter)
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
		buildReq := r.BuildRequest
		var serviceConfig *ServiceConfig
		var serviceContext *ServiceContext
		if buildReq != nil {
			serviceConfig = buildReq.ServiceConfig
			serviceContext = buildReq.ServiceContext
		}

		if serviceConfig == nil {
			resp = &FrameworkServiceMessage{
				RequestId: msg.RequestId,
				Error: &FrameworkServiceErrorMessage{
					Message: "service config is required for build request",
				},
			}
			return resp
		}

		provider, err := m.componentManager.GetInstance(serviceConfig.Name)
		if err != nil {
			resp = &FrameworkServiceMessage{
				RequestId: msg.RequestId,
				Error: &FrameworkServiceErrorMessage{
					Message: fmt.Sprintf("no provider instance found for service: %s. Initialize must be called first",
						serviceConfig.Name),
				},
			}
			return resp
		}

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
		packageReq := r.PackageRequest
		var serviceConfig *ServiceConfig
		var serviceContext *ServiceContext
		if packageReq != nil {
			serviceConfig = packageReq.ServiceConfig
			serviceContext = packageReq.ServiceContext
		}

		if serviceConfig == nil {
			resp = &FrameworkServiceMessage{
				RequestId: msg.RequestId,
				Error: &FrameworkServiceErrorMessage{
					Message: "service config is required for package request",
				},
			}
			return resp
		}

		provider, err := m.componentManager.GetInstance(serviceConfig.Name)
		if err != nil {
			resp = &FrameworkServiceMessage{
				RequestId: msg.RequestId,
				Error: &FrameworkServiceErrorMessage{
					Message: fmt.Sprintf("no provider instance found for service: %s. Initialize must be called first",
						serviceConfig.Name),
				},
			}
			return resp
		}

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
