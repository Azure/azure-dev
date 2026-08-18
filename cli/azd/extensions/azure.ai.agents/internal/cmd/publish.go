// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

const tenantAgentApprovalURL = "https://admin.cloud.microsoft/?#/agents/all/requested"

var teamsPublishScopes = []teamsPackScope{
	{flag: "shared", api: "Shared", summary: "shareable link distribution (no tenant-admin approval required)"},
	{flag: "tenant", api: "Tenant", summary: "organization-wide catalog (requires IT-admin approval; alias: org)"},
}

type publishFlags struct {
	name        string
	scope       string
	scopeSet    bool
	displayName string
	appVersion  string
	output      string
	noPrompt    bool
}

func newPublishCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &publishFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "publish [name]",
		Short: "Publish an activity agent as a Teams app to the Microsoft 365 store.",
		Long: `Publish a deployed Activity (Teams) agent as a Teams app.

The Microsoft 365 service builds the Teams app package server-side (the same package
'azd ai agent pack' produces) and publishes it to the Microsoft 365 store under the
requested scope, then returns the published title id and Teams app id.

Scopes:
	shared    shareable-link distribution (no tenant-admin approval required)
  tenant    organization-wide catalog (requires IT-admin approval)

For a digital worker, an explicit '--scope' overrides
'activity.publish.publishScope' in azure.yaml. When '--scope' is omitted, the
azure.yaml value is used. Simple activity agents default to 'shared'.

'personal' is not supported here: per-user install is a Teams client action, not a
store publish. For local testing, run 'azd ai agent pack' and sideload with
'atk install --scope personal'.

This command requires the agent to have been deployed first ('azd deploy'). Any
failure (tenant policy, permissions, service outage) is reported as a command
failure rather than silently skipped.`,
		Example: `  # Publish using the configured digital-worker scope (or shared for simple activity)
  azd ai agent publish

  # Explicitly override the configured scope for a specific agent
  azd ai agent publish my-agent --scope shared

  # Publish organization-wide (requires IT-admin approval)
  azd ai agent publish --scope tenant`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				flags.name = args[0]
			}
			flags.scopeSet = cmd.Flags().Changed("scope")
			flags.output = extCtx.OutputFormat
			flags.noPrompt = extCtx.NoPrompt

			ctx := azdext.WithAccessToken(cmd.Context())
			action := &PublishAction{flags: flags}
			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVar(&flags.scope, "scope", "",
		fmt.Sprintf("Publish scope (%s)", joinScopesHelp(teamsPublishScopes)))
	cmd.Flags().StringVar(&flags.displayName, "display-name", "",
		"Display name for the Teams app (defaults to the agent name)")
	cmd.Flags().StringVar(&flags.appVersion, "app-version", "",
		"Version stamped into the Teams app manifest (defaults to the configured value or 1.0.0)")

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "none"},
		Default:       "none",
	})

	return cmd
}

// PublishAction implements the agent publish command.
type PublishAction struct {
	flags *publishFlags
}

func (a *PublishAction) Run(ctx context.Context) error {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	packCtx, err := resolveTeamsPackContext(ctx, azdClient, a.flags.name, a.flags.noPrompt)
	if err != nil {
		return err
	}

	scope, err := resolvePublishScope(a.flags, packCtx)
	if err != nil {
		return err
	}

	request := buildTeamsAppPackageRequest(packCtx.botArmID, teamsAppRequestOptions{
		scope:             scope,
		agentName:         packCtx.agentName,
		displayName:       a.flags.displayName,
		appVersion:        a.flags.appVersion,
		blueprintClientID: packCtx.blueprintClientID,
		publish:           digitalWorkerPublishConfig(packCtx),
	})

	if a.flags.output != "json" {
		fmt.Printf(
			"Publishing Teams app %q for agent %q (scope: %s)...\n",
			request.AgentDisplayName,
			packCtx.agentName,
			scope.flag,
		)
	}

	result, err := packCtx.agentClient.PublishTeamsApp(
		ctx, packCtx.agentName, request, microsoft365APIVersion(packCtx),
	)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpPublishTeamsApp)
	}

	deepLink := teamsAppDeepLink(result.TitleID)
	isDigitalWorker := packCtx.activityProfile.UseCase == project.ActivityUseCaseDigitalWorker

	return writePublishResult(
		os.Stdout, a.flags.output, result, scope, request.AgentDisplayName, packCtx.agentName, deepLink, isDigitalWorker,
	)
}

