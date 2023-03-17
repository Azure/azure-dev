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
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
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
	return &cobra.Command{
		Use:   "monitor",
		Short: "Monitor a deployed application.",
	}
}

type monitorAction struct {
	azdCtx      *azdcontext.AzdContext
	env         *environment.Environment
	subResolver account.SubscriptionTenantResolver
	azCli       azcli.AzCli
	console     input.Console
	flags       *monitorFlags
}

func newMonitorAction(
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	subResolver account.SubscriptionTenantResolver,
	azCli azcli.AzCli,
	console input.Console,
	flags *monitorFlags,
) actions.Action {
	return &monitorAction{
		azdCtx:      azdCtx,
		env:         env,
		azCli:       azCli,
		console:     console,
		flags:       flags,
		subResolver: subResolver,
	}
}

func (m *monitorAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if !m.flags.monitorLive && !m.flags.monitorLogs && !m.flags.monitorOverview {
		m.flags.monitorOverview = true
	}

	resourceManager := infra.NewAzureResourceManager(m.azCli)
	resourceGroups, err := resourceManager.GetResourceGroupsForEnvironment(
		ctx, m.env.GetSubscriptionId(), m.env.GetEnvName())
	if err != nil {
		return nil, fmt.Errorf("discovering resource groups from deployment: %w", err)
	}

	var insightsResources []azcli.AzCliResource
	var portalResources []azcli.AzCliResource

	for _, resourceGroup := range resourceGroups {
		resources, err := m.azCli.ListResourceGroupResources(
			ctx, azure.SubscriptionFromRID(resourceGroup.Id), resourceGroup.Name, nil)
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

	tenantId, err := m.subResolver.LookupTenant(ctx, m.env.GetSubscriptionId())
	if err != nil {
		return nil, err
	}

	for _, insightsResource := range insightsResources {
		if m.flags.monitorLive {
			openWithDefaultBrowser(
				fmt.Sprintf("https://app.azure.com/%s%s/quickPulse", tenantId, insightsResource.Id),
			)
		}

		if m.flags.monitorLogs {
			openWithDefaultBrowser(fmt.Sprintf("https://app.azure.com/%s%s/logs", tenantId, insightsResource.Id))
		}
	}

	for _, portalResource := range portalResources {
		if m.flags.monitorOverview {
			openWithDefaultBrowser(
				fmt.Sprintf("https://portal.azure.com/#@%s/dashboard/arm%s", tenantId, portalResource.Id),
			)
		}
	}

	return nil, nil
}

func getCmdMonitorHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(fmt.Sprintf("Monitor a deployed application. For more information, go to: %s.",
		output.WithLinkFormat("https://aka.ms/azure-dev/monitor")), nil)
}

func getCmdMonitorHelpFooter(c *cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Open Application Insights Overview Dashboard.": output.WithHighLightFormat("azd monitor --overview"),
		"Open Application Insights Live Metrics.":       output.WithHighLightFormat("azd monitor --live"),
		"Open Application Insights Logs.":               output.WithHighLightFormat("azd monitor --logs"),
	})
}
