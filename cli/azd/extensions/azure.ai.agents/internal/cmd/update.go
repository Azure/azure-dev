// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"

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

	cmd.AddCommand(newEndpointShowCommand(extCtx))
	cmd.AddCommand(newEndpointUpdateCommand(extCtx))

	return cmd
}

type endpointUpdateFlags struct {
	name  string
	force bool
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

	cmd.Flags().BoolVar(&flags.force, "force", false, "Skip confirmation prompts for breaking changes")

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
	agentYamlPath, err := paths.JoinAllowRoot(proj.Path, svc.RelativePath, "agent.yaml")
	if err != nil {
		return fmt.Errorf("invalid agent.yaml path: %w", err)
	}
	data, err := os.ReadFile(agentYamlPath) //nolint:gosec // path validated by JoinAllowRoot
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

	// Map YAML endpoint/card fields to API models (skips full definition validation).
	apiEndpoint, apiCard, err := agent_yaml.MapEndpointAndCard(agentDef.AgentEndpoint, agentDef.AgentCard)
	if err != nil {
		return fmt.Errorf("failed to map endpoint/card fields: %w", err)
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

	// Check for breaking auth changes and warn unless --force is set.
	if !flags.force && apiEndpoint != nil && len(apiEndpoint.AuthorizationSchemes) > 0 {
		if err := warnIfAuthChange(ctx, agentClient, agentDef.Name, apiEndpoint, extCtx.NoPrompt); err != nil {
			return err
		}
	}

	// Patch endpoint/card fields.
	patchRequest := &agent_api.PatchAgentRequest{
		AgentEndpoint: apiEndpoint,
		AgentCard:     apiCard,
	}

	_, err = agentClient.PatchAgent(ctx, agentDef.Name, patchRequest, DefaultAgentAPIVersion)
	if err != nil {
		return fmt.Errorf("failed to update agent %q: %w", agentDef.Name, err)
	}

	fmt.Fprint(os.Stdout, output.WithSuccessFormat("Agent %q endpoint/card configuration updated successfully.\n", agentDef.Name))
	return nil
}

// warnIfAuthChange detects authorization scheme changes that may break existing callers
// and prompts for confirmation.
func warnIfAuthChange(
	ctx context.Context,
	client *agent_api.AgentClient,
	agentName string,
	newEndpoint *agent_api.AgentEndpoint,
	noPrompt bool,
) error {
	// Fetch current agent to compare auth config.
	current, err := client.GetAgent(ctx, agentName, DefaultAgentAPIVersion)
	if err != nil {
		// If agent doesn't exist yet, no warning needed.
		return nil
	}

	oldIsolation := getIsolationKind(current.AgentEndpoint)
	newIsolation := getIsolationKindFromSchemes(newEndpoint.AuthorizationSchemes)

	if oldIsolation == newIsolation || oldIsolation == "" || newIsolation == "" {
		return nil
	}

	// Auth is changing — warn the user.
	fmt.Fprintf(os.Stderr,
		"\n⚠️  WARNING: Changing isolation key source from %q to %q.\n"+
			"   This is a BREAKING CHANGE — all existing API callers must immediately\n"+
			"   update their requests to match the new authorization scheme.\n",
		oldIsolation, newIsolation,
	)

	if newIsolation == string(agent_api.IsolationKeySourceKindHeader) {
		fmt.Fprintf(os.Stderr,
			"   Callers must include the \"x-ms-user-isolation-key\" header, or they will receive 400 errors.\n",
		)
	}

	if noPrompt {
		return fmt.Errorf("auth scheme change requires confirmation; use --force to skip")
	}

	fmt.Fprintf(os.Stderr, "\n   Continue? [y/N] ")
	var answer string
	fmt.Scanln(&answer)
	if answer != "y" && answer != "Y" {
		return fmt.Errorf("operation cancelled by user")
	}

	return nil
}

func getIsolationKind(endpoint *agent_api.AgentEndpoint) string {
	if endpoint == nil || len(endpoint.AuthorizationSchemes) == 0 {
		return ""
	}
	for _, scheme := range endpoint.AuthorizationSchemes {
		if scheme.IsolationKeySource != nil {
			return string(scheme.IsolationKeySource.Kind)
		}
	}
	return ""
}

func getIsolationKindFromSchemes(schemes []agent_api.AgentEndpointAuthorizationScheme) string {
	if len(schemes) == 0 {
		return ""
	}
	for _, scheme := range schemes {
		if scheme.IsolationKeySource != nil {
			return string(scheme.IsolationKeySource.Kind)
		}
	}
	return ""
}
