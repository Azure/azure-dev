// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// serviceTargetPreviewRegistration is the deployment preview stream for an extension-provided host.
type serviceTargetPreviewRegistration struct {
	extensionId string
	broker      *grpcbroker.MessageBroker[azdext.ServiceTargetMessage]
}

// replayServiceTargetStream returns an already received first message before reading from the stream.
type replayServiceTargetStream struct {
	azdext.ServiceTargetService_StreamServer
	first    *azdext.ServiceTargetMessage
	firstErr error
}

func (s *replayServiceTargetStream) Recv() (*azdext.ServiceTargetMessage, error) {
	if err := s.firstErr; err != nil {
		s.firstErr = nil
		return nil, err
	}
	if first := s.first; first != nil {
		s.first = nil
		return first, nil
	}

	return s.ServiceTargetService_StreamServer.Recv()
}

// previewStream accepts deployment preview registrations for hosts provided by the calling extension.
func (s *ServiceTargetService) previewStream(
	stream azdext.ServiceTargetService_StreamServer,
	extension *extensions.Extension,
) error {
	ctx := stream.Context()
	broker := grpcbroker.NewMessageBroker(stream, azdext.NewServiceTargetEnvelope(), extension.Id, log.Default())

	var registeredHosts []string
	err := broker.On(func(
		ctx context.Context,
		req *azdext.RegisterServiceTargetRequest,
	) (*azdext.ServiceTargetMessage, error) {
		hostType := req.GetHost()
		if !req.GetSupportsPreview() {
			return nil, status.Errorf(
				codes.InvalidArgument, "service target preview registration for %s must set supports_preview", hostType)
		}

		s.providerMapMu.Lock()
		defer s.providerMapMu.Unlock()

		if _, has := s.previewMap[hostType]; has {
			return nil, status.Errorf(codes.AlreadyExists, "preview provider %s already registered", hostType)
		}

		s.previewMap[hostType] = &serviceTargetPreviewRegistration{extensionId: extension.Id, broker: broker}
		registeredHosts = append(registeredHosts, hostType)
		log.Printf("Registered service target preview: %s", hostType)

		return &azdext.ServiceTargetMessage{
			MessageType: &azdext.ServiceTargetMessage_RegisterServiceTargetResponse{
				RegisterServiceTargetResponse: &azdext.RegisterServiceTargetResponse{},
			},
		}, nil
	})
	if err != nil {
		return fmt.Errorf("failed to register handler: %w", err)
	}

	runErr := broker.Run(ctx)

	s.providerMapMu.Lock()
	for _, hostType := range registeredHosts {
		delete(s.previewMap, hostType)
	}
	s.providerMapMu.Unlock()

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		log.Printf("Preview broker error for extension %s: %v", extension.Id, runErr)
		return fmt.Errorf("broker error: %w", runErr)
	}

	return nil
}

// previewFunc forwards deployment previews for hostType to the preview stream registered by the same extension.
func (s *ServiceTargetService) previewFunc(hostType string, extensionId string) project.ExternalPreviewFunc {
	return func(ctx context.Context, serviceConfig *azdext.ServiceConfig) (*project.ServiceDeployPreviewResult, error) {
		s.providerMapMu.Lock()
		registration := s.previewMap[hostType]
		s.providerMapMu.Unlock()

		if registration == nil || registration.extensionId != extensionId {
			return nil, project.ErrDeployPreviewNotSupported
		}

		resp, err := registration.broker.SendAndWait(ctx, &azdext.ServiceTargetMessage{
			RequestId: uuid.NewString(),
			MessageType: &azdext.ServiceTargetMessage_PreviewRequest{
				PreviewRequest: &azdext.ServiceTargetPreviewRequest{ServiceConfig: serviceConfig},
			},
		})
		if err != nil {
			return nil, err
		}

		result := resp.GetPreviewResponse().GetResult()
		if result == nil {
			return nil, errors.New("invalid preview response: missing preview result")
		}

		return &project.ServiceDeployPreviewResult{
			Message: result.GetMessage(),
			Data:    result.GetData().AsMap(),
		}, nil
	}
}
