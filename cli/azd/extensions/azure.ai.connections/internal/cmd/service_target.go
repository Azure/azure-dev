// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
	azdClient *azdext.AzdClient
}

// newConnectionServiceTarget creates the azure.ai.connection service-target provider.
func newConnectionServiceTarget(
	azdClient *azdext.AzdClient,
) azdext.ServiceTargetProvider {
	return &connectionServiceTarget{azdClient: azdClient}
}

// Initialize requires no setup.
func (p *connectionServiceTarget) Initialize(
	_ context.Context,
	_ *azdext.ServiceConfig,
) error {
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

<<<<<<< HEAD
// Deploy upserts the connection on its project via an idempotent ARM CreateOrUpdate.
// ${VAR} references resolve from the forwarded service environment.
// Foundry server-side ${{...}} expressions pass through untouched.
// Removing the service from azure.yaml stops azd managing the connection but does not
// delete it (use `azd ai connection delete`).
=======
// Deploy is a no-op. Connections declared as host: azure.ai.connection
// services are created at provision time by the microsoft.foundry provider
// (for both greenfield and brownfield projects), so creating them again here
// would be a redundant ARM write. This mirrors azure.ai.project's Deploy,
// which is a no-op for the same reason.
//
// The target still exists so azd can order a connection's deploy step via
// `uses:` (toolboxes/agents that depend on it). Removing a connection from
// azure.yaml stops azd managing it but does not delete it (use
// `azd ai connection delete`).
>>>>>>> origin/main
func (p *connectionServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
<<<<<<< HEAD
	cfg, err := parseConnectionServiceConfig(serviceConfig)
	if err != nil {
		return nil, err
	}
	name := serviceConfig.GetName()

	env, err := p.environmentValues(ctx, serviceConfig)
	if err != nil {
		return nil, err
	}
	expand := func(value string) string { return resolveConnectionEnv(value, env) }

	kebabAuth := normalizeAuthType(strings.TrimSpace(cfg.AuthType))
	// Identity-based and OAuth2 connections are provisioned by `connection create`
	// (init/provision), not at deploy time. buildConnectionBody can't build their
	// bodies, so upserting here would fail azd deploy. Skip them; the api-key,
	// custom-keys, and none types are still upserted to stay current.
	if !supportsDeployUpsert(kebabAuth) {
		if progress != nil {
			progress(fmt.Sprintf("Connection %q uses %s auth provisioned elsewhere; skipping deploy upsert", name, kebabAuth))
		}
		return &azdext.ServiceDeployResult{}, nil
	}
	key, customKeys := connectionCredentialArgs(kebabAuth, cfg.Credentials, expand)
	body, err := buildConnectionBody(
		cfg.Category, expand(cfg.Target), kebabAuth, key, customKeys,
		connectionMetadataPairs(cfg.Metadata, expand), "", "",
	)
	if err != nil {
		return nil, err
	}

=======
>>>>>>> origin/main
	if progress != nil {
		progress(fmt.Sprintf(
			"Connection %q is provisioned by infrastructure; nothing to deploy", serviceConfig.GetName()))
	}
	return &azdext.ServiceDeployResult{}, nil
}
<<<<<<< HEAD

// parseConnectionServiceConfig reads the service-level (inline) connection properties,
// falling back to the deprecated config: shape for azure.yaml files written before the
// per-resource service split.
func parseConnectionServiceConfig(svc *azdext.ServiceConfig) (*connectionServiceConfig, error) {
	props := svc.GetAdditionalProperties()
	if props == nil || len(props.GetFields()) == 0 {
		props = svc.GetConfig()
	}
	cfg := &connectionServiceConfig{}
	if props == nil {
		return cfg, nil
	}
	b, err := json.Marshal(props.AsMap())
	if err != nil {
		return nil, fmt.Errorf("encoding connection service %q config: %w", svc.GetName(), err)
	}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parsing connection service %q config: %w", svc.GetName(), err)
	}
	return cfg, nil
}

func (p *connectionServiceTarget) environmentValues(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
) (map[string]string, error) {
	if environment := serviceConfig.GetEnvironment(); len(environment) > 0 {
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

// resolveConnectionEnv expands ${VAR} from the service environment.
func resolveConnectionEnv(value string, env map[string]string) string {
	resolved, err := foundry.ExpandEnv(value, func(name string) string { return env[name] })
	if err != nil {
		return value
	}
	return resolved
}

// supportsDeployUpsert reports whether buildConnectionBody can build a body for
// authType at deploy time. Identity-based and OAuth2 types are provisioned by
// `connection create`, so they are skipped during deploy.
func supportsDeployUpsert(authType string) bool {
	switch authType {
	case "api-key", "custom-keys", "none", "":
		return true
	default:
		return false
	}
}

// connectionCredentialArgs maps the service entry's credentials map to the key /
// custom-keys arguments buildConnectionBody expects, expanding ${VAR} per value. Only
// the auth types buildConnectionBody supports inline (api-key, custom-keys, none) are
// mapped here; other auth types surface buildConnectionBody's own validation error.
func connectionCredentialArgs(
	kebabAuth string,
	credentials map[string]any,
	expand func(string) string,
) (key string, customKeys []string) {
	switch kebabAuth {
	case "api-key":
		key = expand(stringFromAny(credentials["key"]))
	case "custom-keys":
		for _, k := range sortedKeys(credentials) {
			customKeys = append(customKeys, fmt.Sprintf("%s=%s", k, expand(stringFromAny(credentials[k]))))
		}
	}
	return key, customKeys
}

// connectionMetadataPairs renders the metadata map as sorted key=value pairs with ${VAR}
// expanded, matching the []string shape buildConnectionBody consumes.
func connectionMetadataPairs(metadata map[string]string, expand func(string) string) []string {
	if len(metadata) == 0 {
		return nil
	}
	keys := make([]string, 0, len(metadata))
	for k := range metadata {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(metadata))
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, expand(metadata[k])))
	}
	return pairs
}

// sortedKeys returns the keys of m in sorted order so generated credential pairs are
// deterministic across deploys.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// stringFromAny renders a credential or metadata value as a string. Non-string scalars
// are formatted with their default representation; nil becomes empty.
func stringFromAny(v any) string {
	switch typed := v.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		return fmt.Sprintf("%v", typed)
	}
}
=======
>>>>>>> origin/main
