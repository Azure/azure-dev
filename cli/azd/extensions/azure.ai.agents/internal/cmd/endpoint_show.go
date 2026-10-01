// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
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
		Short: "Show callable endpoints or hosted endpoint/card configuration.",
		Long: `Show endpoint information for a hosted, prompt, or voice agent.

Hosted agents display live protocols, version selector (traffic split),
authorization schemes, and agent card (A2A discovery). Prompt agents display
their deployed Responses endpoint. Voice agents display their deployed WebSocket
endpoint.`,
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

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "table"},
		Default:       "table",
	})

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

	validation, err := project.ValidateAgentEndpointOperation(
		svc,
		proj.Path,
		project.AgentEndpointOperationShow,
	)
	if err != nil {
		return err
	}

	switch validation.Kind {
	case agent_yaml.AgentKindHosted:
		return runHostedEndpointShow(ctx, validation.Name, flags.output)
	case agent_yaml.AgentKindPrompt:
		return runPromptEndpointShow(ctx, azdClient, svc, proj.Path, validation, flags.output)
	case agent_yaml.AgentKindPromptVoice, agent_yaml.AgentKindVoice:
		return runVoiceEndpointShow(ctx, azdClient, svc, validation, flags.output)
	default:
		return exterrors.Internal(
			exterrors.CodeUnsupportedAgentKind,
			fmt.Sprintf("agent endpoint show has no handler for kind %q", validation.Kind),
		)
	}
}

func runHostedEndpointShow(ctx context.Context, agentName, outputFormat string) error {
	agentContext, err := newAgentContext(ctx, "", "", agentName, "")
	if err != nil {
		return err
	}

	agentClient, err := agentContext.NewClient()
	if err != nil {
		return err
	}

	agent, err := agentClient.GetAgent(ctx, agentName, DefaultAgentAPIVersion, false)
	if err != nil {
		return fmt.Errorf("failed to get agent %q: %w", agentName, err)
	}

	result := endpointShowResult{
		Name:          agent.Name,
		Kind:          agent_yaml.AgentKindHosted,
		AgentEndpoint: agent.AgentEndpoint,
		AgentCard:     agent.AgentCard,
	}
	return printEndpointShowResult(result, outputFormat)
}

func runPromptEndpointShow(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	svc *azdext.ServiceConfig,
	projectRoot string,
	validation project.AgentDefinitionValidation,
	outputFormat string,
) error {
	prompt, found, err := project.PromptAgentFromResolvedService(svc, projectRoot)
	if err != nil {
		return err
	}
	if !found {
		return exterrors.Internal(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("validated prompt agent service %q has no prompt definition", svc.GetName()),
		)
	}

	envValues, err := promptEnvValues(ctx, azdClient)
	if err != nil {
		return fmt.Errorf("reading the azd environment: %w", err)
	}
	settings, err := project.ResolvePromptAgentSettings(envValues)
	if err != nil {
		return err
	}

	agentName := deployedAgentName(envValues, svc.GetName(), validation.Name)
	endpoint := project.PromptAgentResponsesEndpoint(settings, agentName, prompt.HarnessType() != "")
	result := endpointShowResult{
		Name:      agentName,
		Kind:      validation.Kind,
		Endpoints: map[string]string{"responses": endpoint},
	}
	return printEndpointShowResult(result, outputFormat)
}

func runVoiceEndpointShow(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	svc *azdext.ServiceConfig,
	validation project.AgentDefinitionValidation,
	outputFormat string,
) error {
	envValues, err := promptEnvValues(ctx, azdClient)
	if err != nil {
		return fmt.Errorf("reading the azd environment: %w", err)
	}

	serviceKey := toServiceKey(svc.GetName())
	endpointKey := fmt.Sprintf("AGENT_%s_ENDPOINT", serviceKey)
	endpoint := strings.TrimSpace(envValues[endpointKey])
	if endpoint == "" {
		return exterrors.Dependency(
			exterrors.CodeMissingAgentEnvVars,
			fmt.Sprintf("%s environment variable is required", endpointKey),
			"run `azd deploy` to deploy the voice agent and set its callable endpoint",
		)
	}

	result := endpointShowResult{
		Name:      deployedAgentName(envValues, svc.GetName(), validation.Name),
		Kind:      validation.Kind,
		Endpoints: map[string]string{"voice": endpoint},
	}
	return printEndpointShowResult(result, outputFormat)
}

func deployedAgentName(envValues map[string]string, serviceName, fallback string) string {
	key := fmt.Sprintf("AGENT_%s_NAME", toServiceKey(serviceName))
	if name := strings.TrimSpace(envValues[key]); name != "" {
		return name
	}
	return fallback
}

type endpointShowResult struct {
	Name          string
	Kind          agent_yaml.AgentKind
	Endpoints     map[string]string
	AgentEndpoint *agent_api.AgentEndpoint
	AgentCard     *agent_api.AgentCard
}

