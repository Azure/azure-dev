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

const tenantAgentApprovalURL = "https://aka.ms/m365agentadmin"

var teamsPublishScopes = []teamsPackScope{
	{flag: "shared", api: "Shared", summary: "shareable link distribution (no tenant-admin approval required)"},
	{flag: "tenant", api: "Tenant", summary: "organization-wide catalog (requires IT-admin approval; alias: org)"},
}

type publishFlags struct {
	name                        string
	scope                       string
	scopeSet                    bool
	displayName                 string
	appVersion                  string
	optionalPermissionScopes    []string
	optionalPermissionScopesSet bool
	accessBoundaries            []string
	accessBoundariesSet         bool
	clearAccessBoundaries       bool
	output                      string
	noPrompt                    bool
}

func newPublishCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &publishFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "publish [name]",
		Short: "Publish an activity agent as a Teams app to the Microsoft 365 store.",
		Long: "Publish a deployed Activity (Teams) agent as a Teams app.\n\n" +
			"The Microsoft 365 service builds the Teams app package server-side (the same package\n" +
			"'azd ai agent pack' produces) and publishes it to the Microsoft 365 store under the\n" +
			"requested scope, then returns the published title id and Teams app id.\n\n" +
			"Scopes:\n" +
			"  shared    shareable-link distribution (no tenant-admin approval required)\n" +
			"  tenant    organization-wide catalog (requires IT-admin approval)\n\n" +
			"If --scope is specified, it overrides activity.publish.publishScope in azure.yaml.\n" +
			"When omitted, azd uses the value from azure.yaml; if no value is configured,\n" +
			"Digital Workers default to tenant and simple activity agents default to shared.\n" +
			"Digital Worker publish supports tenant scope only (alias: org).\n\n" +
			"personal is not supported here: per-user install is a Teams client action, not a\n" +
			"store publish. For local testing, run azd ai agent pack and sideload with\n" +
			"atk install --scope personal.\n\n" +
			"This command requires the agent to have been deployed first ('azd deploy'). Any\n" +
			"failure (tenant policy, permissions, service outage) is reported as a command\n" +
			"failure rather than silently skipped.",
		Example: "  # Publish using the configured digital-worker scope (or shared for simple activity)\n" +
			"  azd ai agent publish\n\n" +
			"  # Explicitly override the configured scope for a specific agent\n" +
			"  azd ai agent publish my-agent --scope tenant\n\n" +
			"  # Publish organization-wide (requires IT-admin approval)\n" +
			"  azd ai agent publish --scope tenant",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				flags.name = args[0]
			}
			flags.scopeSet = cmd.Flags().Changed("scope")
			flags.optionalPermissionScopesSet = cmd.Flags().Changed("optional-permission-scope")
			flags.accessBoundariesSet = cmd.Flags().Changed("access-boundary")
			flags.output = extCtx.OutputFormat
			flags.noPrompt = extCtx.NoPrompt

			ctx := azdext.WithAccessToken(cmd.Context())
			action := &PublishAction{flags: flags}
			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVar(&flags.scope, "scope", "",
		fmt.Sprintf("Microsoft 365 publish scope (%s; Digital Workers require tenant)", joinScopesHelp(teamsPublishScopes)))
	cmd.Flags().StringVar(&flags.displayName, "display-name", "",
		"Display name for the Teams app. If specified, it overrides activity.publish.agentDisplayName "+
			"in azure.yaml; otherwise azd uses the azure.yaml value, and falls back to the agent name.")
	cmd.Flags().StringVar(&flags.appVersion, "app-version", "",
		"Version stamped into the Teams app manifest. If specified, it overrides activity.publish.appVersion "+
			"in azure.yaml; otherwise azd uses the azure.yaml value, and falls back to 1.0.0.")
	cmd.Flags().StringArrayVar(
		&flags.optionalPermissionScopes,
		"optional-permission-scope",
		nil,
		"Digital Worker permission in <resource-app-id>=<scope> form. Repeat to select multiple scopes.",
	)
	cmd.Flags().StringArrayVar(
		&flags.accessBoundaries,
		"access-boundary",
		nil,
		"Digital Worker access boundary. Repeat to select multiple developer boundaries.",
	)
	cmd.Flags().BoolVar(
		&flags.clearAccessBoundaries,
		"clear-access-boundaries",
		false,
		"Clear all existing Digital Worker access boundaries.",
	)
	cmd.MarkFlagsMutuallyExclusive("access-boundary", "clear-access-boundaries")

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
	optionalPermissionScopes, accessBoundaries, err := resolveDigitalWorkerPublishInputs(a.flags, packCtx)
	if err != nil {
		return err
	}

	request := buildTeamsAppPackageRequest(packCtx.botArmID, teamsAppRequestOptions{
		scope:                    scope,
		agentName:                packCtx.agentName,
		useCase:                  packCtx.activityProfile.UseCase,
		displayName:              a.flags.displayName,
		appVersion:               a.flags.appVersion,
		publish:                  activityPublishConfig(packCtx),
		optionalPermissionScopes: optionalPermissionScopes,
		accessBoundaries:         accessBoundaries,
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

func resolveDigitalWorkerPublishInputs(
	flags *publishFlags,
	packCtx *teamsPackContext,
) ([]agent_api.Microsoft365PermissionScopes, *[]string, error) {
	isDigitalWorker := packCtx.activityProfile.UseCase == project.ActivityUseCaseDigitalWorker
	publish := activityPublishConfig(packCtx)
	if !isDigitalWorker {
		hasDigitalWorkerConfig := publish != nil &&
			(len(publish.OptionalPermissionScopes) > 0 || publish.AccessBoundaries != nil)
		if flags.optionalPermissionScopesSet || flags.accessBoundariesSet ||
			flags.clearAccessBoundaries || hasDigitalWorkerConfig {
			return nil, nil, exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				"Digital Worker permission scopes and access boundaries cannot be used for a simple Activity agent",
				"remove --optional-permission-scope, --access-boundary, and --clear-access-boundaries, "+
					"or publish a Digital Worker",
			)
		}
		return nil, nil, nil
	}

	var permissions []agent_api.Microsoft365PermissionScopes
	var boundaries *[]string
	if publish != nil {
		permissions = make([]agent_api.Microsoft365PermissionScopes, 0, len(publish.OptionalPermissionScopes))
		for _, permission := range publish.OptionalPermissionScopes {
			scopes := make([]string, 0, len(permission.Scopes))
			for _, scope := range permission.Scopes {
				scopes = append(scopes, strings.TrimSpace(scope))
			}
			permissions = append(permissions, agent_api.Microsoft365PermissionScopes{
				ResourceAppID: strings.TrimSpace(permission.ResourceAppID),
				Scopes:        scopes,
			})
		}
		if publish.AccessBoundaries != nil {
			values := make([]string, 0, len(*publish.AccessBoundaries))
			for _, boundary := range *publish.AccessBoundaries {
				values = append(values, strings.TrimSpace(boundary))
			}
			boundaries = &values
		}
	}

	if flags.optionalPermissionScopesSet {
		var err error
		permissions, err = parseOptionalPermissionScopeFlags(flags.optionalPermissionScopes)
		if err != nil {
			return nil, nil, err
		}
	}
	if flags.accessBoundariesSet {
		values := make([]string, 0, len(flags.accessBoundaries))
		seen := make(map[string]struct{}, len(flags.accessBoundaries))
		for _, boundary := range flags.accessBoundaries {
			boundary = strings.TrimSpace(boundary)
			if _, ok := seen[boundary]; ok {
				continue
			}
			seen[boundary] = struct{}{}
			values = append(values, boundary)
		}
		if err := project.ValidateDigitalWorkerAccessBoundaries(values); err != nil {
			return nil, nil, exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("invalid --access-boundary: %s", err),
				"use a supported *.developers access boundary",
			)
		}
		boundaries = &values
	} else if flags.clearAccessBoundaries {
		values := []string{}
		boundaries = &values
	}

	effectivePublish := effectiveDigitalWorkerPublishConfig(publish, flags, permissions, boundaries)
	if effectivePublish != nil {
		if err := project.ValidateDigitalWorkerPublishConfig(effectivePublish); err != nil {
			return nil, nil, exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("invalid Digital Worker publish configuration: %s", err),
				"fix activity.publish in azure.yaml or pass valid publish override flags",
			)
		}
	}
	return permissions, boundaries, nil
}

