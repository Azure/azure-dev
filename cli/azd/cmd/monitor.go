// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/containerapps"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type monitorFlags struct {
	monitorLive     bool
	monitorLogs     bool
	monitorOverview bool
	monitorTail     bool
	global          *internal.GlobalCommandOptions
	internal.EnvFlag
}

func (m *monitorFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(
		&m.monitorLive,
		"live",
		false,
		"Open a browser to Application Insights Live Metrics. Live Metrics is currently not supported for Python apps.",
	)
	local.BoolVar(&m.monitorLogs, "logs", false, "Open a browser to Application Insights Logs.")
	local.BoolVar(
		&m.monitorOverview, "overview", false, "Open a browser to Application Insights Overview Dashboard.",
	)
	local.BoolVar(
		&m.monitorTail,
		"tail",
		false,
		"Stream application logs from a deployed service directly to the terminal.",
	)
	m.EnvFlag.Bind(local, global)
	m.global = global
}

func newMonitorFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *monitorFlags {
	flags := &monitorFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newMonitorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "monitor",
		Short: "Monitor a deployed project.",
	}
}

type monitorAction struct {
	azdCtx               *azdcontext.AzdContext
	env                  *environment.Environment
	subResolver          account.SubscriptionTenantResolver
	resourceManager      infra.ResourceManager
	resourceService      *azapi.ResourceService
	azureClient          *azapi.AzureClient
	containerAppService  containerapps.ContainerAppService
	console              input.Console
	flags                *monitorFlags
	portalUrlBase        string
	alphaFeaturesManager *alpha.FeatureManager
}

func newMonitorAction(
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	subResolver account.SubscriptionTenantResolver,
	resourceManager infra.ResourceManager,
	resourceService *azapi.ResourceService,
	azureClient *azapi.AzureClient,
	containerAppService containerapps.ContainerAppService,
	console input.Console,
	flags *monitorFlags,
	cloud *cloud.Cloud,
	alphaFeatureManager *alpha.FeatureManager,
) actions.Action {
	return &monitorAction{
		azdCtx:               azdCtx,
		env:                  env,
		resourceManager:      resourceManager,
		resourceService:      resourceService,
		azureClient:          azureClient,
		containerAppService:  containerAppService,
		console:              console,
		flags:                flags,
		subResolver:          subResolver,
		portalUrlBase:        cloud.PortalUrlBase,
		alphaFeaturesManager: alphaFeatureManager,
	}
}

func (m *monitorAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if !m.flags.monitorLive && !m.flags.monitorLogs && !m.flags.monitorOverview &&
		!m.flags.monitorTail {
		m.flags.monitorOverview = true
	}

	if m.env.GetSubscriptionId() == "" {
		return nil, &internal.ErrorWithSuggestion{
			Err:        internal.ErrInfraNotProvisioned,
			Suggestion: "Run 'azd provision' to set up infrastructure before monitoring.",
		}
	}

	// Handle --tail: stream application logs directly to the terminal
	if m.flags.monitorTail {
		return m.runTail(ctx)
	}

	aspireDashboard := apphost.AspireDashboardUrl(ctx, m.env, m.alphaFeaturesManager)
	if aspireDashboard != nil {
		openWithDefaultBrowser(ctx, m.console, aspireDashboard.Link)
		return nil, nil
	}

	resourceGroups, err := m.resourceManager.GetResourceGroupsForEnvironment(
		ctx, m.env.GetSubscriptionId(), m.env.Name(),
	)
	if err != nil {
		return nil, fmt.Errorf("discovering resource groups from deployment: %w", err)
	}

	var insightsResources []*azapi.ResourceExtended
	var portalResources []*azapi.ResourceExtended

	for _, resourceGroup := range resourceGroups {
		resources, err := m.resourceService.ListResourceGroupResources(
			ctx, azure.SubscriptionFromRID(resourceGroup.Id), resourceGroup.Name, nil)
		if err != nil {
			return nil, fmt.Errorf("listing resources: %w", err)
		}

		for _, resource := range resources {
			switch resource.Type {
			case string(azapi.AzureResourceTypePortalDashboard):
				portalResources = append(portalResources, resource)
			case string(azapi.AzureResourceTypeAppInsightComponent):
				insightsResources = append(insightsResources, resource)
			}
		}
	}

	if len(insightsResources) == 0 && (m.flags.monitorLive || m.flags.monitorLogs) {
		return nil, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"no Application Insights resource found: %w",
				internal.ErrResourceNotConfigured,
			),
			Suggestion: "Ensure your infrastructure includes an Application Insights component.",
		}
	}

	if len(portalResources) == 0 && m.flags.monitorOverview {
		return nil, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"no Application Insights dashboard found: %w",
				internal.ErrResourceNotConfigured,
			),
			Suggestion: "Ensure your infrastructure includes an Application Insights dashboard.",
		}
	}

	tenantId, err := m.subResolver.LookupTenant(ctx, m.env.GetSubscriptionId())
	if err != nil {
		return nil, err
	}

	for _, insightsResource := range insightsResources {
		if m.flags.monitorLive {
			openWithDefaultBrowser(ctx, m.console,
				fmt.Sprintf(
					"%s/#@%s/resource%s/quickPulse",
					m.portalUrlBase, tenantId, insightsResource.Id,
				))
		}

		if m.flags.monitorLogs {
			openWithDefaultBrowser(ctx, m.console,
				fmt.Sprintf(
					"%s/#@%s/resource%s/logs",
					m.portalUrlBase, tenantId, insightsResource.Id,
				))
		}
	}

	for _, portalResource := range portalResources {
		if m.flags.monitorOverview {
			openWithDefaultBrowser(ctx, m.console,
				fmt.Sprintf(
					"%s/#@%s/dashboard/arm%s",
					m.portalUrlBase, tenantId, portalResource.Id,
				))
		}
	}

	return nil, nil
}

