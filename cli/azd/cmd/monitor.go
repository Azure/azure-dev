// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/pbnj/go-open"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func monitorCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		&monitorAction{
			rootOptions: rootOptions,
		},
		rootOptions,
		"monitor",
		"Monitor a deployed application.",
		`Monitor a deployed application.
		
Examples:

	$ azd monitor --overview
	$ azd monitor -–live
	$ azd monitor --logs
		
For more information, go to https://aka.ms/azure-dev/monitor.`,
	)
	return cmd
}

type monitorAction struct {
	monitorLive     bool
	monitorLogs     bool
	monitorOverview bool
	rootOptions     *commands.GlobalCommandOptions
}

func (m *monitorAction) SetupFlags(
	persis *pflag.FlagSet,
	local *pflag.FlagSet,
) {
	persis.BoolVar(&m.monitorLive, "live", false, "Open a browser to Application Insights Live Metrics. Live Metrics is currently not supported for Python applications.")
	persis.BoolVar(&m.monitorLogs, "logs", false, "Open a browser to Application Insights Logs.")
	persis.BoolVar(&m.monitorOverview, "overview", false, "Open a browser to Application Insights Overview Dashboard.")
}

func (m *monitorAction) Run(ctx context.Context, _ *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
	azCli := commands.GetAzCliFromContext(ctx)
	console := input.NewConsole(!m.rootOptions.NoPrompt)

	if err := ensureProject(azdCtx.ProjectPath()); err != nil {
		return err
	}

	if err := tools.EnsureInstalled(ctx, azCli); err != nil {
		return err
	}

	if err := ensureLoggedIn(ctx); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}

	if !m.monitorLive && !m.monitorLogs && !m.monitorOverview {
		m.monitorLive = true
	}

	env, err := loadOrInitEnvironment(ctx, &m.rootOptions.EnvironmentName, azdCtx, console)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	tenantId, err := azCli.GetSubscriptionTenant(ctx, env.GetSubscriptionId())
	if err != nil {
		return fmt.Errorf("getting tenant id for subscription: %w", err)
	}

	resourceGroups, err := azureutil.GetResourceGroupsForDeployment(ctx, azCli, env.GetSubscriptionId(), env.GetEnvName())
	if err != nil {
		return fmt.Errorf("discovering resource groups from deployment: %w", err)
	}

	var insightsResources []azcli.AzCliResource
	var portalResources []azcli.AzCliResource

	for _, resourceGroup := range resourceGroups {
		resources, err := azCli.ListResourceGroupResources(ctx, env.GetSubscriptionId(), resourceGroup)
		if err != nil {
			return fmt.Errorf("listing resources: %w", err)
		}

		for _, resource := range resources {
			switch resource.Type {
			case string(infra.AzureResourceTypePortalDashboard):
				portalResources = append(portalResources, resource)
			case string(infra.AzureResourceTypeAppInsightComponent):
				insightsResources = append(insightsResources, resource)
			}
		}
	}

	if len(insightsResources) == 0 && (m.monitorLive || m.monitorLogs) {
		return fmt.Errorf("application does not contain an Application Insights resource")
	}

	if len(portalResources) == 0 && m.monitorOverview {
		return fmt.Errorf("application does not contain an Application Insights dashboard")
	}

	openWithDefaultBrowser := func(url string) {
		fmt.Printf("Opening %s in the default browser...\n", url)

		if err := open.Open(url); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to open default browser: %s\n", err.Error())
		}
	}

	for _, insightsResource := range insightsResources {
		if m.monitorLive {
			openWithDefaultBrowser(fmt.Sprintf("https://app.azure.com/%s%s/quickPulse", tenantId, insightsResource.Id))
		}

		if m.monitorLogs {
			openWithDefaultBrowser(fmt.Sprintf("https://app.azure.com/%s%s/logs", tenantId, insightsResource.Id))
		}
	}

	for _, portalResource := range portalResources {
		if m.monitorOverview {
			openWithDefaultBrowser(fmt.Sprintf("https://portal.azure.com/#@%s/dashboard/arm%s", tenantId, portalResource.Id))
		}
	}

	return nil
}
