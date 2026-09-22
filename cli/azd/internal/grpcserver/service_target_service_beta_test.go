// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type echoServiceTargetService struct {
	azdext.UnimplementedServiceTargetServiceServer
}

func (echoServiceTargetService) Stream(stream azdext.ServiceTargetService_StreamServer) error {
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	return stream.Send(message)
}

type echoBetaServiceTargetOverride struct{}

func (echoBetaServiceTargetOverride) Stream(stream v1beta.ServiceTargetService_StreamServer) error {
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	return stream.Send(message)
}

func TestBetaServiceTargetPreviewOverride(t *testing.T) {
	for _, test := range []struct {
		name            string
		supportsPreview bool
		customOverride  bool
	}{
		{name: "StableLifecycle"},
		{name: "PreviewRejected", supportsPreview: true},
		{name: "TypedBetaOverride", supportsPreview: true, customOverride: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newServerWithContainerService(azdext.UnimplementedContainerServiceServer{})
			server.serviceTargetService = echoServiceTargetService{}
			if test.customOverride {
				WithBetaServiceOverride(BetaServiceTargetService, echoBetaServiceTargetOverride{})(server)
			}
			info, err := server.Start()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, server.Stop()) })
			token, err := GenerateExtensionToken(&extensions.Extension{Id: "test.preview"}, info)
			require.NoError(t, err)
			connection, err := grpc.NewClient(info.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, connection.Close()) })
			stream, err := v1beta.NewServiceTargetServiceClient(connection).
				Stream(azdext.WithAccessToken(t.Context(), token))
			require.NoError(t, err)
			require.NoError(t, stream.Send(&v1beta.ServiceTargetMessage{
				RequestId: "register",
				MessageType: &v1beta.ServiceTargetMessage_RegisterServiceTargetRequest{
					RegisterServiceTargetRequest: &v1beta.RegisterServiceTargetRequest{
						Host: "custom", SupportsPreview: test.supportsPreview,
					},
				},
			}))
			response, err := stream.Recv()
			if test.supportsPreview && !test.customOverride {
				require.Equal(t, codes.Unimplemented, status.Code(err))
				require.ErrorContains(t, err, "does not implement beta service target deployment preview")
				require.Nil(t, response)
			} else {
				require.NoError(t, err)
				require.Equal(t, "register", response.RequestId)
				require.Equal(t, "custom", response.GetRegisterServiceTargetRequest().Host)
				require.Equal(t, test.supportsPreview, response.GetRegisterServiceTargetRequest().SupportsPreview)
			}
		})
	}
}