// streamableResource represents a deployed Azure resource that supports log streaming.
type streamableResource struct {
	Name          string
	ResourceGroup string
	Type          azapi.AzureResourceType
}

// supportsLogStreaming reports whether the Azure resource type supports direct log streaming.
func supportsLogStreaming(resourceType string) bool {
	switch azapi.AzureResourceType(resourceType) {
	case azapi.AzureResourceTypeWebSite,
		azapi.AzureResourceTypeContainerApp:
		return true
	}
	return false
}

// runTail discovers deployed resources and streams application logs to the terminal.
func (m *monitorAction) runTail(ctx context.Context) (*actions.ActionResult, error) {
	resourceGroups, err := m.resourceManager.GetResourceGroupsForEnvironment(
		ctx, m.env.GetSubscriptionId(), m.env.Name(),
	)
	if err != nil {
		return nil, fmt.Errorf("discovering resource groups from deployment: %w", err)
	}

	// Collect all resources that support log streaming
	var streamable []streamableResource
	for _, resourceGroup := range resourceGroups {
		resources, err := m.resourceService.ListResourceGroupResources(
			ctx,
			azure.SubscriptionFromRID(resourceGroup.Id),
			resourceGroup.Name,
			nil,
		)
		if err != nil {
			return nil, fmt.Errorf("listing resources: %w", err)
		}

		for _, resource := range resources {
			if supportsLogStreaming(resource.Type) {
				streamable = append(streamable, streamableResource{
					Name:          resource.Name,
					ResourceGroup: resourceGroup.Name,
					Type:          azapi.AzureResourceType(resource.Type),
				})
			}
		}
	}

	if len(streamable) == 0 {
		return nil, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"no services that support log streaming found: %w",
				internal.ErrResourceNotConfigured,
			),
			Suggestion: "Ensure your project includes App Service, Azure Functions, or " +
				"Container App resources.",
		}
	}

	// If there are multiple streamable resources, prompt the user to select one
	selected := streamable[0]
	if len(streamable) > 1 {
		choices := make([]string, len(streamable))
		for i, r := range streamable {
			displayType := azapi.GetResourceTypeDisplayName(r.Type)
			if displayType == "" {
				displayType = string(r.Type)
			}
			choices[i] = fmt.Sprintf("%s (%s)", r.Name, displayType)
		}

		idx, err := m.console.Select(ctx, input.ConsoleOptions{
			Message: "Select a service to stream logs from:",
			Options: choices,
		})
		if err != nil {
			return nil, fmt.Errorf("selecting service: %w", err)
		}
		selected = streamable[idx]
	}

	displayType := azapi.GetResourceTypeDisplayName(selected.Type)
	if displayType == "" {
		displayType = string(selected.Type)
	}

	m.console.Message(ctx, fmt.Sprintf(
		"Streaming logs from %s (%s). Press Ctrl+C to stop.\n",
		output.WithHighLightFormat(selected.Name),
		displayType,
	))

	logStream, err := m.getLogStream(ctx, selected)
	if err != nil {
		return nil, fmt.Errorf("starting log stream for %s: %w", selected.Name, err)
	}
	defer logStream.Close()

	// Stream log data to the console output writer
	writer := m.console.GetWriter()
	if _, err := io.Copy(writer, logStream); err != nil {
		// Context cancellation (Ctrl+C) is expected when streaming
		if ctx.Err() != nil {
			return nil, nil
		}
		// Check for connection close/reset errors during streaming, which are
		// normal when the user stops streaming or the server closes the connection
		if isStreamClosedError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("streaming logs: %w", err)
	}

	return nil, nil
}

// isStreamClosedError reports whether the error indicates the log stream
// connection was closed, which is expected during normal termination.
func isStreamClosedError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "EOF")
}

// getLogStream returns a streaming reader for the given resource's application logs.
func (m *monitorAction) getLogStream(
	ctx context.Context,
	resource streamableResource,
) (io.ReadCloser, error) {
	subscriptionId := m.env.GetSubscriptionId()

	switch resource.Type {
	case azapi.AzureResourceTypeWebSite:
		return m.azureClient.GetAppServiceLogStream(
			ctx, subscriptionId, resource.ResourceGroup, resource.Name,
		)
	case azapi.AzureResourceTypeContainerApp:
		return m.containerAppService.GetLogStream(
			ctx, subscriptionId, resource.ResourceGroup, resource.Name,
		)
	default:
		return nil, fmt.Errorf(
			"log streaming is not supported for resource type %s", resource.Type,
		)
	}
}

func getCmdMonitorHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		fmt.Sprintf("Monitor a deployed application %s. For more information, go to: %s.",
			output.WithWarningFormat("(Beta)"),
			output.WithLinkFormat("https://aka.ms/azure-dev/monitor")), nil)
}

func getCmdMonitorHelpFooter(c *cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Open Application Insights Overview Dashboard.":    output.WithHighLightFormat("azd monitor --overview"),
		"Open Application Insights Live Metrics.":          output.WithHighLightFormat("azd monitor --live"),
		"Open Application Insights Logs.":                  output.WithHighLightFormat("azd monitor --logs"),
		"Stream application logs directly to the terminal": output.WithHighLightFormat("azd monitor --tail"),
	})
}
