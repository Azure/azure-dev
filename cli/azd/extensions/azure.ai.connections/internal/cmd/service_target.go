// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"azure.ai.connections/internal/definition"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"google.golang.org/grpc"
)

// aiConnectionHost is the azure.yaml service host kind owned by this extension.
// A `host: azure.ai.connection` service entry carries one Foundry project
// connection, keyed by the connection name.
const aiConnectionHost = "azure.ai.connection"

var _ azdext.ServiceTargetProvider = (*connectionServiceTarget)(nil)

// connectionServiceTarget owns the azure.ai.connection host so azd can walk a
// connection entry in the deploy graph. All lifecycle methods are no-ops; see
// Deploy for why.
type connectionServiceTarget struct {
	azdClient     *azdext.AzdClient
	projectClient serviceConfigReader
	upsert        func(context.Context, *connectionCreateFlags) error
}

// newConnectionServiceTarget creates the azure.ai.connection service-target provider.
func newConnectionServiceTarget(azdClient *azdext.AzdClient) azdext.ServiceTargetProvider {
	return &connectionServiceTarget{
		azdClient:     azdClient,
		projectClient: azdClient.Project(),
		upsert: func(ctx context.Context, flags *connectionCreateFlags) error {
			return (&ConnectionCreateAction{flags: flags}).Run(ctx)
		},
	}
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

// Deploy creates or replaces the connection declared by this service. The
// service key is the connection name; the owning extension reads and
// reconciles every other field in the service block.
func (p *connectionServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	environment, err := p.environmentValues(ctx, serviceConfig)
	if err != nil {
		return nil, err
	}
	flags, err := connectionServiceCreateFlags(serviceConfig, environment)
	if err != nil {
		return nil, err
	}
	if progress != nil {
		progress(fmt.Sprintf("Upserting connection %q", serviceConfig.GetName()))
	}
	if err := p.upsert(ctx, flags); err != nil {
		return nil, err
	}
	return &azdext.ServiceDeployResult{}, nil
}

func connectionServiceCreateFlags(
	serviceConfig *azdext.ServiceConfig,
	environment map[string]string,
) (*connectionCreateFlags, error) {
	input, err := parseConnectionServiceConfig(serviceConfig)
	if err != nil {
		return nil, err
	}
	expand := func(field, value string) (string, error) {
		expanded, err := foundry.ExpandEnv(value, func(name string) string { return environment[name] })
		if err != nil {
			return "", fmt.Errorf("resolving connection %q %s: %w", serviceConfig.GetName(), field, err)
		}
		return expanded, nil
	}
	target, err := expand("target", input.Target)
	if err != nil {
		return nil, err
	}
	audience, err := expand("audience", input.Audience)
	if err != nil {
		return nil, err
	}
	authorizationURL, err := expand("authorizationUrl", input.AuthorizationURL)
	if err != nil {
		return nil, err
	}
	tokenURL, err := expand("tokenUrl", input.TokenURL)
	if err != nil {
		return nil, err
	}
	refreshURL, err := expand("refreshUrl", input.RefreshURL)
	if err != nil {
		return nil, err
	}
	connectorName, err := expand("connectorName", input.ConnectorName)
	if err != nil {
		return nil, err
	}
	flags := &connectionCreateFlags{
		name:             serviceConfig.GetName(),
		kind:             input.Category,
		target:           target,
		authType:         normalizeAuthType(input.AuthType),
		force:            true,
		suppressOutput:   true,
		audience:         audience,
		authorizationURL: authorizationURL,
		tokenURL:         tokenURL,
		refreshURL:       refreshURL,
		connectorName:    connectorName,
	}
	for index, scope := range input.Scopes {
		expanded, err := expand(fmt.Sprintf("scopes[%d]", index), scope)
		if err != nil {
			return nil, err
		}
		flags.scopes = append(flags.scopes, expanded)
	}
	if flags.authType == "" {
		flags.authType = "none"
	}

	for key, value := range input.Metadata {
		expanded, err := expand("metadata."+key, value)
		if err != nil {
			return nil, err
		}
		flags.metadata = append(flags.metadata, key+"="+expanded)
	}
	slices.Sort(flags.metadata)
	for key, rawValue := range input.Credentials {
		if flags.authType == "custom-keys" && key == "keys" {
			keys, ok := rawValue.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("connection %q credentials.keys must be an object", flags.name)
			}
			for keyName, rawKeyValue := range keys {
				keyValue, ok := rawKeyValue.(string)
				if !ok {
					return nil, fmt.Errorf("connection %q credential %q must be a string", flags.name, keyName)
				}
				keyValue, err = expand("credentials.keys."+keyName, keyValue)
				if err != nil {
					return nil, err
				}
				flags.customKeys = append(flags.customKeys, keyName+"="+keyValue)
			}
			continue
		}
		value, ok := rawValue.(string)
		if !ok {
			return nil, fmt.Errorf("connection %q credential %q must be a string", flags.name, key)
		}
		value, err = expand("credentials."+key, value)
		if err != nil {
			return nil, err
		}
		switch flags.authType {
		case "api-key":
			if key == "key" {
				flags.key = value
			}
		case "oauth2":
			switch key {
			case "clientId":
				flags.clientID = value
			case "clientSecret":
				flags.clientSecret = value
			}
		default:
			flags.customKeys = append(flags.customKeys, key+"="+value)
		}
	}
	slices.Sort(flags.customKeys)
	return flags, nil
}

type serviceConfigReader interface {
	GetServiceConfigValue(
		ctx context.Context,
		request *azdext.GetServiceConfigValueRequest,
		options ...grpc.CallOption,
	) (*azdext.GetServiceConfigValueResponse, error)
}

func (p *connectionServiceTarget) environmentValues(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
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
	current, err := p.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, fmt.Errorf("resolving current azd environment: %w", err)
	}
	response, err := p.azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: current.GetEnvironment().GetName(),
	})
	if err != nil {
		return nil, fmt.Errorf("loading azd environment values: %w", err)
	}
	values := make(map[string]string, len(response.GetKeyValues()))
	for _, value := range response.GetKeyValues() {
		values[value.GetKey()] = value.GetValue()
	}
	return values, nil
}

func serviceEnvironmentDeclared(
	ctx context.Context,
	client serviceConfigReader,
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

func parseConnectionServiceConfig(serviceConfig *azdext.ServiceConfig) (*definition.Definition, error) {
	props := serviceConfig.GetAdditionalProperties()
	if props == nil || len(props.GetFields()) == 0 {
		props = serviceConfig.GetConfig()
	}
	input := &definition.Definition{}
	if props == nil {
		return input, nil
	}
	data, err := json.Marshal(props.AsMap())
	if err != nil {
		return nil, fmt.Errorf("encoding connection service %q config: %w", serviceConfig.GetName(), err)
	}
	if err := json.Unmarshal(data, input); err != nil {
		return nil, fmt.Errorf("parsing connection service %q config: %w", serviceConfig.GetName(), err)
	}
	return input, nil
}
