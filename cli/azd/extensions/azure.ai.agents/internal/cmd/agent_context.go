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

// lookupEnvFunc is the function used to read host environment variables.
// It is a package-level variable so tests can override it without OS state.
var lookupEnvFunc = os.Getenv

// containsGRPCCode walks the error chain looking for a gRPC status with the
// specified code. Because fmt.Errorf("%w", ...) wraps errors without forwarding
// the GRPCStatus() method, we must unwrap manually.
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

	// Level 2: active azd environment's AZURE_AI_PROJECT_ENDPOINT.
	azdClient, azdErr := azdext.NewAzdClient()
	if azdErr == nil {
		defer azdClient.Close()

		if envResp, err := azdClient.Environment().GetCurrent(
			ctx, &azdext.EmptyRequest{},
		); err == nil {
			envVal, valErr := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
				EnvName: envResp.Environment.Name,
				Key:     "AZURE_AI_PROJECT_ENDPOINT",
			})
			if valErr == nil && envVal.Value != "" {
				normalized, _, err := validateProjectEndpoint(envVal.Value)
				if err != nil {
					return nil, err
				}
				return &resolvedEndpoint{
					Endpoint:   normalized,
					Source:     SourceAzdEnv,
					AzdEnvName: envResp.Environment.Name,
				}, nil
			}
		}

		// Level 3: global config (requires azd client).
		state, found, cfgErr := getProjectContext(ctx, azdClient)
		if cfgErr != nil {
			// A gRPC Unavailable code means the azd daemon is not reachable;
			// treat it the same as azdClient creation failing and fall through
			// to the host-environment level.  Any other error (e.g. parse
			// failure) is a hard error that callers should surface.
			if !containsGRPCCode(cfgErr, codes.Unavailable) {
				return nil, cfgErr
			}
		} else if found && state.Endpoint != "" {
			normalized, _, err := validateProjectEndpoint(state.Endpoint)
			if err != nil {
				return nil, err
			}
			return &resolvedEndpoint{
				Endpoint: normalized,
				Source:   SourceGlobalConfig,
				SetAt:    state.SetAt,
			}, nil
		}
	}

	// Level 4: host environment variable FOUNDRY_PROJECT_ENDPOINT.
	if envVal := lookupEnvFunc("FOUNDRY_PROJECT_ENDPOINT"); envVal != "" {
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
