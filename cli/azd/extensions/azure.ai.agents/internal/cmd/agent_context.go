// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DefaultAgentAPIVersion is the default API version for agent operations.
const DefaultAgentAPIVersion = "2025-11-15-preview"

// ConversationsAPIVersion is the API version used by the Foundry Conversations protocol.
const ConversationsAPIVersion = "v1"

// AgentContext holds the common properties of a hosted agent.
type AgentContext struct {
	ProjectEndpoint string
	Name            string
	Version         string
}

// newAgentContext resolves the project endpoint and returns a fully populated AgentContext.
func newAgentContext(ctx context.Context, accountName, projectName, name, version string) (*AgentContext, error) {
	endpoint, err := resolveAgentEndpoint(ctx, accountName, projectName)
	if err != nil {
		return nil, err
	}

	return &AgentContext{
		ProjectEndpoint: endpoint,
		Name:            name,
		Version:         version,
	}, nil
}

// NewClient creates an AgentClient from this context's ProjectEndpoint.
func (ac *AgentContext) NewClient() (*agent_api.AgentClient, error) {
	credential, err := newAgentCredential()
	if err != nil {
		return nil, err
	}

	return agent_api.NewAgentClient(ac.ProjectEndpoint, credential), nil
}

// buildAgentEndpoint constructs the foundry agent API endpoint from account and project names.
func buildAgentEndpoint(accountName, projectName string) string {
	return fmt.Sprintf("https://%s.services.ai.azure.com/api/projects/%s", accountName, projectName)
}

// resolveProjectEndpointOpts controls the 5-level endpoint resolution cascade.
type resolveProjectEndpointOpts struct {
	// FlagValue is the value of the -p / --project-endpoint flag (level 1).
	// Empty means the flag was not provided.
	FlagValue string
}

// resolvedEndpoint holds the result of resolveProjectEndpoint.
type resolvedEndpoint struct {
	Endpoint   string
	Source     EndpointSource
	AzdEnvName string
	SetAt      string // RFC3339 timestamp, only meaningful when Source == SourceGlobalConfig
}

// azdHostedSources holds the values that the resolver reads from azd-managed
// sources (the active azd environment and ~/.azd/config.json). It is returned
// as a single struct so that tests can stub the whole lookup via
// readAzdHostedSourcesFunc.
type azdHostedSources struct {
	// EnvValue is the AZURE_AI_PROJECT_ENDPOINT value from the active azd
	// env, or "" if not set / no active env / no azd client available.
	EnvValue string
	// EnvName is the active azd env name. Only meaningful when EnvValue != "".
	EnvName string
	// CfgState is the project context persisted in global config.
	CfgState projectContextState
	// CfgFound indicates whether a non-empty endpoint was found in global config.
	CfgFound bool
}

// readAzdHostedSourcesFunc is a package-level seam so tests can stub the
// daemon-backed lookup without spinning up a real azd gRPC server.
var readAzdHostedSourcesFunc = readAzdHostedSources

// readAzdHostedSources dials the azd daemon (if reachable) and reads both
// the active env's AZURE_AI_PROJECT_ENDPOINT and the global-config project
// context in a single client lifetime. Errors talking to the daemon are
// returned only for non-Unavailable cases on the config read — Unavailable
// is treated as "no daemon" and the caller falls through to subsequent levels.
func readAzdHostedSources(ctx context.Context) (azdHostedSources, error) {
	var out azdHostedSources

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		// No azd client at all => no hosted sources, not an error.
		return out, nil
	}
	defer azdClient.Close()

	if envResp, err := azdClient.Environment().GetCurrent(
		ctx, &azdext.EmptyRequest{},
	); err == nil {
		envVal, valErr := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: envResp.Environment.Name,
			Key:     "AZURE_AI_PROJECT_ENDPOINT",
		})
		if valErr == nil && envVal.Value != "" {
			out.EnvValue = envVal.Value
			out.EnvName = envResp.Environment.Name
		}
	}

	state, found, cfgErr := getProjectContext(ctx, azdClient)
	if cfgErr != nil {
		// A gRPC Unavailable code means the azd daemon is not reachable;
		// treat it the same as azdClient creation failing and fall through
		// to the host-environment level.  Any other error (e.g. parse
		// failure) is a hard error that callers should surface.
		if !containsGRPCCode(cfgErr, codes.Unavailable) {
			return out, cfgErr
		}
	} else {
		out.CfgState = state
		out.CfgFound = found
	}

	return out, nil
}

