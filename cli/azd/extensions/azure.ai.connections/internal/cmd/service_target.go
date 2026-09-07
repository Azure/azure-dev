// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
// cspell:ignore envkey

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"strings"

	"azure.ai.connections/internal/definition"
	"azure.ai.connections/internal/exterrors"
	"azure.ai.connections/internal/foundry/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"google.golang.org/grpc"
)

// aiConnectionHost is the azure.yaml service host kind owned by this extension.
// A `host: azure.ai.connection` service entry carries one Foundry project
// connection, keyed by the connection name.
const aiConnectionHost = "azure.ai.connection"

var _ azdext.ServiceTargetProvider = (*connectionServiceTarget)(nil)

// connectionServiceTarget owns the azure.ai.connection host so azd can walk and
// reconcile a Connection entry in the deploy graph. Package and Publish are
// no-ops; Deploy performs the ARM upsert.
type connectionServiceTarget struct {
	projectClient projectConfigReader
	envClient     serviceEnvironmentClient
	environment   string
	upsert        func(context.Context, string, string, rawConnectionProperties) (string, error)
	publishMarker func(context.Context, string, string, string) error
}

// newConnectionServiceTarget creates the azure.ai.connection service-target provider.
func newConnectionServiceTarget(
	azdClient *azdext.AzdClient,
	environmentName string,
) azdext.ServiceTargetProvider {
	target := &connectionServiceTarget{
		projectClient: azdClient.Project(),
		envClient:     azdClient.Environment(),
		environment:   environmentName,
	}
	target.upsert = func(
		ctx context.Context,
		environmentName string,
		name string,
		properties rawConnectionProperties,
	) (string, error) {
		connectionContext, err := resolveConnectionContextForEnvironment(ctx, environmentName)
		if err != nil {
			return "", err
		}
		if err := rawCreateConnection(ctx, connectionContext, name, properties); err != nil {
			return "", exterrors.ServiceFromAzure(err, exterrors.OpCreateConnection)
		}
		return connectionContext.endpoint, nil
	}
	target.publishMarker = target.setConnectionProjectMarker
	return target
}

// Initialize requires no setup.
func (p *connectionServiceTarget) Initialize(context.Context, *azdext.ServiceConfig) error {
	return nil
}

// Endpoints returns no endpoints; a connection service exposes none.
func (p *connectionServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	return nil, nil
}

// GetTargetResource delegates to azd's default resolver and falls back to a minimal
// target so the deploy pipeline can proceed; the connection upsert targets the Foundry
// project, not an ARM resource azd tracks.
func (p *connectionServiceTarget) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *azdext.ServiceConfig,
	defaultResolver func() (*azdext.TargetResource, error),
) (*azdext.TargetResource, error) {
	if defaultResolver != nil {
		if target, err := defaultResolver(); err == nil && target != nil {
			return target, nil
		}
	}
	return &azdext.TargetResource{SubscriptionId: subscriptionId}, nil
}

// Package is a no-op; a connection has nothing to build or stage.
func (p *connectionServiceTarget) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	return &azdext.ServicePackageResult{}, nil
}

// Publish is a no-op; a connection has no artifact to publish.
func (p *connectionServiceTarget) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	return &azdext.ServicePublishResult{}, nil
}

// Deploy creates or replaces the Connection declared by this service. The
// owning extension resolves the complete service block and records a
// project-scoped readiness marker after the ARM upsert succeeds.
func (p *connectionServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	input, err := p.parseConnectionServiceConfig(ctx, serviceConfig)
	if err != nil {
		return nil, err
	}
	environmentName, err := p.environmentName(ctx)
	if err != nil {
		return nil, err
	}
	environment, err := p.environmentValues(ctx, serviceConfig, environmentName)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = serviceConfig.GetName()
	}
	properties, err := connectionServiceProperties(name, input, environment)
	if err != nil {
		return nil, err
	}
	if progress != nil {
		progress(fmt.Sprintf("Upserting connection %q", name))
	}
	projectEndpoint, err := p.upsert(ctx, environmentName, name, properties)
	if err != nil {
		return nil, err
	}
	if err := p.publishMarker(ctx, environmentName, serviceConfig.GetName(), projectEndpoint); err != nil {
		return nil, err
	}
	return &azdext.ServiceDeployResult{}, nil
}

