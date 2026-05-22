// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	goyaml "go.yaml.in/yaml/v3"
)

func newEndpointCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "endpoint",
		Short: "Manage agent endpoint and card configuration.",
	}

	cmd.AddCommand(newEndpointUpdateCommand(extCtx))

	return cmd
}

type endpointUpdateFlags struct {
	name string
}

func newEndpointUpdateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &endpointUpdateFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "update [name]",
		Short: "Update an agent's endpoint and card configuration without deploying a new version.",
		Long: `Update an agent's endpoint and card configuration without deploying a new version.

This command reads the agent_endpoint and agent_card sections from agent.yaml and
patches the existing agent with those values. No new agent version is created.

The agent must already exist (i.e., it must have been previously deployed).`,
		Example: `  # Update endpoint/card for the default agent service
  azd ai agent endpoint update

  # Update a specific agent service
  azd ai agent endpoint update my-agent`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				flags.name = args[0]
			}

			ctx := azdext.WithAccessToken(cmd.Context())

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			return runEndpointUpdate(ctx, azdClient, flags, extCtx)
		},
	}

	return cmd
}

func runEndpointUpdate(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *endpointUpdateFlags,
	extCtx *azdext.ExtensionContext,
) error {
	// Resolve the agent service from the project.
	svc, proj, err := resolveAgentService(ctx, azdClient, flags.name, extCtx.NoPrompt)
	if err != nil {
		return err
	}

	// Read and parse agent.yaml.
	agentYamlPath := filepath.Join(proj.Path, svc.RelativePath, "agent.yaml")
	data, err := os.ReadFile(agentYamlPath) //nolint:gosec // path from azd project config
	if err != nil {
		return fmt.Errorf("failed to read agent.yaml: %w", err)
	}

	var agentDef agent_yaml.ContainerAgent
	if err := goyaml.Unmarshal(data, &agentDef); err != nil {
		return fmt.Errorf("failed to parse agent.yaml: %w", err)
	}

	// Validate that endpoint or card is defined.
	if agentDef.AgentEndpoint == nil && agentDef.AgentCard == nil {
		return fmt.Errorf(
			"agent.yaml for service %q does not define agent_endpoint or agent_card — nothing to update",
			svc.Name,
		)
	}

	// Build the API request to map YAML fields to API model.
	request, err := agent_yaml.CreateAgentAPIRequestFromDefinition(agentDef)
	if err != nil {
		return fmt.Errorf("failed to create agent request from definition: %w", err)
	}

	// Resolve endpoint and create client.
	agentContext, err := newAgentContext(ctx, "", "", agentDef.Name, "")
	if err != nil {
		return err
	}

	agentClient, err := agentContext.NewClient()
	if err != nil {
		return err
	}

	// Patch endpoint/card fields.
	patchRequest := &agent_api.PatchAgentRequest{
		AgentEndpoint: request.AgentEndpoint,
		AgentCard:     request.AgentCard,
	}

	_, err = agentClient.PatchAgent(ctx, agentDef.Name, patchRequest, DefaultAgentAPIVersion)
	if err != nil {
		return fmt.Errorf("failed to update agent %q: %w", agentDef.Name, err)
	}

	fmt.Fprintf(os.Stdout, output.WithSuccessFormat("Agent %q endpoint/card configuration updated successfully.\n"), agentDef.Name)
	return nil
}
