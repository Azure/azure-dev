// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"io"
	"log"

	"github.com/google/uuid"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// ProvisioningProvider defines the interface for provisioning logic, matching the core Provider interface.
type ProvisioningProvider interface {
	Name(ctx context.Context) (string, error)
	Initialize(ctx context.Context, projectPath string, options *ProvisioningOptions) error
	State(ctx context.Context, options *ProvisioningStateOptions) (*ProvisioningStateResult, error)
	Deploy(ctx context.Context) (*ProvisioningDeployResult, error)
	Preview(ctx context.Context) (*ProvisioningDeployPreviewResult, error)
	Destroy(ctx context.Context, options *ProvisioningDestroyOptions) (*ProvisioningDestroyResult, error)
	Parameters(ctx context.Context) ([]*ProvisioningParameter, error)
}

// ProvisioningManager handles registration and provisioning request forwarding for a provider.
type ProvisioningManager struct {
	client *AzdClient
	stream ProvisioningService_StreamClient
}

// NewProvisioningManager creates a new ProvisioningManager for an AzdClient.
func NewProvisioningManager(client *AzdClient) *ProvisioningManager {
	return &ProvisioningManager{
		client: client,
	}
}

// Register registers the provider with the server, waits for the response, then starts background handling of provisioning requests.
func (m *ProvisioningManager) Register(ctx context.Context, provider ProvisioningProvider, name string, displayName string) error {
	stream, err := m.client.Provisioning().Stream(ctx)
	if err != nil {
		return err
	}

	m.stream = stream

	registerReq := &ProvisioningMessage{
		RequestId: uuid.NewString(),
		MessageType: &ProvisioningMessage_RegisterProviderRequest{
			RegisterProviderRequest: &RegisterProviderRequest{
				Name:        name,
				DisplayName: displayName,
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

	initResponse := msg.GetInitializeResponse()
	if initResponse == nil {
		go m.handleProvisioningStream(provider)
		return nil
	}

	return status.Errorf(codes.FailedPrecondition, "expected InitializeResponse, got %T", msg.GetMessageType())
}

func (m *ProvisioningManager) handleProvisioningStream(provider ProvisioningProvider) {
	for {
		msg, err := m.stream.Recv()
		if err != nil {
			log.Printf("provisioning stream closed: %v", err)
			return
		}
		go func(msg *ProvisioningMessage) {
			resp := buildProvisioningResponseMsg(provider, msg)
			if resp != nil {
				if err := m.stream.Send(resp); err != nil {
					log.Printf("failed to send provisioning response: %v", err)
				}
			}
		}(msg)
	}
}

func buildProvisioningResponseMsg(provider ProvisioningProvider, msg *ProvisioningMessage) *ProvisioningMessage {
	ctx := context.Background()
	var resp *ProvisioningMessage
	switch r := msg.MessageType.(type) {
	case *ProvisioningMessage_NameRequest:
		name, _ := provider.Name(ctx)
		resp = &ProvisioningMessage{
			RequestId: msg.RequestId,
			MessageType: &ProvisioningMessage_NameResponse{
				NameResponse: &NameResponse{Name: name},
			},
		}
	case *ProvisioningMessage_InitializeRequest:
		err := provider.Initialize(ctx, r.InitializeRequest.ProjectPath, r.InitializeRequest.Options)
		resp = &ProvisioningMessage{
			RequestId: msg.RequestId,
			MessageType: &ProvisioningMessage_InitializeResponse{
				InitializeResponse: &InitializeResponse{Success: err == nil, ErrorMessage: errorString(err)},
			},
		}
	case *ProvisioningMessage_StateRequest:
		result, err := provider.State(ctx, r.StateRequest.Options)
		resp = &ProvisioningMessage{
			RequestId: msg.RequestId,
			MessageType: &ProvisioningMessage_StateResponse{
				StateResponse: &StateResponse{StateResult: result, ErrorMessage: errorString(err)},
			},
		}
	case *ProvisioningMessage_DeployRequest:
		result, err := provider.Deploy(ctx)
		resp = &ProvisioningMessage{
			RequestId: msg.RequestId,
			MessageType: &ProvisioningMessage_DeployResult{
				DeployResult: &DeployResultResponse{Result: result, ErrorMessage: errorString(err)},
			},
		}
	case *ProvisioningMessage_PreviewRequest:
		result, err := provider.Preview(ctx)
		resp = &ProvisioningMessage{
			RequestId: msg.RequestId,
			MessageType: &ProvisioningMessage_PreviewResult{
				PreviewResult: &PreviewResultResponse{Result: result, ErrorMessage: errorString(err)},
			},
		}
	case *ProvisioningMessage_DestroyRequest:
		result, err := provider.Destroy(ctx, r.DestroyRequest.Options)
		resp = &ProvisioningMessage{
			RequestId: msg.RequestId,
			MessageType: &ProvisioningMessage_DestroyResult{
				DestroyResult: &DestroyResultResponse{Result: result, ErrorMessage: errorString(err)},
			},
		}
	case *ProvisioningMessage_ParametersRequest:
		params, err := provider.Parameters(ctx)
		resp = &ProvisioningMessage{
			RequestId: msg.RequestId,
			MessageType: &ProvisioningMessage_ParametersResponse{
				ParametersResponse: &ParametersResponse{Parameters: params, ErrorMessage: errorString(err)},
			},
		}
	default:
		log.Printf("Unknown or unhandled provisioning message type: %T", r)
	}
	return resp
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
