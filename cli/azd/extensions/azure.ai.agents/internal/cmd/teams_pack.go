// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/botservice"
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// teamsPackScope is a user-facing Teams publish scope. It maps onto the
// Microsoft 365 service's PublishScope contract (Microsoft365Constants:
// Personal/Shared/Tenant).
type teamsPackScope struct {
	// flag is the value the user types on the command line (lowercase).
	flag string
	// api is the corresponding PublishScope value sent to the service.
	api string
	// summary explains, in one line, what the scope means for distribution.
	summary string
}

// teamsPackScopes enumerates the supported publish scopes. "personal" is the
// default because it needs no Teams admin approval (per-user sideload); "shared"
// distributes via a shareable link without tenant-admin approval; "tenant"
// publishes to the whole organization and requires IT-admin approval.
var teamsPackScopes = []teamsPackScope{
	{flag: "personal", api: "Personal", summary: "per-user sideload (no admin approval required)"},
	{flag: "shared", api: "Shared", summary: "shareable link distribution (no tenant-admin approval required)"},
	{flag: "tenant", api: "Tenant", summary: "organization-wide catalog (requires IT-admin approval)"},
}

// resolveTeamsPackScope validates a user-supplied scope value and returns the
// matching scope descriptor. An empty value resolves to the default ("personal").
// An unknown value is a loud, actionable validation error rather than a silent
// fallback, so a typo can never quietly publish to the wrong audience.
func resolveTeamsPackScope(value string) (teamsPackScope, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		normalized = "personal"
	}
	// "org" is accepted as a friendly alias for the Vienna API's "Tenant" scope.
	if normalized == "org" {
		normalized = "tenant"
	}
	for _, scope := range teamsPackScopes {
		if scope.flag == normalized {
			return scope, nil
		}
	}
	return teamsPackScope{}, exterrors.Validation(
		exterrors.CodeInvalidPublishScope,
		fmt.Sprintf("unsupported Teams publish scope %q", value),
		fmt.Sprintf("use one of: %s (alias: org)", strings.Join(teamsPackScopeFlags(), ", ")),
	)
}

// teamsPackScopeFlags returns the supported scope flag values in a stable order,
// for help text and error hints.
func teamsPackScopeFlags() []string {
	flags := make([]string, 0, len(teamsPackScopes))
	for _, scope := range teamsPackScopes {
		flags = append(flags, scope.flag)
	}
	sort.Strings(flags)
	return flags
}

// teamsPackContext holds everything the pack and publish commands need after
// resolving the target agent from the azd project and environment. Both commands
// operate on an agent that has already been deployed (the Azure Bot is created
// during 'azd deploy'), so this resolver requires a recorded deployment and fails
// loudly when the agent is missing, is not an activity agent, or has not been
// deployed yet.
type teamsPackContext struct {
	proj              *azdext.ProjectConfig
	svc               *azdext.ServiceConfig
	agentName         string
	botArmID          string
	blueprintClientID string
	agentClient       *agent_api.AgentClient
	activityProfile   project.ActivityProfile
	activitySettings  *project.ActivitySettings
}

