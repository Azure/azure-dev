// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// DefaultAgentAPIVersion is the default API version for agent operations.
const DefaultAgentAPIVersion = "2025-05-15-preview"

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

// resolveAgentEndpoint resolves the agent API endpoint from explicit flags or the azd environment.
// If accountName and projectName are provided, those are used to construct the endpoint.
// Otherwise, it falls back to the AZURE_AI_PROJECT_ENDPOINT environment variable from the current azd environment.
func resolveAgentEndpoint(ctx context.Context, accountName, projectName string) (string, error) {
	if accountName != "" && projectName != "" {
		return buildAgentEndpoint(accountName, projectName), nil
	}

	if accountName != "" || projectName != "" {
		return "", fmt.Errorf("both --account-name and --project-name must be provided together")
	}

	// Fall back to azd environment
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", fmt.Errorf(
			"failed to create azd client: %w\n\nProvide --account-name and --project-name flags, "+
				"or ensure azd environment is configured", err)
	}
	defer azdClient.Close()

	envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return "", fmt.Errorf(
			"failed to get current azd environment: %w\n\nProvide --account-name and --project-name flags, "+
				"or run 'azd init' to set up your environment", err)
	}

	envValue, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envResponse.Environment.Name,
		Key:     "AZURE_AI_PROJECT_ENDPOINT",
	})
	if err != nil || envValue.Value == "" {
		return "", fmt.Errorf(
			"AZURE_AI_PROJECT_ENDPOINT not found in azd environment '%s'\n\n"+
				"Provide --account-name and --project-name flags, "+
				"or run 'azd ai agent init' to configure the endpoint", envResponse.Environment.Name)
	}

	return envValue.Value, nil
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
