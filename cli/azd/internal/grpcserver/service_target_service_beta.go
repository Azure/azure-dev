// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type betaServiceTargetStream = grpc.BidiStreamingServer[v1beta.ServiceTargetMessage, v1beta.ServiceTargetMessage]

// serviceTargetPreviewRegistration is the v1beta deployment preview stream for an extension-provided host.
type serviceTargetPreviewRegistration struct {
	extensionId string
	broker      *grpcbroker.MessageBroker[v1beta.ServiceTargetMessage]
}

// betaServiceTargetServiceOverride serves the dedicated v1beta deployment preview stream.
// Any other v1beta stream is forwarded to the stable service target service through the generated adapter,
// so ordinary service target lifecycle requests never depend on beta-only fields.
type betaServiceTargetServiceOverride struct {
	service *ServiceTargetService
}

var _ BetaServiceTargetServiceStreamOverride = (*betaServiceTargetServiceOverride)(nil)

func (o *betaServiceTargetServiceOverride) Stream(stream betaServiceTargetStream) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}

	replay := &replayServiceTargetStream{betaServiceTargetStream: stream, first: first}
	if !first.GetRegisterServiceTargetRequest().GetSupportsPreview() {
		return (&betaServiceTargetServiceAdapter{stable: o.service}).Stream(replay)
	}

	return o.service.previewStream(replay)
}

// replayServiceTargetStream returns an already received first message before reading from the stream.
type replayServiceTargetStream struct {
	betaServiceTargetStream
	first *v1beta.ServiceTargetMessage
}

func (s *replayServiceTargetStream) Recv() (*v1beta.ServiceTargetMessage, error) {
	if first := s.first; first != nil {
		s.first = nil
		return first, nil
	}

	return s.betaServiceTargetStream.Recv()
}

// previewStream accepts deployment preview registrations for hosts provided by the calling extension.
func (s *ServiceTargetService) previewStream(stream betaServiceTargetStream) error {
	ctx := stream.Context()
	extension, err := s.serviceTargetExtension(ctx)
	if err != nil {
		return err
	}

	broker := grpcbroker.NewMessageBroker(stream, azdext.NewServiceTargetPreviewEnvelope(), extension.Id, log.Default())

	var registeredHosts []string
	err = broker.On(func(
		ctx context.Context,
		req *v1beta.RegisterServiceTargetRequest,
	) (*v1beta.ServiceTargetMessage, error) {
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

		return &v1beta.ServiceTargetMessage{
			MessageType: &v1beta.ServiceTargetMessage_RegisterServiceTargetResponse{
				RegisterServiceTargetResponse: &v1beta.RegisterServiceTargetResponse{},
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

		betaConfig := &v1beta.ServiceConfig{}
		if err := transcodeVersionedMessage(serviceConfig, betaConfig, false); err != nil {
			return nil, err
		}

		resp, err := registration.broker.SendAndWait(ctx, &v1beta.ServiceTargetMessage{
			RequestId: uuid.NewString(),
			MessageType: &v1beta.ServiceTargetMessage_PreviewRequest{
				PreviewRequest: &v1beta.ServiceTargetPreviewRequest{ServiceConfig: betaConfig},
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