// resolveTeamsPackContext resolves the target activity agent and derives the Azure
// Bot ARM id, credential, and Microsoft 365 client shared by the pack and publish
// commands. It enforces the preconditions loudly:
//   - the resolved service must be a hosted agent that speaks the Activity protocol
//     (pack/publish only make sense for Teams-facing agents);
//   - the agent must already be deployed (an AGENT_{KEY}_VERSION recorded by
//     'azd deploy'), because the bot the Teams app binds to is created during deploy.
func resolveTeamsPackContext(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	name string,
	noPrompt bool,
) (*teamsPackContext, error) {
	svc, proj, err := resolveAgentService(ctx, azdClient, name, noPrompt)
	if err != nil {
		return nil, err
	}

	ca, isHosted, _, err := project.LoadAgentDefinition(svc, proj.Path)
	if err != nil {
		return nil, err
	}
	if !isHosted || !project.ResolveActivityProfile(ca).IsActivity {
		return nil, exterrors.Validation(
			exterrors.CodeNotActivityAgent,
			fmt.Sprintf("agent service %q is not an Activity (Teams) agent", svc.Name),
			"'azd ai agent pack' and 'azd ai agent publish' only apply to hosted agents that "+
				"speak the Activity protocol; check the agent's protocols in azure.yaml",
		)
	}

	serviceTargetConfig, err := project.LoadServiceTargetAgentConfig(svc)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("failed to parse service target config: %s", err),
			"check the activity configuration in azure.yaml",
		)
	}
	activityProfile, err := project.ResolveActivityProfileWithSettings(ca, serviceTargetConfig.Activity)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("invalid Activity configuration: %s", err),
			"check the activity configuration in azure.yaml",
		)
	}

	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, exterrors.Dependency(
			exterrors.CodeEnvironmentNotFound,
			"no azd environment is selected",
			"run 'azd env select <name>' or deploy the agent with 'azd deploy' first",
		)
	}
	envName := envResp.Environment.Name

	// A recorded version is the signal that 'azd deploy' has run and created the bot
	// this Teams app binds to. Without it, packaging would reference a bot that does
	// not exist, so fail loudly with a clear next step instead of producing a broken
	// package.
	serviceKey := toServiceKey(svc.Name)
	agentName := readOptionalEnvValue(ctx, azdClient, envName, fmt.Sprintf("AGENT_%s_NAME", serviceKey))
	if strings.TrimSpace(agentName) == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeAgentNotDeployed,
			fmt.Sprintf("agent service %q has not been deployed in environment %q", svc.Name, envName),
			"run 'azd deploy' first; the deployed agent name is recorded during deploy and is required "+
				"before packaging or publishing",
		)
	}
	version := readOptionalEnvValue(ctx, azdClient, envName, fmt.Sprintf("AGENT_%s_VERSION", serviceKey))
	if strings.TrimSpace(version) == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeAgentNotDeployed,
			fmt.Sprintf("agent %q has not been deployed in environment %q", agentName, envName),
			"run 'azd deploy' first; the Teams bot is created during deploy and is required "+
				"before packaging or publishing",
		)
	}

	botArmID := ""
	blueprintClientID := ""
	if activityProfile.UseCase == project.ActivityUseCaseDigitalWorker {
		blueprintClientID, err = readEnvValue(
			ctx, azdClient, envName, envkey.AgentBlueprintClientID(svc.Name),
		)
		if err != nil {
			return nil, exterrors.Dependency(
				exterrors.CodeAgentNotDeployed,
				fmt.Sprintf("agent %q has no recorded blueprint client ID in environment %q", agentName, envName),
				"run 'azd deploy' first; the blueprint client ID is required before "+
					"publishing a digital_worker agent",
			)
		}
	} else {
		subscriptionID, readErr := readEnvValue(ctx, azdClient, envName, "AZURE_SUBSCRIPTION_ID")
		if readErr != nil {
			return nil, readErr
		}
		resourceGroup, readErr := readEnvValue(ctx, azdClient, envName, "AZURE_RESOURCE_GROUP")
		if readErr != nil {
			return nil, readErr
		}
		botName, readErr := readEnvValue(ctx, azdClient, envName, envkey.AgentBotName(svc.Name))
		if readErr != nil {
			return nil, exterrors.Dependency(
				exterrors.CodeAgentNotDeployed,
				fmt.Sprintf("agent %q has no recorded Teams bot name in environment %q", agentName, envName),
				"run 'azd deploy' first; the Teams bot name is recorded during deploy and is required "+
					"before packaging or publishing",
			)
		}
		botResourceGroup := readOptionalEnvValue(
			ctx, azdClient, envName, envkey.AgentBotResourceGroup(svc.Name),
		)
		botArmID, err = teamsBotArmID(subscriptionID, resourceGroup, botName, botResourceGroup)
		if err != nil {
			return nil, err
		}
	}

	endpoint, err := resolveAgentEndpoint(ctx, "", "")
	if err != nil {
		return nil, err
	}
	credential, err := newAgentCredential()
	if err != nil {
		return nil, err
	}

	return &teamsPackContext{
		proj:              proj,
		svc:               svc,
		agentName:         agentName,
		botArmID:          botArmID,
		blueprintClientID: blueprintClientID,
		agentClient:       agent_api.NewAgentClient(endpoint, credential),
		activityProfile:   activityProfile,
		activitySettings:  serviceTargetConfig.Activity,
	}, nil
}

func teamsBotArmID(subscriptionID, defaultResourceGroup, botName, botResourceGroup string) (string, error) {
	botName = strings.TrimSpace(botName)
	if botName == "" {
		return "", exterrors.Dependency(
			exterrors.CodeAgentNotDeployed,
			"the deployed Teams bot name is not recorded",
			"run 'azd deploy' first; the Teams bot name is recorded during deploy and is required "+
				"before packaging or publishing",
		)
	}
	resourceGroup := strings.TrimSpace(botResourceGroup)
	if resourceGroup == "" {
		resourceGroup = strings.TrimSpace(defaultResourceGroup)
	}
	return botservice.BotArmID(subscriptionID, resourceGroup, botName), nil
}

// teamsAppRequestOptions carries the user-overridable display metadata for a Teams
// app package/publish request. Zero-value fields fall back to sensible defaults
// derived from the agent name.
type teamsAppRequestOptions struct {
	scope             teamsPackScope
	displayName       string
	appVersion        string
	blueprintClientID string
	publish           *project.DigitalWorkerPublishConfig
}

