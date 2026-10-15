// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package hostproject consumes the canonical preview project contract without requiring
// an unreleased SDK or registering duplicate protobuf types from a newer SDK snapshot.
// TODO: Replace this bridge with the released SDK's ProjectBeta client after the core/SDK release.
package hostproject

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Client uses a private descriptor registry compiled from the canonical beta schema.
type Client struct {
	connection grpc.ClientConnInterface
	files      *protoregistry.Files
}

// NewClient constructs the preview client on an existing authenticated transport.
func NewClient(connection grpc.ClientConnInterface) (*Client, error) {
	wire, err := base64.StdEncoding.DecodeString(descriptorSetBase64)
	if err != nil {
		return nil, fmt.Errorf("decode project contract: %w", err)
	}
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(wire, set); err != nil {
		return nil, fmt.Errorf("unmarshal project contract: %w", err)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, fmt.Errorf("load project contract: %w", err)
	}
	return &Client{connection: connection, files: files}, nil
}

// Connect opens the host transport using the SDK's loopback/TLS policy.
// Authentication remains on the caller's context, as for azdext.NewAzdClient.
func Connect() (*Client, func() error, error) {
	address := os.Getenv("AZD_SERVER")
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid azd server address: %w", err)
	}
	transport := credentials.NewTLS(nil)
	if host == "localhost" || net.ParseIP(host).IsLoopback() {
		transport = insecure.NewCredentials()
	}
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(transport), grpc.WithDisableRetry())
	if err != nil {
		return nil, nil, err
	}
	client, err := NewClient(connection)
	if err != nil {
		if closeErr := connection.Close(); closeErr != nil {
			return nil, nil, errors.Join(err, fmt.Errorf("closing project transport: %w", closeErr))
		}
		return nil, nil, err
	}
	return client, connection.Close, nil
}

// Message creates a canonical message without adding it to the global SDK registry.
func (c *Client) Message(name string) (*dynamicpb.Message, error) {
	descriptor, err := c.files.FindDescriptorByName(protoreflect.FullName("azd.extensions.v1beta." + name))
	if err != nil {
		return nil, err
	}
	message, ok := descriptor.(protoreflect.MessageDescriptor)
	if !ok {
		return nil, fmt.Errorf("%s is not a project contract message", name)
	}
	return dynamicpb.NewMessage(message), nil
}

// SupportsAcknowledgment performs only the read-only capability RPC.
// Only an explicit unsupported response or Unimplemented permits the stable fallback.
func (c *Client) SupportsAcknowledgment(ctx context.Context) (bool, error) {
	request, err := c.Message("EmptyRequest")
	if err != nil {
		return false, err
	}
	response, err := c.Message("GetAddServiceCapabilitiesResponse")
	if err != nil {
		return false, err
	}
	if err := c.connection.Invoke(ctx, capabilitiesMethod, request, response); err != nil {
		if status.Code(err) == codes.Unimplemented {
			return false, nil
		}
		return false, fmt.Errorf("query AddService capability: %w", err)
	}
	return response.Get(response.Descriptor().Fields().ByName("acknowledgment_supported")).Bool(), nil
}

// AddService returns whether a failed operation was confirmed complete by the host.
// No mutation is retried or replayed, including an Unimplemented mutation response.
func (c *Client) AddService(ctx context.Context, service *azdext.ServiceConfig, operationID string) (bool, error) {
	request, err := c.Message("AddServiceRequest")
	if err != nil {
		return false, err
	}
	wire, err := proto.Marshal(&azdext.AddServiceRequest{Service: service})
	if err != nil {
		return false, err
	}
	if err := proto.Unmarshal(wire, request); err != nil {
		return false, err
	}
	request.Set(request.Descriptor().Fields().ByName("operation_id"), protoreflect.ValueOfString(operationID))
	response, err := c.Message("EmptyResponse")
	if err != nil {
		return false, err
	}
	err = c.connection.Invoke(ctx, addServiceMethod, request, response, grpc.MaxRetryRPCBufferSize(0))
	if err == nil {
		return true, nil
	}
	return c.completedFailure(err, operationID), err
}

func (c *Client) completedFailure(err error, operationID string) bool {
	if operationID == "" {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	// These statuses cannot establish safe completion, even if a proxy attaches a detail.
	switch st.Code() {
	case codes.Canceled, codes.DeadlineExceeded, codes.Unauthenticated, codes.PermissionDenied, codes.Unavailable:
		return false
	}
	count := 0
	for _, detail := range st.Proto().Details {
		if detail.GetTypeUrl() != "type.googleapis.com/azd.extensions.v1beta.AddServiceAcknowledgment" {
			continue
		}
		count++
		ack, decodeErr := c.Message("AddServiceAcknowledgment")
		if decodeErr != nil || proto.Unmarshal(detail.Value, ack) != nil ||
			ack.Get(ack.Descriptor().Fields().ByName("operation_id")).String() != operationID {
			return false
		}
	}
	return count == 1
}