func effectiveDigitalWorkerPublishConfig(
	publish *project.ActivityPublishConfig,
	flags *publishFlags,
	permissions []agent_api.Microsoft365PermissionScopes,
	boundaries *[]string,
) *project.ActivityPublishConfig {
	if publish == nil && len(permissions) == 0 && boundaries == nil {
		return nil
	}

	effective := &project.ActivityPublishConfig{}
	if publish != nil {
		*effective = *publish
	}
	if flags.scopeSet {
		effective.PublishScope = ""
	}
	if flags.optionalPermissionScopesSet {
		effective.OptionalPermissionScopes = make([]project.Microsoft365PermissionScopes, 0, len(permissions))
		for _, permission := range permissions {
			effective.OptionalPermissionScopes = append(
				effective.OptionalPermissionScopes,
				project.Microsoft365PermissionScopes{
					ResourceAppID: permission.ResourceAppID,
					Scopes:        append([]string(nil), permission.Scopes...),
				},
			)
		}
	}
	if flags.accessBoundariesSet || flags.clearAccessBoundaries {
		effective.AccessBoundaries = boundaries
	}

	return effective
}

func parseOptionalPermissionScopeFlags(values []string) ([]agent_api.Microsoft365PermissionScopes, error) {
	permissions := make([]agent_api.Microsoft365PermissionScopes, 0)
	resourceIndexes := make(map[string]int)
	seen := make(map[string]struct{})
	for _, value := range values {
		resourceAppID, scope, found := strings.Cut(value, "=")
		resourceAppID = strings.TrimSpace(resourceAppID)
		scope = strings.TrimSpace(scope)
		if !found || resourceAppID == "" || scope == "" {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("invalid optional permission scope %q", value),
				"use --optional-permission-scope <resource-app-id>=<scope>",
			)
		}
		key := resourceAppID + "\x00" + scope
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		index, ok := resourceIndexes[resourceAppID]
		if !ok {
			index = len(permissions)
			resourceIndexes[resourceAppID] = index
			permissions = append(permissions, agent_api.Microsoft365PermissionScopes{
				ResourceAppID: resourceAppID,
			})
		}
		permissions[index].Scopes = append(permissions[index].Scopes, scope)
	}
	return permissions, nil
}