func connectionServiceProperties(
	name string,
	input *definition.Definition,
	environment map[string]string,
) (rawConnectionProperties, error) {
	expand := func(field, value string) (string, error) {
		expanded, err := foundry.ExpandEnv(value, func(key string) string { return environment[key] })
		if err != nil {
			return "", fmt.Errorf("resolving connection %q %s: %w", name, field, err)
		}
		return expanded, nil
	}
	target, err := expand("target", input.Target)
	if err != nil {
		return rawConnectionProperties{}, err
	}
	audience, err := expand("audience", input.Audience)
	if err != nil {
		return rawConnectionProperties{}, err
	}
	authorizationURL, err := expand("authorizationUrl", input.AuthorizationURL)
	if err != nil {
		return rawConnectionProperties{}, err
	}
	tokenURL, err := expand("tokenUrl", input.TokenURL)
	if err != nil {
		return rawConnectionProperties{}, err
	}
	refreshURL, err := expand("refreshUrl", input.RefreshURL)
	if err != nil {
		return rawConnectionProperties{}, err
	}
	connectorName, err := expand("connectorName", input.ConnectorName)
	if err != nil {
		return rawConnectionProperties{}, err
	}

	properties := rawConnectionProperties{
		AuthType:         normalizeAuthTypeToARM(normalizeAuthType(input.AuthType)),
		Category:         normalizeKind(input.Category),
		Target:           target,
		Audience:         audience,
		AuthorizationURL: authorizationURL,
		TokenURL:         tokenURL,
		RefreshURL:       refreshURL,
		ConnectorName:    connectorName,
	}
	if properties.AuthType == "" {
		properties.AuthType = "None"
	}
	for index, scope := range input.Scopes {
		expanded, err := expand(fmt.Sprintf("scopes[%d]", index), scope)
		if err != nil {
			return rawConnectionProperties{}, err
		}
		properties.Scopes = append(properties.Scopes, expanded)
	}
	if input.Metadata != nil {
		properties.Metadata = make(map[string]string, len(input.Metadata))
		for key, value := range input.Metadata {
			expanded, err := expand("metadata."+key, value)
			if err != nil {
				return rawConnectionProperties{}, err
			}
			properties.Metadata[key] = expanded
		}
	}
	credentials, err := expandConnectionCredentials(input.Credentials, environment)
	if err != nil {
		return rawConnectionProperties{}, fmt.Errorf("resolving connection %q credentials: %w", name, err)
	}
	if credentials != nil {
		raw := rawCredentials(credentials)
		properties.Credentials = &raw
	} else if properties.AuthType == "OAuth2" {
		raw := rawCredentials{}
		properties.Credentials = &raw
	}
	return properties, nil
}

func expandConnectionCredentials(value map[string]any, environment map[string]string) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	expanded, err := expandConnectionCredentialValue(value, environment)
	if err != nil {
		return nil, err
	}
	credentials, ok := expanded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("credentials must be an object")
	}
	return credentials, nil
}

func expandConnectionCredentialValue(value any, environment map[string]string) (any, error) {
	switch typed := value.(type) {
	case string:
		return foundry.ExpandEnv(typed, func(name string) string { return environment[name] })
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			expanded, err := expandConnectionCredentialValue(item, environment)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			result[key] = expanded
		}
		return result, nil
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			expanded, err := expandConnectionCredentialValue(item, environment)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", index, err)
			}
			result[index] = expanded
		}
		return result, nil
	default:
		return value, nil
	}
}

type projectConfigReader interface {
	Get(
		context.Context,
		*azdext.EmptyRequest,
		...grpc.CallOption,
	) (*azdext.GetProjectResponse, error)
	GetServiceConfigValue(
		context.Context,
		*azdext.GetServiceConfigValueRequest,
		...grpc.CallOption,
	) (*azdext.GetServiceConfigValueResponse, error)
}

type serviceEnvironmentClient interface {
	GetCurrent(
		context.Context,
		*azdext.EmptyRequest,
		...grpc.CallOption,
	) (*azdext.EnvironmentResponse, error)
	GetValues(
		context.Context,
		*azdext.GetEnvironmentRequest,
		...grpc.CallOption,
	) (*azdext.KeyValueListResponse, error)
	SetValue(
		context.Context,
		*azdext.SetEnvRequest,
		...grpc.CallOption,
	) (*azdext.EmptyResponse, error)
}

