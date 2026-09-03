// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"net"
	"os"
	"strings"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	_ "google.golang.org/grpc/encoding/gzip" // registers gzip compressor for gRPC streams
	"google.golang.org/grpc/metadata"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
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
	composeClient       v1beta.ComposeServiceClient
	workflowClient      WorkflowServiceClient
	extensionClient     ExtensionServiceClient
	serviceTargetClient ServiceTargetServiceClient
	containerClient     ContainerServiceClient
	accountClient       AccountServiceClient
	aiClient            AiModelServiceClient
	copilotClient       v1beta.CopilotServiceClient
	provisioningClient  ProvisioningServiceClient
	validationClient    ValidationServiceClient
	telemetryClient     v1beta.TelemetryServiceClient
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

// WithAccessToken sets the access token for the `azd` client into a new Go
// context. It also forwards the W3C trace context so telemetry the host
// records while serving the call joins the azd command's trace instead of
// starting an unrelated one.
func WithAccessToken(ctx context.Context, params ...string) context.Context {
	tokenValue := strings.Join(params, "")
	if tokenValue == "" {
		tokenValue = os.Getenv("AZD_ACCESS_TOKEN")
	}

	pairs := append([]string{"authorization", tokenValue}, traceContextPairs(ctx)...)

	return metadata.NewOutgoingContext(ctx, metadata.Pairs(pairs...))
}

// traceContextPairs returns W3C trace context metadata pairs. A span
// already on ctx wins; otherwise the TRACEPARENT/TRACESTATE variables
// azd sets on the extension process are used, which is the common
// case because extensions build their context from scratch. Values
// always round-trip through the propagator, so only well-formed trace
// context reaches gRPC metadata.
func traceContextPairs(ctx context.Context) []string {
	propagator := propagation.TraceContext{}

	// Extract rather than forward the environment values directly. A
	// malformed one is dropped here instead of reaching gRPC, which
	// rejects headers holding non-printable characters and would fail
	// every call on this client, not just telemetry.
	if !trace.SpanContextFromContext(ctx).IsValid() {
		ctx = propagator.Extract(ctx, propagation.MapCarrier{
			TraceparentKey: os.Getenv(TraceparentEnv),
			TracestateKey:  os.Getenv(TracestateEnv),
		})
	}

	carrier := propagation.MapCarrier{}
	propagator.Inject(ctx, carrier)

	parent := carrier.Get(TraceparentKey)
	if parent == "" {
		return nil
	}

	pairs := []string{TraceparentKey, parent}
	if state := carrier.Get(TracestateKey); state != "" {
		pairs = append(pairs, TracestateKey, state)
	}

	return pairs
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

// Compose returns the preview compose service client.
func (c *AzdClient) Compose() v1beta.ComposeServiceClient {
	if c.composeClient == nil {
		c.composeClient = v1beta.NewComposeServiceClient(c.connection)
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

// Copilot returns the preview Copilot agent service client.
func (c *AzdClient) Copilot() v1beta.CopilotServiceClient {
	if c.copilotClient == nil {
		c.copilotClient = v1beta.NewCopilotServiceClient(c.connection)
	}

	return c.copilotClient
}

// Provisioning returns the provisioning service client.
func (c *AzdClient) Provisioning() ProvisioningServiceClient {
	if c.provisioningClient == nil {
		c.provisioningClient = NewProvisioningServiceClient(c.connection)
	}

	return c.provisioningClient
}

// Validation returns the validation service client.
func (c *AzdClient) Validation() ValidationServiceClient {
	if c.validationClient == nil {
		c.validationClient = NewValidationServiceClient(c.connection)
	}

	return c.validationClient
}

// Telemetry returns the telemetry service client used to report extension
// usage events. See extensions/microsoft.azd.demo/internal/cmd/telemetry.go
// for a worked example.
//
// A fresh client is returned on each call rather than caching it on the
// AzdClient struct. Service target providers can deploy services concurrently,
// so an unsynchronized lazily-written cache field could race on first use. The
// generated client wrapper is cheap and shares the existing connection.
func (c *AzdClient) Telemetry() v1beta.TelemetryServiceClient {
	if c.telemetryClient == nil {
		c.telemetryClient = v1beta.NewTelemetryServiceClient(c.connection)
	}

	return c.telemetryClient
}
