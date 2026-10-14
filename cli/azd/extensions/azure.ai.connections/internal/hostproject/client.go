// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package hostproject uses preview descriptors with a released SDK.
// Replace it with ProjectBeta after the authoring API is released.
package hostproject

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Client keeps preview descriptors outside the SDK's global registry.
type Client struct {
	connection grpc.ClientConnInterface
	files      *protoregistry.Files
}

// NewClient constructs a client on an authenticated host transport.
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

// Connect follows the SDK transport policy.
// The caller supplies authentication on its context.
func Connect() (*Client, func() error, error) {
	address := os.Getenv("AZD_SERVER")
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid azd server address")
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
		return nil, nil, errors.Join(err, connection.Close())
	}
	return client, connection.Close, nil
}

func (c *Client) message(name string) (*dynamicpb.Message, error) {
	descriptor, err := c.files.FindDescriptorByName(protoreflect.FullName("azd.extensions.v1beta." + name))
	if err != nil {
		return nil, err
	}
	message, ok := descriptor.(protoreflect.MessageDescriptor)
	if !ok {
		return nil, fmt.Errorf("invalid project message descriptor")
	}
	return dynamicpb.NewMessage(message), nil
}

// ApplyServiceConfig sends one conditional raw mutation.
// It never retries or falls back to an unsafe setter.
func (c *Client) ApplyServiceConfig(ctx context.Context, name, expectedYAML, serviceYAML string) error {
	request, err := c.message("ApplyServiceConfigRequest")
	if err != nil {
		return err
	}
	for key, value := range map[protoreflect.Name]string{
		"service_name": name, "expected_services_yaml": expectedYAML, "service_yaml": serviceYAML,
	} {
		request.Set(request.Descriptor().Fields().ByName(key), protoreflect.ValueOfString(value))
	}
	response, err := c.message("EmptyResponse")
	if err != nil {
		return err
	}
	return c.connection.Invoke(ctx, applyServiceConfigMethod, request, response, grpc.MaxRetryRPCBufferSize(0))
}
