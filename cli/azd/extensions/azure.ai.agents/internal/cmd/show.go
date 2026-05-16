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
	"text/tabwriter"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"
	projectpkg "azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type showFlags struct {
	name   string
	output string
}

// ShowAction handles the execution of the show command.
type ShowAction struct {
	*AgentContext
	flags     *showFlags
	azdClient *azdext.AzdClient
	envName   string
	// serviceKey is the uppercase/underscored form of the service name,
	// used to look up per-service env vars (e.g. AGENT_{KEY}_RESPONSES_ENDPOINT).
	serviceKey string
}

func newShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &showFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "show [name]",
		Short: "Show the status of a hosted agent.",
		Long: `Show the status of a hosted agent.

The agent name and version are resolved automatically from the azure.yaml service
configuration and the current azd environment. Optionally specify the service name
(from azure.yaml) as a positional argument when multiple agent services exist.`,
		Example: `  # Show status (auto-resolves from azure.yaml)
  azd ai agent show

  # Show status for a specific agent service
  azd ai agent show my-agent

  # Show status as JSON
  azd ai agent show --output json`,
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

			info, err := resolveAgentServiceFromProject(ctx, azdClient, flags.name, extCtx.NoPrompt)
			if err != nil {
				return err
			}

			if info.AgentName == "" {
				return fmt.Errorf(
					"agent name could not be resolved from azd environment for service '%s'\n\n"+
						"Run 'azd deploy' first to deploy the agent, or check your azd environment values",
					info.ServiceName,
				)
			}
			if info.Version == "" {
				return fmt.Errorf(
					"agent version could not be resolved from azd environment for service '%s'\n\n"+
						"Run 'azd deploy' first to deploy the agent, or check your azd environment values",
					info.ServiceName,
				)
			}

			agentContext, err := newAgentContext(ctx, "", "", info.AgentName, info.Version)
			if err != nil {
				return err
			}

			// Resolve the current environment name for env var lookups
			var envName string
			if envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
				envName = envResp.Environment.Name
			}

			action := &ShowAction{
				AgentContext: agentContext,
				flags:        flags,
				azdClient:    azdClient,
				envName:      envName,
				serviceKey:   toServiceKey(info.ServiceName),
			}

			return action.Run(ctx)
		},
	}

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "table"},
		Default:       "table",
	})

	return cmd
}

// showResult wraps the API response with additional computed links.
type showResult struct {
	*agent_api.AgentVersionObject
	PlaygroundURL string            `json:"playground_url,omitempty"`
	Endpoints     map[string]string `json:"agent_endpoints,omitempty"`
}

// Run executes the show command logic.
func (a *ShowAction) Run(ctx context.Context) error {
	agentClient, err := a.NewClient()
	if err != nil {
		return err
	}

	version, err := agentClient.GetAgentVersion(
		ctx, a.Name, a.Version, DefaultAgentAPIVersion,
	)
	if err != nil {
		return fmt.Errorf("failed to get agent version: %w", err)
	}

	result := &showResult{AgentVersionObject: version}

	// Resolve playground URL (best-effort)
	result.PlaygroundURL = a.resolvePlaygroundURL(ctx)

	// Resolve deployed endpoint URLs from env vars (best-effort)
	result.Endpoints = a.resolveEndpointURLs(ctx)

	return printShowResult(result, a.flags.output)
}

func printShowResult(result *showResult, output string) error {
	switch output {
	case "", "table":
		return printShowResultTable(result)
	case "json":
		return printShowResultJSON(result)
	default:
		return fmt.Errorf("unsupported output format %q", output)
	}
}

// resolvePlaygroundURL reads AZURE_AI_PROJECT_ID from the azd environment
// and constructs the Foundry portal playground URL. Returns empty string on failure.
func (a *ShowAction) resolvePlaygroundURL(ctx context.Context) string {
	if a.azdClient == nil || a.envName == "" {
		return ""
	}

	v, err := a.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: a.envName,
		Key:     "AZURE_AI_PROJECT_ID",
	})
	if err != nil {
		return ""
	}
	if v.Value == "" {
		return ""
	}

	playgroundURL, err := projectpkg.AgentPlaygroundURL(v.Value, a.Name, a.Version)
	if err != nil {
		return ""
	}

	return playgroundURL
}

