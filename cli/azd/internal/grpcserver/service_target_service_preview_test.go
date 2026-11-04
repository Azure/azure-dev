// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestServiceTargetService_Preview(t *testing.T) {
	for _, beta := range []bool{false, true} {
		t.Run(map[bool]string{false: "Stable", true: "DeprecatedBeta"}[beta], func(t *testing.T) {
			testServiceTargetPreview(t, beta)
		})
	}
}

func testServiceTargetPreview(t *testing.T, beta bool) {
	t.Helper()

	extension := &extensions.Extension{
		Id:           "test.preview",
		Capabilities: []extensions.CapabilityType{extensions.ServiceTargetProviderCapability},
	}
	service := NewServiceTargetService(
		ioc.NewNestedContainer(nil),
		newStreamTestExtensionManager(t, extension),
		nil,
	).(*ServiceTargetService)

	server := newServerWithContainerService(azdext.UnimplementedContainerServiceServer{})
	server.serviceTargetService = service
	info, err := server.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Stop()) })

	token, err := GenerateExtensionToken(extension, info)
	require.NoError(t, err)
	connection, err := grpc.NewClient(info.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })

	ctx, cancel := context.WithCancel(azdext.WithAccessToken(t.Context(), token))
	t.Cleanup(cancel)

	newStream := func(t *testing.T) *grpcbroker.MessageBroker[azdext.ServiceTargetMessage] {
		var stream grpcbroker.BidiStream[azdext.ServiceTargetMessage]
		if beta {
			betaStream, err := v1beta.NewServiceTargetServiceClient(connection).Stream(ctx)
			require.NoError(t, err)
			stream = &betaPreviewTestStream{BidiStream: betaStream}
		} else {
			stableStream, err := azdext.NewServiceTargetServiceClient(connection).Stream(ctx)
			require.NoError(t, err)
			stream = stableStream
		}
		broker := grpcbroker.NewMessageBroker(stream, azdext.NewServiceTargetEnvelope(), extension.Id, nil)
		go func() { _ = broker.Run(ctx) }()
		require.NoError(t, broker.Ready(ctx))
		return broker
	}
	register := func(
		broker *grpcbroker.MessageBroker[azdext.ServiceTargetMessage],
		host string,
		supportsPreview bool,
	) error {
		_, err := broker.SendAndWait(ctx, &azdext.ServiceTargetMessage{
			RequestId: uuid.NewString(),
			MessageType: &azdext.ServiceTargetMessage_RegisterServiceTargetRequest{
				RegisterServiceTargetRequest: &azdext.RegisterServiceTargetRequest{
					Host:            host,
					SupportsPreview: supportsPreview,
				},
			},
		})
		return err
	}

	data, err := structpb.NewStruct(map[string]any{"action": "update"})
	require.NoError(t, err)
	received := make(chan *azdext.ServiceTargetPreviewRequest, 4)
	previewBroker := newStream(t)
	require.NoError(t, previewBroker.On(func(
		ctx context.Context,
		req *azdext.ServiceTargetPreviewRequest,
	) (*azdext.ServiceTargetMessage, error) {
		received <- req
		if req.GetServiceConfig().GetName() == "broken" {
			return nil, errors.New("remote lookup failed")
		}
		if req.GetServiceConfig().GetName() == "missing-result" {
			return &azdext.ServiceTargetMessage{
				MessageType: &azdext.ServiceTargetMessage_PreviewResponse{
					PreviewResponse: &azdext.ServiceTargetPreviewResponse{},
				},
			}, nil
		}
		return &azdext.ServiceTargetMessage{
			MessageType: &azdext.ServiceTargetMessage_PreviewResponse{
				PreviewResponse: &azdext.ServiceTargetPreviewResponse{
					Result: &azdext.ServiceDeployPreviewResult{Message: "1 change", Data: data},
				},
			},
		}, nil
	}))
	require.NoError(t, register(previewBroker, "custom", true))

	t.Run("ForwardsPreview", func(t *testing.T) {
		result, err := service.previewFunc("custom", extension.Id)(ctx, &azdext.ServiceConfig{Name: "api", Host: "custom"})
		require.NoError(t, err)
		require.Equal(t, "1 change", result.Message)
		require.Equal(t, map[string]any{"action": "update"}, result.Data)
		require.Equal(t, "api", (<-received).GetServiceConfig().GetName())
	})

	t.Run("ProviderError", func(t *testing.T) {
		_, err := service.previewFunc("custom", extension.Id)(ctx, &azdext.ServiceConfig{Name: "broken", Host: "custom"})
		require.ErrorContains(t, err, "remote lookup failed")
	})

	t.Run("MissingResult", func(t *testing.T) {
		_, err := service.previewFunc("custom", extension.Id)(
			ctx, &azdext.ServiceConfig{Name: "missing-result", Host: "custom"})
		require.ErrorContains(t, err, "invalid preview response: missing preview result")
	})

	t.Run("NotRegistered", func(t *testing.T) {
		for _, preview := range []project.ExternalPreviewFunc{
			service.previewFunc("other", extension.Id),
			service.previewFunc("custom", "another.extension"),
		} {
			_, err := preview(ctx, &azdext.ServiceConfig{Name: "api"})
			require.ErrorIs(t, err, project.ErrDeployPreviewNotSupported)
		}
	})

	t.Run("DuplicateRejected", func(t *testing.T) {
		err := register(newStream(t), "custom", true)
		require.ErrorContains(t, err, "preview provider custom already registered")
	})

	t.Run("OrdinaryRegistrationUsesStableService", func(t *testing.T) {
		require.NoError(t, register(newStream(t), "ordinary", false))

		service.providerMapMu.Lock()
		defer service.providerMapMu.Unlock()
		require.Contains(t, service.providerMap, "ordinary")
		require.NotContains(t, service.previewMap, "ordinary")
	})

	t.Run("FrozenLegacyRoute", func(t *testing.T) {
		newLegacyStream := func() *grpc.GenericClientStream[azdext.ServiceTargetMessage, azdext.ServiceTargetMessage] {
			raw, err := connection.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true, ClientStreams: true},
				"/azdext.ServiceTargetService/Stream")
			require.NoError(t, err)
			return &grpc.GenericClientStream[azdext.ServiceTargetMessage, azdext.ServiceTargetMessage]{ClientStream: raw}
		}

		broker := grpcbroker.NewMessageBroker(newLegacyStream(), azdext.NewServiceTargetEnvelope(), extension.Id, nil)
		go func() { _ = broker.Run(ctx) }()
		require.NoError(t, broker.Ready(ctx))
		require.NoError(t, register(broker, "legacy", false))

		stream := newLegacyStream()
		require.NoError(t, stream.Send(&azdext.ServiceTargetMessage{
			MessageType: &azdext.ServiceTargetMessage_RegisterServiceTargetRequest{
				RegisterServiceTargetRequest: &azdext.RegisterServiceTargetRequest{
					Host: "legacy-preview", SupportsPreview: true,
				},
			},
		}))
		_, err := stream.Recv()
		require.Equal(t, codes.Unimplemented, status.Code(err))

		service.providerMapMu.Lock()
		defer service.providerMapMu.Unlock()
		require.Contains(t, service.providerMap, "legacy")
		require.NotContains(t, service.previewMap, "legacy")
		require.NotContains(t, service.previewMap, "legacy-preview")
	})
}

type betaPreviewTestStream struct {
	grpcbroker.BidiStream[v1beta.ServiceTargetMessage]
}

func (s *betaPreviewTestStream) Send(message *azdext.ServiceTargetMessage) error {
	betaMessage := &v1beta.ServiceTargetMessage{}
	if err := transcodeVersionedMessage(message, betaMessage, false); err != nil {
		return err
	}
	return s.BidiStream.Send(betaMessage)
}

func (s *betaPreviewTestStream) Recv() (*azdext.ServiceTargetMessage, error) {
	betaMessage, err := s.BidiStream.Recv()
	if err != nil {
		return nil, err
	}
	message := &azdext.ServiceTargetMessage{}
	if err := transcodeVersionedMessage(betaMessage, message, false); err != nil {
		return nil, err
	}
	return message, nil
}
