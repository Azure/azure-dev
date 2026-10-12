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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestServiceTargetService_Preview(t *testing.T) {
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

	newStream := func(t *testing.T) *grpcbroker.MessageBroker[v1beta.ServiceTargetMessage] {
		stream, err := v1beta.NewServiceTargetServiceClient(connection).Stream(ctx)
		require.NoError(t, err)
		broker := grpcbroker.NewMessageBroker(stream, azdext.NewServiceTargetPreviewEnvelope(), extension.Id, nil)
		go func() { _ = broker.Run(ctx) }()
		require.NoError(t, broker.Ready(ctx))
		return broker
	}
	register := func(
		broker *grpcbroker.MessageBroker[v1beta.ServiceTargetMessage],
		host string,
		supportsPreview bool,
	) error {
		_, err := broker.SendAndWait(ctx, &v1beta.ServiceTargetMessage{
			RequestId: uuid.NewString(),
			MessageType: &v1beta.ServiceTargetMessage_RegisterServiceTargetRequest{
				RegisterServiceTargetRequest: &v1beta.RegisterServiceTargetRequest{
					Host:            host,
					SupportsPreview: supportsPreview,
				},
			},
		})
		return err
	}

	data, err := structpb.NewStruct(map[string]any{"action": "update"})
	require.NoError(t, err)
	received := make(chan *v1beta.ServiceTargetPreviewRequest, 2)
	previewBroker := newStream(t)
	require.NoError(t, previewBroker.On(func(
		ctx context.Context,
		req *v1beta.ServiceTargetPreviewRequest,
	) (*v1beta.ServiceTargetMessage, error) {
		received <- req
		if req.GetServiceConfig().GetName() == "broken" {
			return nil, errors.New("remote lookup failed")
		}
		return &v1beta.ServiceTargetMessage{
			MessageType: &v1beta.ServiceTargetMessage_PreviewResponse{
				PreviewResponse: &v1beta.ServiceTargetPreviewResponse{
					Result: &v1beta.ServiceDeployPreviewResult{Message: "1 change", Data: data},
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

	t.Run("OrdinaryBetaRegistrationUsesStableService", func(t *testing.T) {
		require.NoError(t, register(newStream(t), "ordinary", false))

		service.providerMapMu.Lock()
		defer service.providerMapMu.Unlock()
		require.Contains(t, service.providerMap, "ordinary")
		require.NotContains(t, service.previewMap, "ordinary")
	})
}
