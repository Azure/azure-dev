// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type AzdClientOption func(*AzdClient) error

type AzdClient struct {
	connection        *grpc.ClientConn
	projectClient     ProjectServiceClient
	environmentClient EnvironmentServiceClient
	userConfigClient  UserConfigServiceClient
	promptClient      PromptServiceClient
	deploymentClient  DeploymentServiceClient
}

func WithAddress(address string) AzdClientOption {
	return func(c *AzdClient) error {
		connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return err
		}

		c.connection = connection
		return nil
	}
}

func WithAccessToken(ctx context.Context, params ...string) context.Context {
	tokenValue := strings.Join(params, "")
	if tokenValue == "" {
		tokenValue = os.Getenv("AZD_ACCESS_TOKEN")
	}

	md := metadata.Pairs("authorization", tokenValue)
	return metadata.NewOutgoingContext(ctx, md)
}

func NewAzdClient(opts ...AzdClientOption) (*AzdClient, error) {
	if opts == nil {
		opts = append(opts, WithAddress(os.Getenv("AZD_SERVER")))
	}

	client := &AzdClient{}

	for _, opt := range opts {
		if err := opt(client); err != nil {
			return nil, err
		}
	}

	return client, nil
}

func (c *AzdClient) Close() {
	c.connection.Close()
}

func (c *AzdClient) Project() ProjectServiceClient {
	if c.projectClient == nil {
		c.projectClient = NewProjectServiceClient(c.connection)
	}

	return c.projectClient
}

func (c *AzdClient) Environment() EnvironmentServiceClient {
	if c.environmentClient == nil {
		c.environmentClient = NewEnvironmentServiceClient(c.connection)
	}

	return c.environmentClient
}

func (c *AzdClient) UserConfig() UserConfigServiceClient {
	if c.userConfigClient == nil {
		c.userConfigClient = NewUserConfigServiceClient(c.connection)
	}

	return c.userConfigClient
}

func (c *AzdClient) Prompt() PromptServiceClient {
	if c.promptClient == nil {
		c.promptClient = NewPromptServiceClient(c.connection)
	}

	return c.promptClient
}

func (c *AzdClient) Deployment() DeploymentServiceClient {
	if c.deploymentClient == nil {
		c.deploymentClient = NewDeploymentServiceClient(c.connection)
	}

	return c.deploymentClient
}
