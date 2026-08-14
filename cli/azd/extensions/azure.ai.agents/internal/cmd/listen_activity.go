// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"log"
	"strings"
	"text/template"

	"azureaiagent/internal/pkg/botservice"
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// teamsSetupGuideFile is the name of the generated Teams onboarding guide.
const teamsSetupGuideFile = "TEAMS_APP_SETUP.md"

// writeTeamsSetupGuide is intentionally disabled for Digital Worker deployments.
// The Teams onboarding guide is not created automatically after deploy.
func writeTeamsSetupGuide(
	proj *azdext.ProjectConfig, svc *azdext.ServiceConfig, agentName, botName, msaAppID string,
) string {
	log.Printf("postdeploy: skipping Teams setup guide generation for agent %q; TEAMS_APP_SETUP.md is intentionally disabled", agentName)
	return ""
}

//go:embed assets/teams_app_setup_guide.md
var teamsSetupGuideMarkdown string

// teamsSetupGuideTmpl is the compiled onboarding guide. Keeping the markdown in
// an actual .md file (assets/teams_app_setup_guide.md) lets editors render and
// lint it and catch formatting errors that a Go string literal hides.
var teamsSetupGuideTmpl = template.Must(
	template.New("teamsSetupGuide").Parse(teamsSetupGuideMarkdown),
)

// teamsSetupGuideContent renders the Teams onboarding guide markdown. It gives
// concrete, minimal step-by-step instructions for the two manual actions
// (package the Teams app, then sideload it) and links to the official docs for
// detail. The single value the user must not get wrong is the bot id: a Teams
// app manifest's bots[].botId MUST equal this bot's msaAppId, which azd bound to
// the agent instance identity.
func teamsSetupGuideContent(agentName, botName, msaAppID string) string {
	var buf bytes.Buffer
	// Inputs are azd-controlled resource names and the template is compile-time
	// embedded, so execution cannot realistically fail.
	_ = teamsSetupGuideTmpl.Execute(&buf, struct {
		AgentName string
		BotName   string
		MsaAppID  string
	}{AgentName: agentName, BotName: botName, MsaAppID: msaAppID})
	return buf.String()
}

// printTeamsNextSteps is intentionally disabled for Digital Worker deployments.
func printTeamsNextSteps(botName, msaAppID, guidePath string) {
	return
}

// readEnvValue reads a required environment value, returning a descriptive error
// when it is missing or empty.
func readEnvValue(
	ctx context.Context, azdClient *azdext.AzdClient, envName, key string,
) (string, error) {
	resp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     key,
	})
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", key, err)
	}
	if resp.Value == "" {
		return "", fmt.Errorf("%s is not set in the environment", key)
	}
	return resp.Value, nil
}

func readOptionalEnvValue(ctx context.Context, azdClient *azdext.AzdClient, envName, key string) string {
	resp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     key,
	})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(resp.Value)
}

func activityBotTeardownTarget(
	persistedBotName string,
	persistedResourceGroup string,
	persistedOwned string,
) (string, string, bool) {
	botName := strings.TrimSpace(persistedBotName)
	resourceGroup := strings.TrimSpace(persistedResourceGroup)
	owned := strings.EqualFold(strings.TrimSpace(persistedOwned), "true")
	if botName == "" || resourceGroup == "" || !owned {
		return "", "", false
	}
	return botName, resourceGroup, true
}

// teardownActivityBots deletes the Azure Bot created for each activity-protocol
// agent during teardown. BotService resource names are globally unique, so an
// orphaned bot would collide with a future redeploy. It is best-effort: missing
// environment values or delete failures are logged and never block azd down.
func teardownActivityBots(
	ctx context.Context, azdClient *azdext.AzdClient, envName string, proj *azdext.ProjectConfig,
) {
	var activityAgents []string
	for _, svc := range proj.Services {
		if svc.Host != AiAgentHost {
			continue
		}
		ca, isHosted, _, err := project.LoadAgentDefinition(svc, proj.Path)
		if err != nil || !isHosted {
			continue
		}
		if project.IsActivityProtocol(ca) {
			activityAgents = append(activityAgents, svc.Name)
		}
	}
	if len(activityAgents) == 0 {
		return
	}

	subscriptionID, err := readEnvValue(ctx, azdClient, envName, "AZURE_SUBSCRIPTION_ID")
	if err != nil {
		log.Printf("postdown: skipping Teams bot cleanup: %v", err)
		return
	}
	tenantID, err := readEnvValue(ctx, azdClient, envName, "AZURE_TENANT_ID")
	if err != nil {
		log.Printf("postdown: skipping Teams bot cleanup: %v", err)
		return
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   tenantID,
			AdditionallyAllowedTenants: []string{"*"},
		},
	)
	if err != nil {
		log.Printf("postdown: skipping Teams bot cleanup: %v", err)
		return
	}

	botClient, err := botservice.NewClient(subscriptionID, cred, nil)
	if err != nil {
		log.Printf("postdown: skipping Teams bot cleanup: %v", err)
		return
	}

	for _, agentName := range activityAgents {
		botName, botResourceGroup, tracked := activityBotTeardownTarget(
			readOptionalEnvValue(ctx, azdClient, envName, envkey.AgentBotName(agentName)),
			readOptionalEnvValue(ctx, azdClient, envName, envkey.AgentBotResourceGroup(agentName)),
			readOptionalEnvValue(ctx, azdClient, envName, envkey.AgentBotOwned(agentName)),
		)
		if !tracked {
			continue
		}
		owned, ownershipErr := botClient.IsOwned(ctx, botResourceGroup, botName)
		if ownershipErr != nil {
			log.Printf("postdown: failed to verify ownership of Azure Bot %q: %v", botName, ownershipErr)
			continue
		}
		if !owned {
			log.Printf("postdown: leaving Azure Bot %q because it is not marked as created by azd", botName)
			continue
		}
		if err := botClient.DeleteBot(ctx, botResourceGroup, botName); err != nil {
			log.Printf("postdown: failed to delete Azure Bot %q: %v", botName, err)
			continue
		}
		fmt.Printf("Deleted Azure Bot %q for agent %q\n", botName, agentName)
	}
}
