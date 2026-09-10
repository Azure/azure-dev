// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/insights_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

const (
	insightsExportSchemaVersion = "1.0"
	insightsPageSize            = 100
)

type insightsExportFlags struct {
	name            string
	envName         string
	projectEndpoint string
	outFile         string
	category        string
	severity        string
	status          string
	includeDetails  bool
}

type insightsClient interface {
	ListMonitors(context.Context, string, string, int) (*insights_api.MonitorPage, error)
	ListInsights(
		context.Context,
		string,
		insights_api.ListInsightOptions,
	) (*insights_api.InsightPage, error)
}

type insightsExportFilters struct {
	Category       string `json:"category,omitempty"`
	Severity       string `json:"severity,omitempty"`
	Status         string `json:"status,omitempty"`
	IncludeDetails bool   `json:"include_details"`
}

type insightsExportDocument struct {
	SchemaVersion   string                `json:"schema_version"`
	APIVersion      string                `json:"api_version"`
	ExportedAt      string                `json:"exported_at"`
	ProjectEndpoint string                `json:"project_endpoint"`
	AgentName       string                `json:"agent_name"`
	MonitorID       string                `json:"monitor_id"`
	Filters         insightsExportFilters `json:"filters"`
	InsightCount    int                   `json:"insight_count"`
	Insights        []json.RawMessage     `json:"insights"`
}

type insightsExportAction struct {
	client          insightsClient
	writer          io.Writer
	now             func() time.Time
	projectEndpoint string
	flags           *insightsExportFlags
}

func newInsightsCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "insights",
		Short: "Work with insights generated from hosted-agent traces. (Preview)",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newInsightsExportCommand(extCtx))
	return cmd
}

func newInsightsExportCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &insightsExportFlags{includeDetails: true}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "export [name]",
		Short: "Export insights for a hosted agent as JSON. (Preview)",
		Long: `Export all current insights for a hosted agent as JSON.

The command automatically follows every result page. By default, the export includes
highlighted traces, linked traces, and proposed fixes. Use --include-details=false
to export only the lightweight insight fields.

When run from an azd project, the agent is resolved from azure.yaml and the active
environment. Optionally specify a service name when the project has multiple agents.
Use --environment to select a different azd environment. Environment-bound exports
require FOUNDRY_PROJECT_ENDPOINT in that environment or an explicit --project-endpoint;
they never fall back to the global project context or shell endpoint.
Outside an azd project, pass the hosted-agent name as the positional argument.

JSON is written to stdout for PowerShell and other automation. Use --out-file to
write the same document directly to a file. Treat the export as sensitive because
insight descriptions and trace details can contain application or user data.`,
		Example: `  # Export the current project's agent insights to stdout
  azd ai agent insights export

  # Parse the export in PowerShell
  $export = azd ai agent insights export | ConvertFrom-Json

  # Export a named agent, including only active high-severity insights
  azd ai agent insights export my-agent --severity high --status active --out-file .\insights.json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				flags.name = args[0]
			}
			flags.envName = extCtx.Environment
			if err := validateInsightsExportFlags(flags); err != nil {
				return err
			}

			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("creating azd client: %w", err)
			}
			defer azdClient.Close()

			info, err := resolveInsightsAgentInfo(ctx, azdClient, flags, extCtx.NoPrompt)
			if err != nil {
				return err
			}
			if info.AgentName == "" {
				return exterrors.Dependency(
					exterrors.CodeAgentDefinitionNotFound,
					fmt.Sprintf("agent name could not be resolved for service %q", info.ServiceName),
					"run `azd deploy` first, or pass the hosted-agent name explicitly",
				)
			}

			resolved, err := resolveInsightsProjectEndpoint(ctx, flags, info)
			if err != nil {
				return err
			}

			credential, err := newAgentCredential()
			if err != nil {
				return exterrors.Auth(
					exterrors.CodeCredentialCreationFailed,
					fmt.Sprintf("failed to create an Azure credential: %s", err),
					"run `azd auth login` and retry",
				)
			}

			action := &insightsExportAction{
				client:          insights_api.NewClient(resolved.Endpoint, credential, nil),
				writer:          cmd.OutOrStdout(),
				now:             time.Now,
				projectEndpoint: resolved.Endpoint,
				flags:           flags,
			}
			action.flags.name = info.AgentName
			return action.Run(ctx)
		},
	}

	addProjectEndpointFlag(cmd, &flags.projectEndpoint)
	cmd.Flags().StringVarP(&flags.outFile, "out-file", "O", "", "Write the JSON export to this file")
	cmd.Flags().StringVar(&flags.category, "category", "", "Filter by exact insight category")
	cmd.Flags().StringVar(&flags.severity, "severity", "", "Filter by severity: high, medium, or low")
	cmd.Flags().StringVar(&flags.status, "status", "", "Filter by status: active, resolved, or ignored")
	cmd.Flags().BoolVar(
		&flags.includeDetails,
		"include-details",
		true,
		"Include traces and proposed fixes in the export",
	)
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json"},
		Default:       "json",
	})

	return cmd
}

func validateInsightsExportFlags(flags *insightsExportFlags) error {
	flags.category = strings.TrimSpace(flags.category)

	var err error
	flags.severity, err = normalizeInsightsFilter(
		"--severity",
		flags.severity,
		[]string{"high", "medium", "low"},
	)
	if err != nil {
		return err
	}
	flags.status, err = normalizeInsightsFilter(
		"--status",
		flags.status,
		[]string{"active", "resolved", "ignored"},
	)
	return err
}

func resolveInsightsAgentInfo(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *insightsExportFlags,
	noPrompt bool,
) (*AgentServiceInfo, error) {
	return resolveMonitorAgentInfo(
		ctx,
		azdClient,
		flags.name,
		noPrompt,
		withEnvironmentName(flags.envName),
	)
}

func resolveInsightsProjectEndpoint(
	ctx context.Context,
	flags *insightsExportFlags,
	info *AgentServiceInfo,
) (*resolvedEndpoint, error) {
	environmentBound := info.ServiceName != "" || flags.envName != ""
	resolved, err := resolveProjectEndpoint(ctx, resolveProjectEndpointOpts{
		FlagValue:                  flags.projectEndpoint,
		EnvName:                    flags.envName,
		RequireEnvironmentEndpoint: environmentBound,
	})
	if err != nil && !environmentBound {
		return nil, insightsEndpointError(err)
	}
	return resolved, err
}

func normalizeInsightsFilter(flagName, value string, allowed []string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	if slices.Contains(allowed, value) {
		return value, nil
	}
	return "", exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf("%s must be one of: %s", flagName, strings.Join(allowed, ", ")),
		fmt.Sprintf("choose a supported value for %s", flagName),
	)
}

func (a *insightsExportAction) Run(ctx context.Context) error {
	monitorPage, err := a.client.ListMonitors(ctx, a.flags.name, "", 2)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpListInsightMonitors)
	}
	matches := make([]insights_api.Monitor, 0, len(monitorPage.Data))
	for _, monitor := range monitorPage.Data {
		if strings.EqualFold(monitor.AgentName, a.flags.name) {
			matches = append(matches, monitor)
		}
	}
	if len(matches) == 0 {
		return exterrors.Dependency(
			exterrors.CodeInsightMonitorNotFound,
			fmt.Sprintf("no Agent Insights monitor exists for agent %q", a.flags.name),
			"create an Insights monitor for this agent in Microsoft Foundry, then retry",
		)
	}
	if len(matches) > 1 || monitorPage.HasMore {
		return exterrors.Dependency(
			exterrors.CodeInsightMonitorAmbiguous,
			fmt.Sprintf("multiple Agent Insights monitors were returned for agent %q", a.flags.name),
			"remove duplicate monitors for this agent in Microsoft Foundry, then retry",
		)
	}

	monitor := matches[0]
	if monitor.ID == "" {
		return exterrors.Internal(
			exterrors.CodeInvalidInsightMonitor,
			fmt.Sprintf("Agent Insights returned a monitor without an ID for agent %q", monitor.AgentName),
		)
	}

	monitorID := monitor.ID
	insights, err := fetchAllInsights(ctx, a.client, monitorID, a.flags)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpListInsights)
	}

	document := insightsExportDocument{
		SchemaVersion:   insightsExportSchemaVersion,
		APIVersion:      insights_api.APIVersion,
		ExportedAt:      a.now().UTC().Format(time.RFC3339),
		ProjectEndpoint: a.projectEndpoint,
		AgentName:       monitor.AgentName,
		MonitorID:       monitorID,
		Filters: insightsExportFilters{
			Category:       a.flags.category,
			Severity:       a.flags.severity,
			Status:         a.flags.status,
			IncludeDetails: a.flags.includeDetails,
		},
		InsightCount: len(insights),
		Insights:     insights,
	}

	if a.flags.outFile != "" {
		if err := writeInsightsExport(a.flags.outFile, document); err != nil {
			return fmt.Errorf("writing insights export: %w", err)
		}
		return nil
	}

	encoder := json.NewEncoder(a.writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return fmt.Errorf("writing insights export: %w", err)
	}
	return nil
}

func fetchAllInsights(
	ctx context.Context,
	client insightsClient,
	monitorID string,
	flags *insightsExportFlags,
) ([]json.RawMessage, error) {
	insights := make([]json.RawMessage, 0)
	after := ""
	seenCursors := map[string]struct{}{}

	for {
		page, err := client.ListInsights(ctx, monitorID, insights_api.ListInsightOptions{
			Category:       flags.category,
			Severity:       flags.severity,
			Status:         flags.status,
			IncludeDetails: flags.includeDetails,
			Order:          "desc",
			After:          after,
			Limit:          insightsPageSize,
		})
		if err != nil {
			return nil, err
		}
		insights = append(insights, page.Data...)
		if !page.HasMore {
			return insights, nil
		}

		next := strings.TrimSpace(page.LastID)
		if next == "" {
			return nil, fmt.Errorf("Agent Insights returned has_more without a continuation cursor")
		}
		if _, exists := seenCursors[next]; exists {
			return nil, fmt.Errorf("Agent Insights returned a repeated continuation cursor %q", next)
		}
		seenCursors[next] = struct{}{}
		after = next
	}
}

func writeInsightsExport(path string, document insightsExportDocument) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("opening output file: %w", err)
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("restricting output file permissions: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("truncating output file: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seeking output file: %w", err)
	}

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return fmt.Errorf("writing output file: %w", err)
	}
	return nil
}

func insightsEndpointError(err error) error {
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localErr.Code != exterrors.CodeMissingProjectEndpoint {
		return err
	}

	return exterrors.Dependency(
		exterrors.CodeMissingProjectEndpoint,
		"no Foundry project endpoint is configured",
		"pass --project-endpoint, set a default with `azd ai project set <project-endpoint>`, "+
			"or run from an azd project with FOUNDRY_PROJECT_ENDPOINT set",
	)
}
