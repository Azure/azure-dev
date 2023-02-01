// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/cli/browser"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type monitorFlags struct {
	monitorLive     bool
	monitorLogs     bool
	monitorOverview bool
	global          *internal.GlobalCommandOptions
	envFlag
}

func (m *monitorFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(
		&m.monitorLive,
		"live",
		false,
		"Open a browser to Application Insights Live Metrics. Live Metrics is currently not supported for Python apps.",
	)
	local.BoolVar(&m.monitorLogs, "logs", false, "Open a browser to Application Insights Logs.")
	local.BoolVar(&m.monitorOverview, "overview", false, "Open a browser to Application Insights Overview Dashboard.")
	m.envFlag.Bind(local, global)
	m.global = global
}

func newMonitorFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *monitorFlags {
	flags := &monitorFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newMonitorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Monitor a deployed app.",
		Long: `Monitor a deployed app.

Examples:

	$ azd monitor --overview
	$ azd monitor -–live
	$ azd monitor --logs

For more information, go to https://aka.ms/azure-dev/monitor.`,
	}

	return cmd
}

type monitorAction struct {
	azdCtx  *azdcontext.AzdContext
	azCli   azcli.AzCli
	console input.Console
	flags   *monitorFlags
}

func newMonitorAction(
	azdCtx *azdcontext.AzdContext,
	azCli azcli.AzCli,
	console input.Console,
	flags *monitorFlags,
) actions.Action {
	return &monitorAction{
		azdCtx:  azdCtx,
		azCli:   azCli,
		console: console,
		flags:   flags,
	}
}

func (m *monitorAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if !m.flags.monitorLive && !m.flags.monitorLogs && !m.flags.monitorOverview {
		m.flags.monitorOverview = true
	}

	env, err := loadOrInitEnvironment(ctx, &m.flags.environmentName, m.azdCtx, m.console, m.azCli)
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	account, err := m.azCli.GetAccount(ctx, env.GetSubscriptionId())
	if err != nil {
		return nil, fmt.Errorf("getting tenant id for subscription: %w", err)
	}

	resourceManager := infra.NewAzureResourceManager(m.azCli)
	resourceGroups, err := resourceManager.GetResourceGroupsForEnvironment(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("discovering resource groups from deployment: %w", err)
	}

	var insightsResources []azcli.AzCliResource
	var portalResources []azcli.AzCliResource

	for _, resourceGroup := range resourceGroups {
		resources, err := m.azCli.ListResourceGroupResources(ctx, env.GetSubscriptionId(), resourceGroup.Name, nil)
		if err != nil {
			return nil, fmt.Errorf("listing resources: %w", err)
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

	if len(insightsResources) == 0 && (m.flags.monitorLive || m.flags.monitorLogs) {
		return nil, fmt.Errorf("application does not contain an Application Insights resource")
	}

	if len(portalResources) == 0 && m.flags.monitorOverview {
		return nil, fmt.Errorf("application does not contain an Application Insights dashboard")
	}

	openWithDefaultBrowser := func(url string) {
		fmt.Fprintf(m.console.Handles().Stdout, "Opening %s in the default browser...\n", url)

		// In Codespaces and devcontainers a $BROWSER environment variable is
		// present whose value is an executable that launches the browser when
		// called with the form:
		// $BROWSER <url>

		const BrowserEnvVarName = "BROWSER"

		if envBrowser := os.Getenv(BrowserEnvVarName); len(envBrowser) > 0 {
			if err := exec.Command(envBrowser, url).Run(); err != nil {
				fmt.Fprintf(
					m.console.Handles().Stderr,
					"warning: failed to open browser configured by $BROWSER: %s\n",
					err.Error(),
				)
			}
			return
		}

		if err := browser.OpenURL(url); err != nil {
			fmt.Fprintf(m.console.Handles().Stderr, "warning: failed to open default browser: %s\n", err.Error())
		}
	}

	for _, insightsResource := range insightsResources {
		if m.flags.monitorLive {
			openWithDefaultBrowser(
				fmt.Sprintf("https://app.azure.com/%s%s/quickPulse", account.TenantId, insightsResource.Id),
			)
		}

		if m.flags.monitorLogs {
			openWithDefaultBrowser(fmt.Sprintf("https://app.azure.com/%s%s/logs", account.TenantId, insightsResource.Id))
		}
	}

	for _, portalResource := range portalResources {
		if m.flags.monitorOverview {
			openWithDefaultBrowser(
				fmt.Sprintf("https://portal.azure.com/#@%s/dashboard/arm%s", account.TenantId, portalResource.Id),
			)
		}
	}

	return nil, nil
}