func printEndpointShowResult(result endpointShowResult, outputFormat string) error {
	if outputFormat == "json" {
		return printEndpointResultJSON(result)
	}
	return printEndpointResultTable(result)
}

func printEndpointJSON(agent *agent_api.AgentObject) error {
	return printEndpointResultJSON(endpointShowResult{
		Name:          agent.Name,
		Kind:          agent_yaml.AgentKindHosted,
		AgentEndpoint: agent.AgentEndpoint,
		AgentCard:     agent.AgentCard,
	})
}

func printEndpointResultJSON(result endpointShowResult) error {
	out := map[string]any{
		"name": result.Name,
		"kind": result.Kind,
	}
	if len(result.Endpoints) > 0 {
		out["endpoints"] = result.Endpoints
	}
	if result.Kind == agent_yaml.AgentKindHosted {
		out["agent_endpoint"] = result.AgentEndpoint
		out["agent_card"] = result.AgentCard
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// resolveEndpointProtocols returns the list of enabled protocol names for display.
// It prefers ProtocolConfiguration (where key presence declares enablement) and falls
// back to the deprecated Protocols field for older API responses.
func resolveEndpointProtocols(endpoint *agent_api.AgentEndpoint) []string {
	if endpoint == nil {
		return nil
	}

	// Prefer protocol_configuration (newer API shape).
	// A non-nil ProtocolConfiguration is authoritative even if empty,
	// so we never fall through to the deprecated Protocols field.
	if pc := endpoint.ProtocolConfiguration; pc != nil {
		var protocols []string
		if pc.Activity != nil {
			protocols = append(protocols, "activity")
		}
		if pc.Responses != nil {
			protocols = append(protocols, "responses")
		}
		if pc.A2A != nil {
			protocols = append(protocols, "a2a")
		}
		if pc.MCP != nil {
			protocols = append(protocols, "mcp")
		}
		if pc.Invocations != nil {
			protocols = append(protocols, "invocations")
		}
		if pc.InvocationsWS != nil {
			protocols = append(protocols, "invocations_ws")
		}
		return protocols
	}

	// Fall back to deprecated Protocols field.
	if len(endpoint.Protocols) > 0 {
		protocols := make([]string, len(endpoint.Protocols))
		for i, p := range endpoint.Protocols {
			protocols[i] = string(p)
		}
		return protocols
	}

	return nil
}

func printEndpointTable(agent *agent_api.AgentObject) error {
	return printEndpointResultTable(endpointShowResult{
		Name:          agent.Name,
		Kind:          agent_yaml.AgentKindHosted,
		AgentEndpoint: agent.AgentEndpoint,
		AgentCard:     agent.AgentCard,
	})
}

func printEndpointResultTable(result endpointShowResult) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)

	fmt.Fprintf(w, "Agent:\t%s\n", result.Name)
	fmt.Fprintf(w, "Kind:\t%s\n", result.Kind)
	if len(result.Endpoints) > 0 {
		fmt.Fprintln(w, "\nCallable Endpoints:")
		for _, protocol := range slices.Sorted(maps.Keys(result.Endpoints)) {
			fmt.Fprintf(w, "  %s:\t%s\n", protocol, result.Endpoints[protocol])
		}
	}
	if result.Kind != agent_yaml.AgentKindHosted {
		return w.Flush()
	}
	fmt.Fprintln(w)

	// Protocols
	fmt.Fprintf(w, "Protocols:\t")
	if protocols := resolveEndpointProtocols(result.AgentEndpoint); len(protocols) > 0 {
		fmt.Fprintf(w, "%s\n", strings.Join(protocols, ", "))
	} else {
		fmt.Fprintf(w, "(not configured)\n")
	}

	// Version Selector
	fmt.Fprintf(w, "\nVersion Selector:\n")
	if result.AgentEndpoint != nil && result.AgentEndpoint.VersionSelector != nil &&
		len(result.AgentEndpoint.VersionSelector.VersionSelectionRules) > 0 {
		for _, rule := range result.AgentEndpoint.VersionSelector.VersionSelectionRules {
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
	if result.AgentEndpoint != nil && len(result.AgentEndpoint.AuthorizationSchemes) > 0 {
		for _, scheme := range result.AgentEndpoint.AuthorizationSchemes {
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
	if result.AgentCard != nil {
		if result.AgentCard.Version != nil {
			fmt.Fprintf(w, "  Version:\t%s\n", *result.AgentCard.Version)
		}
		fmt.Fprintf(w, "  Description:\t%s\n", result.AgentCard.Description)
		if len(result.AgentCard.Skills) > 0 {
			fmt.Fprintf(w, "  Skills:\n")
			for _, skill := range result.AgentCard.Skills {
				fmt.Fprintf(w, "    - %s:\t%s\n", skill.Name, skill.Description)
			}
		}
	} else {
		fmt.Fprintf(w, "  (not configured)\n")
	}

	return w.Flush()
}