// buildTeamsAppPackageRequest assembles the Microsoft 365 request body shared by
// the zip (pack) and publish endpoints. Only display metadata and the publish
// scope vary; the agent itself is resolved server-side from the route. Defaults
// mirror the postdeploy packaging path so a command run with no flags produces the
// same package the deploy hook would.
func buildTeamsAppPackageRequest(
	botArmID string,
	opts teamsAppRequestOptions,
) agent_api.TeamsAppPackageRequest {
	displayName := strings.TrimSpace(opts.displayName)
	appVersion := strings.TrimSpace(opts.appVersion)
	publish := opts.publish
	if publish != nil {
		if displayName == "" {
			displayName = strings.TrimSpace(publish.AgentDisplayName)
		}
		if appVersion == "" {
			appVersion = strings.TrimSpace(publish.AppVersion)
		}
		if strings.TrimSpace(publish.PublishScope) != "" {
			if scope, err := resolveTeamsPackScope(publish.PublishScope); err == nil {
				if strings.TrimSpace(opts.scope.api) == "" {
					opts.scope = scope
				}
			}
		}
	}
	if appVersion == "" {
		appVersion = "1.0.0"
	}
	if displayName == "" {
		displayName = "Agent"
	}

	request := agent_api.TeamsAppPackageRequest{
		BotServiceArmID:          botArmID,
		PublishScope:             opts.scope.api,
		AgentDisplayName:         displayName,
		AppVersion:               appVersion,
		ShortDescription:         fmt.Sprintf("%s agent", displayName),
		FullDescription:          fmt.Sprintf("%s agent on Microsoft Teams (activity protocol)", displayName),
		DeveloperName:            "Azure AI Foundry",
		DeveloperWebsiteURL:      "https://learn.microsoft.com/azure/ai-foundry/",
		PrivacyURL:               "https://learn.microsoft.com/azure/ai-foundry/",
		TermsOfUseURL:            "https://learn.microsoft.com/azure/ai-foundry/",
		CanRespondWithoutMention: true,
	}
	if publish != nil {
		request.PublishAsAutopilot = publish.PublishAsAutopilot
		request.UseAgenticUserTemplate = publish.AgenticUserTemplate != nil
		if publish.AgenticUserTemplate != nil {
			request.AgenticUserTemplate = &agent_api.AgenticUserTemplate{
				ID:                       publish.AgenticUserTemplate.ID,
				File:                     publish.AgenticUserTemplate.File,
				SchemaVersion:            publish.AgenticUserTemplate.SchemaVersion,
				AgentIdentityBlueprintID: opts.blueprintClientID,
				CommunicationProtocol:    publish.AgenticUserTemplate.CommunicationProtocol,
			}
		}
		if strings.TrimSpace(publish.AgentDisplayName) != "" {
			request.AgentDisplayName = publish.AgentDisplayName
		}
		if strings.TrimSpace(publish.ShortDescription) != "" {
			request.ShortDescription = publish.ShortDescription
		}
		if strings.TrimSpace(publish.FullDescription) != "" {
			request.FullDescription = publish.FullDescription
		}
		if strings.TrimSpace(publish.DeveloperName) != "" {
			request.DeveloperName = publish.DeveloperName
		}
		if strings.TrimSpace(publish.DeveloperWebsiteURL) != "" {
			request.DeveloperWebsiteURL = publish.DeveloperWebsiteURL
		}
		if strings.TrimSpace(publish.PrivacyURL) != "" {
			request.PrivacyURL = publish.PrivacyURL
		}
		if strings.TrimSpace(publish.TermsOfUseURL) != "" {
			request.TermsOfUseURL = publish.TermsOfUseURL
		}
		if publish.CanRespondWithoutMention != nil {
			request.CanRespondWithoutMention = *publish.CanRespondWithoutMention
		}
	}
	return request
}

// teamsAppDeepLink returns the Teams v2 launcher link for a Microsoft Organization
// Store title. Shared-scope Foundry publishes are discoverable through the MOS
// title id; the Teams app id alone can open a catalog app only after acquisition.
func teamsAppDeepLink(titleID string) string {
	return fmt.Sprintf(
		"https://teams.microsoft.com/v2/#/l/app/?source=agent-details-page&titleId=%s&launchAgent=join_launcher_web",
		url.QueryEscape(titleID),
	)
}

// validatePublishScope rejects scopes the Microsoft 365 publish backend does not
// support. "personal" (per-user install) is a Teams client action, not a store
// publish, so 'azd ai agent publish' cannot fulfill it; the user is pointed to the
// 'azd ai agent pack' + 'atk install' path instead.
func validatePublishScope(scope teamsPackScope) error {
	if scope.flag == "personal" {
		return exterrors.Validation(
			exterrors.CodeInvalidPublishScope,
			"'personal' scope is not supported by 'azd ai agent publish'",
			"personal install is a Teams client action, not a store publish; run "+
				"'azd ai agent pack' and sideload with 'atk install --scope personal', "+
				"or publish with --scope shared or --scope tenant",
		)
	}
	return nil
}
