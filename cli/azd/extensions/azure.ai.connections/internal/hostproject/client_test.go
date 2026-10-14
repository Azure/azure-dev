// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package hostproject

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

type recordingConnection struct {
	grpc.ClientConnInterface
	invoke func(context.Context, string, any, any, ...grpc.CallOption) error
}

func (c recordingConnection) Invoke(
	ctx context.Context, method string, request, response any, options ...grpc.CallOption,
) error {
	return c.invoke(ctx, method, request, response, options...)
}

func TestApplyServiceConfigCanonicalWireAndNoUnsafeFallback(t *testing.T) {
	for _, code := range []codes.Code{codes.OK, codes.Aborted, codes.Unimplemented, codes.Unavailable} {
		t.Run(code.String(), func(t *testing.T) {
			calls := 0
			expected := "worker.api:\n  number: 9007199254740993\n"
			candidate := "host: azure.ai.connection\nenv:\n  KEY: ${SEARCH_KEY}\n"
			client, err := NewClient(recordingConnection{
				invoke: func(ctx context.Context, method string, request, response any, _ ...grpc.CallOption) error {
					calls++
					assert.Equal(t, "/azd.extensions.v1beta.ProjectService/ApplyServiceConfig", method)
					md, ok := metadata.FromOutgoingContext(ctx)
					require.True(t, ok)
					assert.Equal(t, []string{"test-token"}, md.Get("authorization"))
					req, ok := request.(*dynamicpb.Message)
					require.True(t, ok)
					wire, err := proto.Marshal(req)
					require.NoError(t, err)
					assert.Contains(t, string(wire), "9007199254740993")
					assert.Contains(t, string(wire), "${SEARCH_KEY}")
					fields := req.Descriptor().Fields()
					assert.Equal(t, "search.api", req.Get(fields.ByName("service_name")).String())
					assert.Equal(t, expected, req.Get(fields.ByName("expected_services_yaml")).String())
					assert.Equal(t, candidate, req.Get(fields.ByName("service_yaml")).String())
					return status.Error(code, "host result")
				},
			})
			require.NoError(t, err)
			ctx := metadata.AppendToOutgoingContext(t.Context(), "authorization", "test-token")
			err = client.ApplyServiceConfig(ctx, "search.api", expected, candidate)
			assert.Equal(t, code, status.Code(err))
			assert.Equal(t, 1, calls)
		})
	}
}
