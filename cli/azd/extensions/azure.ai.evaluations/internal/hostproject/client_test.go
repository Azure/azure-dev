// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package hostproject

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
)

type projectConnection struct {
	grpc.ClientConnInterface
	invoke func(context.Context, string, any, any, ...grpc.CallOption) error
}

func (c projectConnection) Invoke(
	ctx context.Context, method string, request, response any, options ...grpc.CallOption,
) error {
	return c.invoke(ctx, method, request, response, options...)
}

func TestCanonicalRequestPreservesReleasedSDKService(t *testing.T) {
	var calls int
	client, err := NewClient(projectConnection{invoke: func(
		_ context.Context, method string, request, response any, _ ...grpc.CallOption,
	) error {
		calls++
		assert.Equal(t, addServiceMethod, method)
		message, ok := request.(proto.Message)
		require.True(t, ok)
		reflected := message.ProtoReflect()
		assert.Equal(t, "this-operation", reflected.Get(reflected.Descriptor().Fields().ByName("operation_id")).String())
		wire, err := proto.Marshal(message)
		require.NoError(t, err)
		stable := &azdext.AddServiceRequest{}
		require.NoError(t, proto.Unmarshal(wire, stable))
		assert.Equal(t, "evals", stable.GetService().GetName())
		assert.Equal(t, []string{"agent"}, stable.GetService().GetUses())
		return nil
	}})
	require.NoError(t, err)
	completed, err := client.AddService(t.Context(),
		&azdext.ServiceConfig{Name: "evals", Uses: []string{"agent"}}, "this-operation")
	require.NoError(t, err)
	assert.True(t, completed)
	assert.Equal(t, 1, calls)
}

func TestCompletionDetailIsDecodedWithoutGlobalRegistration(t *testing.T) {
	client, err := NewClient(nil)
	require.NoError(t, err)
	ack, err := client.Message("AddServiceAcknowledgment")
	require.NoError(t, err)
	ack.Set(ack.Descriptor().Fields().ByName("operation_id"), protoreflect.ValueOfString("this-operation"))
	detail, err := anypb.New(ack)
	require.NoError(t, err)
	st := status.New(codes.Unknown, "save failed").Proto()
	st.Details = append(st.Details, detail)
	assert.True(t, client.completedFailure(status.FromProto(st).Err(), "this-operation"))
	assert.False(t, client.completedFailure(status.FromProto(st).Err(), "other-operation"))
	assert.False(t, client.completedFailure(status.FromProto(st).Err(), ""))
	// Resolve explicitly with the private registry: newer SDKs may register the same name globally.
	// Neither presence nor absence of a global type affects this decoder.
}

func TestCapabilityLookupIsNonMutating(t *testing.T) {
	client, err := NewClient(projectConnection{invoke: func(
		_ context.Context, method string, request, response any, _ ...grpc.CallOption,
	) error {
		assert.Equal(t, capabilitiesMethod, method)
		return status.Error(codes.Unimplemented, "not available")
	}})
	require.NoError(t, err)
	supported, err := client.SupportsAcknowledgment(t.Context())
	require.NoError(t, err)
	assert.False(t, supported)
}

func TestConnectRejectsInvalidAddress(t *testing.T) {
	t.Setenv("AZD_SERVER", "not-an-address")
	_, _, err := Connect()
	require.ErrorContains(t, err, "invalid azd server address")
}
