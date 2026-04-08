// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"net"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type AzdClientOption func(*AzdClient) error

// AzdClient is the client for the `azd` gRPC server.
type AzdClient struct {
	connection          *grpc.ClientConn
	projectClient       ProjectServiceClient
	environmentClient   EnvironmentServiceClient
	userConfigClient    UserConfigServiceClient
	promptClient        PromptServiceClient
	deploymentClient    DeploymentServiceClient
	eventsClient        EventServiceClient
	composeClient       ComposeServiceClient
	workflowClient      WorkflowServiceClient
	extensionClient     ExtensionServiceClient
	serviceTargetClient ServiceTargetServiceClient
	containerClient     ContainerServiceClient
	accountClient       AccountServiceClient
	aiClient            AiModelServiceClient
	copilotClient       CopilotServiceClient
}

// WithAddress sets the address of the `azd` gRPC server.
func WithAddress(address string) AzdClientOption {
	return func(c *AzdClient) error {
		var opts []grpc.DialOption

		if isLocalhostAddress(address) {
			opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		} else {
			// For non-localhost connections, require TLS to prevent man-in-the-middle attacks.
			opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(nil)))
		}

		connection, err := grpc.NewClient(address, opts...)
		if err != nil {
			return err
		}

		c.connection = connection
		return nil
	}
}

// isLocalhostAddress checks if the given address refers to the local machine.
// It parses host:port format and checks against known localhost identifiers.
func isLocalhostAddress(address string) bool {
	host := address
	// Strip port if present
	if h, _, err := net.SplitHostPort(address); err == nil {
		host = h
	}

	host = strings.TrimSpace(strings.ToLower(host))

	switch host {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}

	// Check if it's a loopback IP
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// WithAccessToken sets the access token for the `azd` client into a new Go context.
func WithAccessToken(ctx context.Context, params ...string) context.Context {
	tokenValue := strings.Join(params, "")
	if tokenValue == "" {
		tokenValue = os.Getenv("AZD_ACCESS_TOKEN")
	}

	md := metadata.Pairs("authorization", tokenValue)
	return metadata.NewOutgoingContext(ctx, md)
}

// NewAzdClient creates a new `azd` client.
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

// Close closes the connection to the `azd` server.
func (c *AzdClient) Close() {
	c.connection.Close()
}

// Project returns the project service client.
func (c *AzdClient) Project() ProjectServiceClient {
	if c.projectClient == nil {
		c.projectClient = NewProjectServiceClient(c.connection)
	}

	return c.projectClient
}

// Environment returns the environment service client.
func (c *AzdClient) Environment() EnvironmentServiceClient {
	if c.environmentClient == nil {
		c.environmentClient = NewEnvironmentServiceClient(c.connection)
	}

	return c.environmentClient
}

// UserConfig returns the user config service client.
func (c *AzdClient) UserConfig() UserConfigServiceClient {
	if c.userConfigClient == nil {
		c.userConfigClient = NewUserConfigServiceClient(c.connection)
	}

	return c.userConfigClient
}

// Prompt returns the prompt service client.
func (c *AzdClient) Prompt() PromptServiceClient {
	if c.promptClient == nil {
		c.promptClient = NewPromptServiceClient(c.connection)
	}

	return c.promptClient
}

// Deployment returns the deployment service client.
func (c *AzdClient) Deployment() DeploymentServiceClient {
	if c.deploymentClient == nil {
		c.deploymentClient = NewDeploymentServiceClient(c.connection)
	}

	return c.deploymentClient
}

// Events returns the event service client.
func (c *AzdClient) Events() EventServiceClient {
	if c.eventsClient == nil {
		c.eventsClient = NewEventServiceClient(c.connection)
	}

	return c.eventsClient
}

// Compose returns the compose service client.
func (c *AzdClient) Compose() ComposeServiceClient {
	if c.composeClient == nil {
		c.composeClient = NewComposeServiceClient(c.connection)
	}

	return c.composeClient
}

// Workflow returns the workflow service client.
func (c *AzdClient) Workflow() WorkflowServiceClient {
	if c.workflowClient == nil {
		c.workflowClient = NewWorkflowServiceClient(c.connection)
	}

	return c.workflowClient
}

// ServiceTarget returns the service target service client.
func (c *AzdClient) ServiceTarget() ServiceTargetServiceClient {
	if c.serviceTargetClient == nil {
		c.serviceTargetClient = NewServiceTargetServiceClient(c.connection)
	}
	return c.serviceTargetClient
}

// FrameworkService returns the framework service client.
func (c *AzdClient) FrameworkService() FrameworkServiceClient {
	// Create framework service client directly as it's not yet added to the client struct
	return NewFrameworkServiceClient(c.connection)
}

// Container returns the container service client.
func (c *AzdClient) Container() ContainerServiceClient {
	if c.containerClient == nil {
		c.containerClient = NewContainerServiceClient(c.connection)
	}
	return c.containerClient
}

// Extension returns the extension service client.
func (c *AzdClient) Extension() ExtensionServiceClient {
	if c.extensionClient == nil {
		c.extensionClient = NewExtensionServiceClient(c.connection)
	}

	return c.extensionClient
}

// Account returns the account service client.
func (c *AzdClient) Account() AccountServiceClient {
	if c.accountClient == nil {
		c.accountClient = NewAccountServiceClient(c.connection)
	}

	return c.accountClient
}

// Ai returns the AI model service client.
func (c *AzdClient) Ai() AiModelServiceClient {
	if c.aiClient == nil {
		c.aiClient = NewAiModelServiceClient(c.connection)
	}

	return c.aiClient
}

// Copilot returns the Copilot agent service client.
func (c *AzdClient) Copilot() CopilotServiceClient {
	if c.copilotClient == nil {
		c.copilotClient = NewCopilotServiceClient(c.connection)
	}

	return c.copilotClient
}