// resolveEndpointURLs reads per-protocol endpoint env vars
// (e.g. AGENT_{KEY}_RESPONSES_ENDPOINT) from the azd environment.
// Falls back to the legacy single-endpoint var (AGENT_{KEY}_ENDPOINT)
// for environments deployed before per-protocol vars were introduced.
// Returns a map of protocol label to URL for endpoints that are set.
func (a *ShowAction) resolveEndpointURLs(ctx context.Context) map[string]string {
	if a.azdClient == nil || a.envName == "" || a.serviceKey == "" {
		return nil
	}

	// Use the canonical protocol list from the project package
	protocolSuffixes := projectpkg.DisplayableProtocolEnvSuffixes()

	endpoints := make(map[string]string)
	for _, ps := range protocolSuffixes {
		key := fmt.Sprintf("AGENT_%s_%s_ENDPOINT", a.serviceKey, ps.Suffix)
		v, err := a.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: a.envName,
			Key:     key,
		})
		if err != nil || v.Value == "" {
			continue
		}
		endpoints[ps.Label] = v.Value
	}

	// Fall back to single-endpoint var for older deployments
	if len(endpoints) == 0 {
		singleEndpointKey := fmt.Sprintf("AGENT_%s_ENDPOINT", a.serviceKey)
		v, err := a.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: a.envName,
			Key:     singleEndpointKey,
		})
		if err == nil && v.Value != "" {
			endpoints["Agent"] = v.Value
		}
	}

	if len(endpoints) == 0 {
		return nil
	}
	return endpoints
}

func printShowResultJSON(result *showResult) error {
	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal show result to JSON: %w", err)
	}
	fmt.Println(string(jsonBytes))
	return nil
}

func printShowResultTable(result *showResult) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FIELD\tVALUE")
	fmt.Fprintln(w, "-----\t-----")

	version := result.AgentVersionObject

	fmt.Fprintf(w, "ID\t%s\n", version.ID)
	fmt.Fprintf(w, "Name\t%s\n", version.Name)
	fmt.Fprintf(w, "Version\t%s\n", version.Version)
	if version.Status != "" {
		fmt.Fprintf(w, "Status\t%s\n", version.Status)
	}
	if version.Description != nil {
		fmt.Fprintf(w, "Description\t%s\n", *version.Description)
	}
	if version.CreatedAt != 0 {
		ts := time.Unix(version.CreatedAt, 0).UTC().Format(time.RFC3339)
		fmt.Fprintf(w, "Created At\t%s\n", ts)
	}
	if version.AgentGUID != "" {
		fmt.Fprintf(w, "Agent GUID\t%s\n", version.AgentGUID)
	}
	if version.InstanceIdentity != nil {
		fmt.Fprintf(w, "Instance Identity Principal ID\t%s\n", version.InstanceIdentity.PrincipalID)
		fmt.Fprintf(w, "Instance Identity Client ID\t%s\n", version.InstanceIdentity.ClientID)
	}
	if version.Blueprint != nil {
		fmt.Fprintf(w, "Blueprint Principal ID\t%s\n", version.Blueprint.PrincipalID)
		fmt.Fprintf(w, "Blueprint Client ID\t%s\n", version.Blueprint.ClientID)
	}
	if version.BlueprintReference != nil {
		fmt.Fprintf(w, "Blueprint Reference Type\t%s\n", version.BlueprintReference.Type)
		fmt.Fprintf(w, "Blueprint Reference ID\t%s\n", version.BlueprintReference.BlueprintID)
	}
	for k, v := range version.Metadata {
		fmt.Fprintf(w, "Metadata[%s]\t%s\n", k, v)
	}

	// Display playground and endpoint links
	if result.PlaygroundURL != "" {
		fmt.Fprintf(w, "Playground URL\t%s\n", result.PlaygroundURL)
	}
	for _, label := range slices.Sorted(maps.Keys(result.Endpoints)) {
		fmt.Fprintf(w, "Endpoint (%s)\t%s\n", label, result.Endpoints[label])
	}

	return w.Flush()
}
