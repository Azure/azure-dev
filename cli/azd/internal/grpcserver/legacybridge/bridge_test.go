// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package legacybridge

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)

func TestFreezeDescriptionExcludesNewStableMethods(t *testing.T) {
	service := frozenService{
		description: &grpc.ServiceDesc{
			ServiceName: "azd.extensions.v1.TestService",
			Methods: []grpc.MethodDesc{
				{MethodName: "Frozen"},
				{MethodName: "AddedLater"},
			},
			Streams: []grpc.StreamDesc{
				{StreamName: "FrozenStream"},
				{StreamName: "AddedLaterStream"},
			},
			Metadata: "azd/extensions/v1/test.proto",
		},
		methods: []string{"Frozen", "FrozenStream"},
	}

	description, err := freezeDescription(service)

	require.NoError(t, err)
	require.Equal(t, "azdext.TestService", description.ServiceName)
	require.Equal(t, []grpc.MethodDesc{{MethodName: "Frozen"}}, description.Methods)
	require.Equal(t, []grpc.StreamDesc{{StreamName: "FrozenStream"}}, description.Streams)
	require.Nil(t, description.Metadata)
}

func TestFreezeDescriptionRejectsMissingFrozenMethod(t *testing.T) {
	_, err := freezeDescription(frozenService{
		description: &grpc.ServiceDesc{ServiceName: "azd.extensions.v1.TestService"},
		methods:     []string{"Removed"},
	})

	require.ErrorContains(t, err, "Removed no longer exists")
}

func TestUsageInterceptorsCountOnlyLegacyCalls(t *testing.T) {
	tracing.ResetUsageAttributesForTest()
	t.Cleanup(tracing.ResetUsageAttributesForTest)

	unary := UnaryUsageInterceptor()
	handler := func(ctx context.Context, req any) (any, error) {
		return nil, nil
	}

	_, err := unary(t.Context(), nil, &grpc.UnaryServerInfo{FullMethod: "/azdext.ProjectService/Get"},
		func(ctx context.Context, req any) (any, error) {
			return handler(ctx, req)
		})
	require.NoError(t, err)
	_, err = unary(t.Context(), nil, &grpc.UnaryServerInfo{FullMethod: "/azd.extensions.v1.ProjectService/Get"},
		func(ctx context.Context, req any) (any, error) {
			return handler(ctx, req)
		})
	require.NoError(t, err)

	stream := StreamUsageInterceptor()
	err = stream(nil, nil, &grpc.StreamServerInfo{FullMethod: "/azdext.EventService/EventStream"},
		func(srv any, stream grpc.ServerStream) error {
			return nil
		})
	require.NoError(t, err)
	err = stream(nil, nil, &grpc.StreamServerInfo{FullMethod: "/azd.extensions.v1.EventService/EventStream"},
		func(srv any, stream grpc.ServerStream) error {
			return nil
		})
	require.NoError(t, err)

	attributes := tracing.GetUsageAttributes()
	require.Len(t, attributes, 1)
	require.Equal(t, fields.ExtensionLegacyGrpcCallCount.Key, attributes[0].Key)
	require.Equal(t, int64(2), attributes[0].Value.AsInt64())
}

func TestTranslateStatusDetails(t *testing.T) {
	original := status.FromProto(&statuspb.Status{
		Code:    int32(codes.InvalidArgument),
		Message: "invalid",
		Details: []*anypb.Any{
			{
				TypeUrl: "type.googleapis.com/azd.extensions.v1.ActionableErrorDetail",
				Value:   []byte{1, 2, 3},
			},
			{
				TypeUrl: "type.googleapis.com/azd.extensions.v1beta.ServiceErrorDetail",
				Value:   []byte{4, 5, 6},
			},
			{
				TypeUrl: "type.googleapis.com/google.rpc.ErrorInfo",
				Value:   []byte{7, 8, 9},
			},
		},
	}).Err()

	translated := TranslateStatusDetails(original)

	require.False(t, errors.Is(translated, original))
	st, ok := status.FromError(translated)
	require.True(t, ok)
	require.Equal(t, "type.googleapis.com/azdext.ActionableErrorDetail", st.Proto().Details[0].TypeUrl)
	require.Equal(t, "type.googleapis.com/azdext.ServiceErrorDetail", st.Proto().Details[1].TypeUrl)
	require.Equal(t, "type.googleapis.com/google.rpc.ErrorInfo", st.Proto().Details[2].TypeUrl)
}
