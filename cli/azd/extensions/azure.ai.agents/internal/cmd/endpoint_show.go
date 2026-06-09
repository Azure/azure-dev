// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	goyaml "go.yaml.in/yaml/v3"
)

type endpointShowFlags struct {
	name   string
	output string
}

func newEndpointShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &endpointShowFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "show [name]",
		Short: "Show the current endpoint and card configuration of an agent.",
		Long: `Show the current endpoint and card configuration of an agent.

Displays protocols, version selector (traffic split), authorization schemes,
and agent card (A2A discovery) as configured on the live agent.`,
		Example: `  # Show endpoint config (auto-resolves from azure.yaml)
  azd ai agent endpoint show

  # Show for a specific agent service
  azd ai agent endpoint show my-agent

  # Output as JSON
  azd ai agent endpoint show --output json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				flags.name = args[0]
			}
			flags.output = extCtx.OutputFormat

			ctx := azdext.WithAccessToken(cmd.Context())

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			return runEndpointShow(ctx, azdClient, flags, extCtx)
		},
	}

	return cmd
}

func runEndpointShow(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *endpointShowFlags,
	extCtx *azdext.ExtensionContext,
) error {
	svc, proj, err := resolveAgentService(ctx, azdClient, flags.name, extCtx.NoPrompt)
	if err != nil {
		return err
	}

	// Read agent.yaml to get agent name.
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

	// Resolve endpoint and create client.
	agentContext, err := newAgentContext(ctx, "", "", agentDef.Name, "")
	if err != nil {
		return err
	}

	agentClient, err := agentContext.NewClient()
	if err != nil {
		return err
	}

	agent, err := agentClient.GetAgent(ctx, agentDef.Name, DefaultAgentAPIVersion)
	if err != nil {
		return fmt.Errorf("failed to get agent %q: %w", agentDef.Name, err)
	}

	if flags.output == "json" {
		return printEndpointJSON(agent)
	}

	return printEndpointTable(agent)
}

func printEndpointJSON(agent *agent_api.AgentObject) error {
	out := map[string]any{
		"name":           agent.Name,
		"agent_endpoint": agent.AgentEndpoint,
		"agent_card":     agent.AgentCard,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func printEndpointTable(agent *agent_api.AgentObject) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)

	fmt.Fprintf(w, "Agent:\t%s\n", agent.Name)
	fmt.Fprintln(w)

	// Protocols
	fmt.Fprintf(w, "Protocols:\t")
	if agent.AgentEndpoint != nil && len(agent.AgentEndpoint.Protocols) > 0 {
		protocols := make([]string, len(agent.AgentEndpoint.Protocols))
		for i, p := range agent.AgentEndpoint.Protocols {
			protocols[i] = string(p)
		}
		fmt.Fprintf(w, "%s\n", strings.Join(protocols, ", "))
	} else {
		fmt.Fprintf(w, "(not configured)\n")
	}

	// Version Selector
	fmt.Fprintf(w, "\nVersion Selector:\n")
	if agent.AgentEndpoint != nil && agent.AgentEndpoint.VersionSelector != nil &&
		len(agent.AgentEndpoint.VersionSelector.VersionSelectionRules) > 0 {
		for _, rule := range agent.AgentEndpoint.VersionSelector.VersionSelectionRules {
			pct := ""
			if rule.TrafficPercentage != nil {
				pct = fmt.Sprintf("%d%%", *rule.TrafficPercentage)
			}
			fmt.Fprintf(w, "  %s\t%s\n", rule.AgentVersion, pct)
		}
	} else {
		fmt.Fprintf(w, "  (default: @latest 100%%)\n")
	}

	// Authorization
	fmt.Fprintf(w, "\nAuthorization:\n")
	if agent.AgentEndpoint != nil && len(agent.AgentEndpoint.AuthorizationSchemes) > 0 {
		for _, scheme := range agent.AgentEndpoint.AuthorizationSchemes {
			isolation := "(not specified)"
			if scheme.IsolationKeySource != nil {
				isolation = string(scheme.IsolationKeySource.Kind)
			}
			fmt.Fprintf(w, "  Type:\t%s\n", scheme.Type)
			fmt.Fprintf(w, "  Isolation:\t%s\n", isolation)
		}
	} else {
		fmt.Fprintf(w, "  (not configured)\n")
	}

	// Agent Card
	fmt.Fprintf(w, "\nAgent Card:\n")
	if agent.AgentCard != nil {
		if agent.AgentCard.Version != nil {
			fmt.Fprintf(w, "  Version:\t%s\n", *agent.AgentCard.Version)
		}
		fmt.Fprintf(w, "  Description:\t%s\n", agent.AgentCard.Description)
		if len(agent.AgentCard.Skills) > 0 {
			fmt.Fprintf(w, "  Skills:\n")
			for _, skill := range agent.AgentCard.Skills {
				fmt.Fprintf(w, "    - %s:\t%s\n", skill.Name, skill.Description)
			}
		}
	} else {
		fmt.Fprintf(w, "  (not configured)\n")
	}

	return w.Flush()
}
