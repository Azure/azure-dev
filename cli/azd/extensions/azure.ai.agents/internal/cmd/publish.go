// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type publishFlags struct {
	name        string
	scope       string
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
  shared    shareable-link distribution (no tenant-admin approval required) [default]
  org       organization-wide catalog (requires IT-admin approval)

'personal' is not supported here: per-user install is a Teams client action, not a
store publish. For local testing, run 'azd ai agent pack' and sideload with
'atk install --scope personal'.

This command requires the agent to have been deployed first ('azd deploy'). Any
failure (tenant policy, permissions, service outage) is reported as a command
failure rather than silently skipped.`,
		Example: `  # Publish for shareable-link distribution (default)
  azd ai agent publish

  # Publish a specific agent
  azd ai agent publish my-agent --scope shared

  # Publish organization-wide (requires IT-admin approval)
  azd ai agent publish --scope org`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				flags.name = args[0]
			}
			flags.output = extCtx.OutputFormat
			flags.noPrompt = extCtx.NoPrompt

			ctx := azdext.WithAccessToken(cmd.Context())
			action := &PublishAction{flags: flags}
			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVar(&flags.scope, "scope", "shared",
		fmt.Sprintf("Publish scope (%s)", joinScopeHelp()))
	cmd.Flags().StringVar(&flags.displayName, "display-name", "",
		"Display name for the Teams app (defaults to the agent name)")
	cmd.Flags().StringVar(&flags.appVersion, "app-version", "1.0.0",
		"Version stamped into the Teams app manifest")

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
	scope, err := resolveTeamsPackScope(a.flags.scope)
	if err != nil {
		return err
	}
	// The Microsoft 365 publish backend does not support "personal": installing an
	// app for a single user (personal sideload) is a Teams client action, not a store
	// publish. Reject it loudly and point to the pack + sideload path instead of
	// silently publishing to a different audience.
	if err := validatePublishScope(scope); err != nil {
		return err
	}

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	packCtx, err := resolveTeamsPackContext(ctx, azdClient, a.flags.name, a.flags.noPrompt)
	if err != nil {
		return err
	}

	displayName := a.flags.displayName
	if displayName == "" {
		displayName = packCtx.agentName
	}

	request := buildTeamsAppPackageRequest(packCtx.botArmID, teamsAppRequestOptions{
		scope:       scope,
		displayName: displayName,
		appVersion:  a.flags.appVersion,
	})

	fmt.Printf("Publishing Teams app for agent %q (scope: %s)...\n", packCtx.agentName, scope.flag)

	result, err := packCtx.agentClient.PublishTeamsApp(
		ctx, packCtx.agentName, request, agent_api.Microsoft365APIVersion,
	)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpPublishTeamsApp)
	}

	deepLink := teamsAppDeepLink(result.TeamsAppID)

	if a.flags.output == "json" {
		payload := map[string]string{
			"titleId":    result.TitleID,
			"teamsAppId": result.TeamsAppID,
			"scope":      scope.flag,
			"deepLink":   deepLink,
		}
		data, jsonErr := json.MarshalIndent(payload, "", "  ")
		if jsonErr != nil {
			return fmt.Errorf("failed to marshal response: %w", jsonErr)
		}
		fmt.Println(string(data))
		return nil
	}

	fmt.Printf("Published Teams app for agent %q (scope: %s)\n", packCtx.agentName, scope.flag)
	fmt.Printf("  Title ID:     %s\n", result.TitleID)
	fmt.Printf("  Teams App ID: %s\n", result.TeamsAppID)
	fmt.Printf("  Install link: %s\n", deepLink)
	switch scope.flag {
	case "org":
		fmt.Println("The app is submitted to the organization catalog and awaits IT-admin approval.")
	default:
		fmt.Println("Share the install link above; recipients can add the app without tenant-admin approval.")
	}
	return nil
}
