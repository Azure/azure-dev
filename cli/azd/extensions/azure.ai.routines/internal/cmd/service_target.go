// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"azure.ai.routines/internal/pkg/routines"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
)

// aiRoutineHost is the azure.yaml service host kind owned by this extension. A
// `host: azure.ai.routine` service entry carries one Foundry routine, keyed by
// the routine name, and is upserted at deploy time by routineServiceTarget. A
// routine references an agent by name, so its service must declare uses: on the
// azure.ai.agent service it invokes, which orders the agent ahead of it.
const aiRoutineHost = "azure.ai.routine"

var _ azdext.ServiceTargetProvider = (*routineServiceTarget)(nil)

// routineServiceTarget upserts a Foundry routine declared as an azure.ai.routine
// service. The entry's service-level keys are bound directly to the routine API
// model (triggers, action, ...); the routine name is the service key. Package
// and Publish are no-ops because a routine has no build artifact.
type routineServiceTarget struct {
	azdClient *azdext.AzdClient
}

// newRoutineServiceTarget creates the azure.ai.routine service-target provider.
func newRoutineServiceTarget(
	azdClient *azdext.AzdClient,
) azdext.ServiceTargetProvider {
	return &routineServiceTarget{azdClient: azdClient}
}

// Initialize requires no setup.
func (p *routineServiceTarget) Initialize(
	_ context.Context,
	_ *azdext.ServiceConfig,
) error {
	return nil
}

// Endpoints returns no endpoints; a routine service exposes none.
func (p *routineServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	return nil, nil
}

// GetTargetResource delegates to azd's default resolver and falls back to a
// minimal target so the deploy pipeline can proceed; the routine upsert targets
// the Foundry project endpoint, not an ARM resource.
func (p *routineServiceTarget) GetTargetResource(
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

// Package is a no-op; a routine has nothing to build or stage.
func (p *routineServiceTarget) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	return &azdext.ServicePackageResult{}, nil
}

// Publish is a no-op; a routine has no artifact to publish.
func (p *routineServiceTarget) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	return &azdext.ServicePublishResult{}, nil
}

// Deploy upserts the routine with an idempotent PUT. The service-level keys bind
// directly to the routine API model, so the routine name is taken from the
// service key and never from the body. Removing the service from azure.yaml
// stops azd managing the routine but does not delete it (use
// `azd ai routine delete`).
func (p *routineServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	body, err := parseRoutineServiceConfig(serviceConfig)
	if err != nil {
		return nil, err
	}
	// The service key is the routine identity; ignore any name in the body.
	body.Name = serviceConfig.GetName()

	// Resolve ${VAR} against the service environment forwarded by azd.
	if body.Action != nil {
		environment, err := p.environmentValues(ctx, serviceConfig)
		if err != nil {
			return nil, err
		}
		body.Action.Input = expandRoutineValue(
			body.Action.Input,
			environment,
		)
	}

	if progress != nil {
		progress(fmt.Sprintf("Upserting routine %q", serviceConfig.GetName()))
	}

	client, err := newRoutineServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := client.PutRoutine(ctx, body.Name, body); err != nil {
		return nil, fmt.Errorf("upserting routine %q: %w", body.Name, err)
	}

	return &azdext.ServiceDeployResult{}, nil
}

// parseRoutineServiceConfig binds the service-level (inline) routine keys to the
// routine API model, falling back to the deprecated config: shape for azure.yaml
// files written before the per-resource service split.
func parseRoutineServiceConfig(svc *azdext.ServiceConfig) (*routines.Routine, error) {
	props := svc.GetAdditionalProperties()
	if props == nil || len(props.GetFields()) == 0 {
		props = svc.GetConfig()
	}
	body := &routines.Routine{}
	if props == nil {
		return body, nil
	}
	b, err := json.Marshal(props.AsMap())
	if err != nil {
		return nil, fmt.Errorf("encoding routine service %q config: %w", svc.GetName(), err)
	}
	if err := json.Unmarshal(b, body); err != nil {
		return nil, fmt.Errorf("parsing routine service %q config: %w", svc.GetName(), err)
	}
	return body, nil
}

// newRoutineServiceClient resolves the project endpoint (from the active azd
// environment, global config, or FOUNDRY_PROJECT_ENDPOINT) and an azd developer
// credential, then builds an authenticated routine client for deploy-time
// upserts. It mirrors newRoutineClient but takes no cobra command, since a
// service target has no flags.
func newRoutineServiceClient(ctx context.Context) (*routines.Client, error) {
	requestTimeout, err := routineHTTPTimeoutOverrideFromEnv()
	if err != nil {
		return nil, err
	}

	resolved, err := resolveProjectEndpoint(ctx, "")
	if err != nil {
		return nil, err
	}
	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}
	return routines.NewClient(
		resolved.Endpoint,
		cred,
		routineClientOptions(requestTimeout),
	), nil
}

var serviceEnvDeclared = func(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	serviceName string,
) (bool, error) {
	resp, err := azdClient.Project().GetServiceConfigValue(ctx, &azdext.GetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "env",
	})
	if err != nil {
		return false, fmt.Errorf("reading env for service %q: %w", serviceName, err)
	}
	return resp.GetFound(), nil
}

func (p *routineServiceTarget) environmentValues(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
) (map[string]string, error) {
	environment := serviceConfig.GetEnvironment()
	if len(environment) > 0 {
		return environment, nil
	}
	// An explicit empty env: {} declares an isolated scope.
	// Core forwards it as an empty map, indistinguishable from
	// an omitted env, so consult the raw config before falling
	// back to the full azd environment.
	declared, err := serviceEnvDeclared(ctx, p.azdClient, serviceConfig.GetName())
	if err != nil {
		return nil, err
	}
	if declared {
		return environment, nil
	}

	current, err := p.azdClient.Environment().GetCurrent(
		ctx,
		&azdext.EmptyRequest{},
	)
	if err != nil {
		return nil, fmt.Errorf("resolving current azd environment: %w", err)
	}
	resp, err := p.azdClient.Environment().GetValues(
		ctx,
		&azdext.GetEnvironmentRequest{
			Name: current.GetEnvironment().GetName(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("loading azd environment values: %w", err)
	}
	values := make(map[string]string, len(resp.GetKeyValues()))
	for _, kv := range resp.GetKeyValues() {
		values[kv.GetKey()] = kv.GetValue()
	}
	return values, nil
}

// expandRoutineValue expands ${VAR} in nested routine values.
func expandRoutineValue(value any, env map[string]string) any {
	switch typed := value.(type) {
	case string:
		resolved, err := foundry.ExpandEnv(typed, func(name string) string { return env[name] })
		if err != nil {
			return typed
		}
		return resolved
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[k] = expandRoutineValue(v, env)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, v := range typed {
			out[i] = expandRoutineValue(v, env)
		}
		return out
	default:
		return value
	}
}