func (p *connectionServiceTarget) environmentName(ctx context.Context) (string, error) {
	if p.environment != "" {
		return p.environment, nil
	}
	current, err := p.envClient.GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return "", fmt.Errorf("resolving current azd environment: %w", err)
	}
	if current.GetEnvironment() == nil || current.GetEnvironment().GetName() == "" {
		return "", fmt.Errorf("resolving current azd environment: environment is empty")
	}
	return current.GetEnvironment().GetName(), nil
}

func (p *connectionServiceTarget) environmentValues(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	environmentName string,
) (map[string]string, error) {
	if len(serviceConfig.GetEnvironment()) > 0 {
		return maps.Clone(serviceConfig.GetEnvironment()), nil
	}
	declared, err := serviceEnvironmentDeclared(ctx, p.projectClient, serviceConfig.GetName())
	if err != nil {
		return nil, err
	}
	if declared {
		return map[string]string{}, nil
	}
	response, err := p.envClient.GetValues(ctx, &azdext.GetEnvironmentRequest{Name: environmentName})
	if err != nil {
		return nil, fmt.Errorf("loading azd environment values: %w", err)
	}
	values := make(map[string]string, len(response.GetKeyValues()))
	for _, value := range response.GetKeyValues() {
		values[value.GetKey()] = value.GetValue()
	}
	for _, item := range os.Environ() {
		key, value, found := strings.Cut(item, "=")
		if found {
			if _, exists := values[key]; !exists {
				values[key] = value
			}
		}
	}
	return values, nil
}

func serviceEnvironmentDeclared(
	ctx context.Context,
	client projectConfigReader,
	serviceName string,
) (bool, error) {
	response, err := client.GetServiceConfigValue(ctx, &azdext.GetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "env",
	})
	if err != nil {
		return false, fmt.Errorf("reading env for connection service %q: %w", serviceName, err)
	}
	return response.GetFound(), nil
}

func (p *connectionServiceTarget) parseConnectionServiceConfig(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
) (*definition.Definition, error) {
	props := serviceConfig.GetAdditionalProperties()
	if props == nil || len(props.GetFields()) == 0 {
		props = serviceConfig.GetConfig()
	}
	input := &definition.Definition{}
	if props == nil {
		return input, nil
	}
	projectResponse, err := p.projectClient.Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, fmt.Errorf("reading project path for connection service %q: %w", serviceConfig.GetName(), err)
	}
	if projectResponse.GetProject() == nil || strings.TrimSpace(projectResponse.GetProject().GetPath()) == "" {
		return nil, fmt.Errorf("reading project path for connection service %q: project path is empty", serviceConfig.GetName())
	}
	resolved, err := foundry.ResolveFileRefs(props.AsMap(), projectResponse.GetProject().GetPath())
	if err != nil {
		return nil, fmt.Errorf("resolving connection service %q configuration: %w", serviceConfig.GetName(), err)
	}
	data, err := json.Marshal(resolved)
	if err != nil {
		return nil, fmt.Errorf("encoding connection service %q config: %w", serviceConfig.GetName(), err)
	}
	if err := json.Unmarshal(data, input); err != nil {
		return nil, fmt.Errorf("parsing connection service %q config: %w", serviceConfig.GetName(), err)
	}
	return input, nil
}

func (p *connectionServiceTarget) setConnectionProjectMarker(
	ctx context.Context,
	environmentName string,
	connectionName string,
	projectEndpoint string,
) error {
	key := envkey.ConnectionProjectEndpoint(connectionName)
	if _, err := p.envClient.SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: environmentName,
		Key:     key,
		Value:   "",
	}); err != nil {
		return fmt.Errorf("clearing Connection project marker %s: %w", key, err)
	}
	if _, err := p.envClient.SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: environmentName,
		Key:     key,
		Value:   strings.TrimRight(strings.TrimSpace(projectEndpoint), "/"),
	}); err != nil {
		return fmt.Errorf("publishing Connection project marker %s: %w", key, err)
	}
	return nil
}
