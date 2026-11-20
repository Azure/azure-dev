// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
)

func TestAddProviderRoutesThroughBetaSDK(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	extension := &extensions.Extension{
		Id: "test.add", Capabilities: []extensions.CapabilityType{extensions.AddProviderCapability},
	}
	manager := newStreamTestExtensionManager(t, extension)
	installed, err := manager.GetInstalled(extensions.FilterOptions{Id: extension.Id})
	require.NoError(t, err)
	server := newTestServer(azdext.UnimplementedContainerServiceServer{}, NewExtensionService(manager))
	registry := NewAddProviderRegistry()
	server.SetAddProviderRegistry(registry)
	info, err := server.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Stop()) })
	require.NotContains(t, server.grpcServer.GetServiceInfo(), "azd.extensions.v1.AddProviderService")
	token, err := GenerateExtensionToken(extension, info)
	require.NoError(t, err)
	conn, err := grpc.NewClient(info.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	rpc := v1beta.NewAddProviderServiceClient(conn)
	unauthenticated, err := rpc.Stream(ctx)
	require.NoError(t, err)
	_, err = unauthenticated.Recv()
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	auth := azdext.WithAccessToken(ctx, token)
	client, err := azdext.NewAzdClient(azdext.WithAddress(info.Address))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	entered := make(chan string, 1)
	host := azdext.NewExtensionHost(client).WithBetaAddProvider(
		[]*v1beta.AddItem{{Id: "wait", DisplayName: "Wait"}, {Id: "fail", DisplayName: "Fail"}},
		func(ctx context.Context, id string) error {
			entered <- id
			if id == "wait" {
				<-ctx.Done()
				return ctx.Err()
			}
			return errors.New("provider failure")
		},
	)
	hostCtx, stopHost := context.WithCancel(auth)
	defer stopHost()
	done := make(chan error, 1)
	go func() { done <- host.Run(hostCtx) }()
	require.NoError(t, installed.WaitUntilReady(ctx))
	require.True(t, registry.HasProvider(extension.Id))
	items := registry.Items()
	require.Len(t, items, 2)
	require.Equal(t, "wait", items[1].ID)
	require.Equal(t, "Wait", items[1].DisplayName)
	require.Equal(t, extension.Id, items[1].Provider)

	duplicate, err := rpc.Stream(auth)
	require.NoError(t, err)
	sendRegistration(t, duplicate)
	_, err = duplicate.Recv()
	require.Equal(t, codes.AlreadyExists, status.Code(err))

	invokeCtx, stopInvoke := context.WithCancel(ctx)
	defer stopInvoke()
	invoked := make(chan error, 1)
	go func() { invoked <- items[1].Run(invokeCtx) }()
	select {
	case id := <-entered:
		require.Equal(t, "wait", id)
	case <-ctx.Done():
		t.Fatal("add invocation did not reach provider")
	}
	stopInvoke()
	select {
	case err := <-invoked:
		require.ErrorIs(t, err, context.Canceled)
	case <-ctx.Done():
		t.Fatal("add invocation did not acknowledge cancellation")
	}
	require.ErrorContains(t, items[0].Run(ctx), "provider failure")
	require.Equal(t, "fail", <-entered)

	stopHost()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("add provider host did not stop")
	}
	require.Eventually(t, func() bool { return !registry.HasProvider(extension.Id) }, time.Second, time.Millisecond)

	empty, err := rpc.Stream(auth)
	require.NoError(t, err)
	sendRegistration(t, empty)
	ack, err := empty.Recv()
	require.NoError(t, err)
	require.NotNil(t, ack.GetRegisterResponse())
	require.True(t, registry.HasProvider(extension.Id))
	require.Empty(t, registry.Items())
	require.Equal(t, codes.Unavailable, status.Code(items[0].Run(ctx)))
	require.NoError(t, empty.CloseSend())
}

func sendRegistration(t *testing.T, stream v1beta.AddProviderService_StreamClient) {
	t.Helper()
	require.NoError(t, stream.Send(&v1beta.AddProviderMessage{
		RequestId: "registration",
		MessageType: &v1beta.AddProviderMessage_RegisterRequest{
			RegisterRequest: &v1beta.RegisterAddProviderRequest{},
		},
	}))
}