func resolvePublishScope(flags *publishFlags, packCtx *teamsPackContext) (teamsPackScope, error) {
	scopeValue := flags.scope
	if !flags.scopeSet {
		if publish := digitalWorkerPublishConfig(packCtx); publish != nil && strings.TrimSpace(publish.PublishScope) != "" {
			scopeValue = publish.PublishScope
		} else {
			scopeValue = "shared"
		}
	}

	scope, err := resolveTeamsPackScope(scopeValue)
	if err != nil {
		return teamsPackScope{}, err
	}
	// The Microsoft 365 publish backend does not support "personal": installing an
	// app for a single user (personal sideload) is a Teams client action, not a store
	// publish. Reject it loudly and point to the pack + sideload path instead of
	// silently publishing to a different audience.
	if err := validatePublishScope(scope); err != nil {
		return teamsPackScope{}, err
	}
	return scope, nil
}

func digitalWorkerPublishConfig(packCtx *teamsPackContext) *project.DigitalWorkerPublishConfig {
	if packCtx.activityProfile.UseCase != project.ActivityUseCaseDigitalWorker || packCtx.activitySettings == nil {
		return nil
	}
	return packCtx.activitySettings.Publish
}

func microsoft365APIVersion(packCtx *teamsPackContext) string {
	if packCtx.activityProfile.UseCase == project.ActivityUseCaseDigitalWorker {
		return agent_api.Microsoft365DigitalWorkerAPIVersion
	}
	return agent_api.Microsoft365APIVersion
}

func writePublishResult(
	w io.Writer,
	output string,
	result *agent_api.TeamsAppPublishResult,
	scope teamsPackScope,
	displayName string,
	agentName string,
	deepLink string,
	isDigitalWorker bool,
) error {
	if output == "json" {
		payload := map[string]string{
			"titleId":     result.TitleID,
			"teamsAppId":  result.TeamsAppID,
			"scope":       scope.flag,
			"displayName": displayName,
		}
		if scope.flag == "tenant" || isDigitalWorker {
			payload["approvalLink"] = tenantAgentApprovalURL
		}
		if scope.flag != "tenant" {
			payload["deepLink"] = deepLink
		}
		data, jsonErr := json.MarshalIndent(payload, "", "  ")
		if jsonErr != nil {
			return fmt.Errorf("failed to marshal response: %w", jsonErr)
		}
		_, err := fmt.Fprintln(w, string(data))
		return err
	}

	agentType := "Teams app"
	if isDigitalWorker {
		agentType = "Digital Worker"
	}
	fmt.Fprintf(w, "Published %s %q for agent %q (scope: %s)\n", agentType, displayName, agentName, scope.flag)
	fmt.Fprintf(w, "  Title ID:     %s\n", result.TitleID)
	fmt.Fprintf(w, "  Teams App ID: %s\n", result.TeamsAppID)

	switch {
	case isDigitalWorker && scope.flag == "shared":
		fmt.Fprintf(w, "  Install link: %s\n", deepLink)
		fmt.Fprintln(w, "Open or share the install link to add the Digital Worker in Teams.")
		fmt.Fprintln(w, "The first attempt to create an instance may submit an activation request if the "+
			"Digital Worker template is not yet active in the tenant.")
		fmt.Fprintf(w, "  Admin approval: %s\n", tenantAgentApprovalURL)
		fmt.Fprintln(w, "After approval, reopen the install link to create your personal instance.")
	case isDigitalWorker && scope.flag == "tenant":
		fmt.Fprintln(w, "The Digital Worker was submitted for tenant administration and may require approval "+
			"and template activation before users can create instances.")
		fmt.Fprintf(w, "  Admin approval: %s\n", tenantAgentApprovalURL)
		fmt.Fprintln(w, "After approval, users can open the Digital Worker in Teams and create their personal instances.")
	case scope.flag == "tenant":
		fmt.Fprintln(w, "The app is submitted to the organization catalog and awaits IT-admin approval.")
		fmt.Fprintf(w, "  Admin approval: %s\n", tenantAgentApprovalURL)
	default:
		fmt.Fprintf(w, "  Install link: %s\n", deepLink)
		fmt.Fprintln(w, "Share this link with users in the target tenant. Users can add the app directly, "+
			"subject to their tenant's app installation policies.")
	}
	return nil
}
