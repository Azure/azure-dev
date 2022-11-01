// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
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
}

func (m *monitorFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(
		&m.monitorLive,
		"live",
		false,
		"Open a browser to Application Insights Live Metrics. Live Metrics is currently not supported for Python applications.",
	)
	local.BoolVar(&m.monitorLogs, "logs", false, "Open a browser to Application Insights Logs.")
	local.BoolVar(&m.monitorOverview, "overview", false, "Open a browser to Application Insights Overview Dashboard.")
	m.global = global
}

func monitorCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *monitorFlags) {
	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Monitor a deployed application.",
		Long: `Monitor a deployed application.
		
Examples:

	$ azd monitor --overview
	$ azd monitor -–live
	$ azd monitor --logs
		
For more information, go to https://aka.ms/azure-dev/monitor.`,
	}
	flags := &monitorFlags{}
	flags.Bind(cmd.Flags(), global)
	return cmd, flags
}

type monitorAction struct {
	azdCtx  *azdcontext.AzdContext
	azCli   azcli.AzCli
	console input.Console
	flags   monitorFlags
}

func newMonitorAction(
	azdCtx *azdcontext.AzdContext,
	azCli azcli.AzCli,
	console input.Console,
	flags monitorFlags,
) *monitorAction {
	return &monitorAction{
		azdCtx:  azdCtx,
		azCli:   azCli,
		console: console,
		flags:   flags,
	}
}

func (m *monitorAction) Run(ctx context.Context) error {
	if err := ensureProject(m.azdCtx.ProjectPath()); err != nil {
		return err
	}

	if err := tools.EnsureInstalled(ctx, m.azCli); err != nil {
		return err
	}

	if err := ensureLoggedIn(ctx); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}

	if !m.flags.monitorLive && !m.flags.monitorLogs && !m.flags.monitorOverview {
		m.flags.monitorOverview = true
	}

	env, ctx, err := loadOrInitEnvironment(ctx, &m.flags.global.EnvironmentName, m.azdCtx, m.console)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	account, err := m.azCli.GetAccount(ctx, env.GetSubscriptionId())
	if err != nil {
		return fmt.Errorf("getting tenant id for subscription: %w", err)
	}

	resourceManager := infra.NewAzureResourceManager(ctx)
	resourceGroups, err := resourceManager.GetResourceGroupsForEnvironment(ctx, env)
	if err != nil {
		return fmt.Errorf("discovering resource groups from deployment: %w", err)
	}

	var insightsResources []azcli.AzCliResource
	var portalResources []azcli.AzCliResource

	for _, resourceGroup := range resourceGroups {
		resources, err := m.azCli.ListResourceGroupResources(ctx, env.GetSubscriptionId(), resourceGroup.Name, nil)
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

	if len(insightsResources) == 0 && (m.flags.monitorLive || m.flags.monitorLogs) {
		return fmt.Errorf("application does not contain an Application Insights resource")
	}

	if len(portalResources) == 0 && m.flags.monitorOverview {
		return fmt.Errorf("application does not contain an Application Insights dashboard")
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

	return nil
}