func resolvePublishScope(flags *publishFlags, packCtx *teamsPackContext) (teamsPackScope, error) {
	scopeValue := flags.scope
	if !flags.scopeSet {
		if publish := activityPublishConfig(packCtx); publish != nil && strings.TrimSpace(publish.PublishScope) != "" {
			scopeValue = publish.PublishScope
		} else if packCtx.activityProfile.UseCase == project.ActivityUseCaseDigitalWorker {
			scopeValue = "tenant"
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
	if err := validateDigitalWorkerPublishScope(packCtx.activityProfile.UseCase, scope); err != nil {
		return teamsPackScope{}, err
	}
	return scope, nil
}

func validateDigitalWorkerPublishScope(useCase project.ActivityUseCase, scope teamsPackScope) error {
	if useCase != project.ActivityUseCaseDigitalWorker {
		return nil
	}
	if scope.flag == "tenant" {
		return nil
	}
	return exterrors.Validation(
		exterrors.CodeInvalidPublishScope,
		fmt.Sprintf("digital_worker publish does not support %q scope", scope.flag),
		"digital_worker supports tenant scope only; use --scope tenant (alias: org) "+
			"or set activity.publish.publishScope: tenant in azure.yaml",
	)
}

func activityPublishConfig(packCtx *teamsPackContext) *project.ActivityPublishConfig {
	if packCtx.activitySettings == nil {
		return nil
	}
	return packCtx.activitySettings.Publish
}

func resolvePackScope(value string, packCtx *teamsPackContext) (teamsPackScope, error) {
	scopeValue := strings.TrimSpace(value)
	if scopeValue == "" {
		if publish := activityPublishConfig(packCtx); publish != nil && strings.TrimSpace(publish.PublishScope) != "" {
			scopeValue = publish.PublishScope
		} else {
			scopeValue = "personal"
		}
	}
	return resolveTeamsPackScope(scopeValue)
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
			"deepLink":    deepLink,
		}
		if scope.flag == "tenant" || isDigitalWorker {
			payload["approvalLink"] = tenantAgentApprovalURL
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