// containsGRPCCode walks the error chain looking for a gRPC status with the
// specified code. Because fmt.Errorf("%w", ...) wraps errors without forwarding
// the GRPCStatus() method, we must unwrap manually.
// Note: only follows errors.Unwrap chains; errors.Join multi-wraps are not traversed.
func containsGRPCCode(err error, code codes.Code) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if st, ok := status.FromError(err); ok && st.Code() == code {
			return true
		}
	}
	return false
}

// resolveProjectEndpoint resolves a Foundry project endpoint using the 5-level
// cascade defined in the design spec:
//
//  1. -p / --project-endpoint flag
//  2. Active azd env value (AZURE_AI_PROJECT_ENDPOINT)
//  3. Global config: extensions.ai-agents.context.endpoint in ~/.azd/config.json
//  4. Host environment variable FOUNDRY_PROJECT_ENDPOINT
//  5. Structured error with actionable suggestion
//
// Invalid values at any level produce a hard validation error (no silent fallback).
func resolveProjectEndpoint(
	ctx context.Context,
	opts resolveProjectEndpointOpts,
) (*resolvedEndpoint, error) {
	// Level 1: explicit flag.
	if opts.FlagValue != "" {
		normalized, _, err := validateProjectEndpoint(opts.FlagValue)
		if err != nil {
			return nil, err
		}
		return &resolvedEndpoint{
			Endpoint: normalized,
			Source:   SourceFlag,
		}, nil
	}

	// Levels 2 + 3: azd-hosted sources (active env, then global config).
	sources, err := readAzdHostedSourcesFunc(ctx)
	if err != nil {
		return nil, err
	}

	// Level 2: active azd environment's AZURE_AI_PROJECT_ENDPOINT.
	if sources.EnvValue != "" {
		normalized, _, err := validateProjectEndpoint(sources.EnvValue)
		if err != nil {
			return nil, err
		}
		return &resolvedEndpoint{
			Endpoint:   normalized,
			Source:     SourceAzdEnv,
			AzdEnvName: sources.EnvName,
		}, nil
	}

	// Level 3: global config (~/.azd/config.json).
	if sources.CfgFound && sources.CfgState.Endpoint != "" {
		normalized, _, err := validateProjectEndpoint(sources.CfgState.Endpoint)
		if err != nil {
			return nil, err
		}
		return &resolvedEndpoint{
			Endpoint: normalized,
			Source:   SourceGlobalConfig,
			SetAt:    sources.CfgState.SetAt,
		}, nil
	}

	// Level 4: host environment variable FOUNDRY_PROJECT_ENDPOINT.
	if envVal := os.Getenv("FOUNDRY_PROJECT_ENDPOINT"); envVal != "" {
		normalized, _, err := validateProjectEndpoint(envVal)
		if err != nil {
			return nil, err
		}
		return &resolvedEndpoint{
			Endpoint: normalized,
			Source:   SourceFoundryEnv,
		}, nil
	}

	// Level 5: structured error.
	return nil, noProjectEndpointError()
}

// resolveAgentEndpoint resolves the agent API endpoint from explicit flags or
// the 5-level cascade. If accountName and projectName are provided, those are
// used to construct the endpoint directly (existing behavior). Otherwise the
// cascade is invoked with no flag value.
func resolveAgentEndpoint(ctx context.Context, accountName string, projectName string) (string, error) {
	if accountName != "" && projectName != "" {
		return buildAgentEndpoint(accountName, projectName), nil
	}

	if accountName != "" || projectName != "" {
		return "", fmt.Errorf("both --account-name and --project-name must be provided together")
	}

	result, err := resolveProjectEndpoint(ctx, resolveProjectEndpointOpts{})
	if err != nil {
		return "", err
	}

	return result.Endpoint, nil
}

// newAgentCredential creates a new Azure credential for agent API calls.
func newAgentCredential() (azcore.TokenCredential, error) {
	credential, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}
	return credential, nil
}
