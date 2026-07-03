// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/botservice"
	"azureaiagent/internal/pkg/paths"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// ensureActivityBot provisions the Azure Bot + Microsoft Teams channel for an
// activity-protocol (Teams) agent during postdeploy. It is a no-op for any agent
// that does not opt into the Activity protocol, so non-activity deployments are
// completely unaffected.
//
// Scope: azd owns the Azure resource plane only — create the bot, bind it to the
// agent instance identity, enable the Teams channel, and point its messaging
// endpoint at the agent. Teams app packaging and install live on the M365/Graph
// plane and stay out of azd; postdeploy prints a guide for those manual steps.
func ensureActivityBot(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	cred azcore.TokenCredential,
	envName string,
	svc *azdext.ServiceConfig,
	proj *azdext.ProjectConfig,
	projectEndpoint string,
	tenantID string,
) error {
	ca, isHosted, _, err := project.LoadAgentDefinition(svc, proj.Path)
	if err != nil || !isHosted {
		return nil
	}

	profile := project.ResolveActivityProfile(ca)
	if !profile.IsActivity {
		return nil
	}

	// Only activity agents pay for the version lookup below; this keeps the base
	// postdeploy path (slimmed on main) untouched for every other agent.
	//
	// Phase 1 supports the simple use case only: the bot msaAppId is the agent
	// instance identity client id, which only exists after the agent version is
	// created during deploy. Fetch the active version to read that identity.
	serviceKey := toServiceKey(svc.Name)
	versionResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     fmt.Sprintf("AGENT_%s_VERSION", serviceKey),
	})
	if err != nil {
		return fmt.Errorf("failed to read AGENT_%s_VERSION for %q: %w", serviceKey, svc.Name, err)
	}
	if versionResp.Value == "" {
		return fmt.Errorf(
			"activity agent %q has no recorded version yet; cannot bind the Teams bot. "+
				"Re-run 'azd deploy' once the agent version is active.",
			svc.Name,
		)
	}

	agentClient := agent_api.NewAgentClient(projectEndpoint, cred)
	versionObj, err := agentClient.GetAgentVersion(
		ctx, svc.Name, versionResp.Value, DefaultAgentAPIVersion,
	)
	if err != nil {
		return fmt.Errorf(
			"failed to fetch agent version for %s/%s: %w",
			svc.Name, versionResp.Value, err,
		)
	}

	if versionObj == nil || versionObj.InstanceIdentity == nil ||
		versionObj.InstanceIdentity.ClientID == "" {
		return fmt.Errorf(
			"activity agent %q has no instance identity client id yet; cannot bind the "+
				"Teams bot. Re-run 'azd deploy' once the agent version is active.",
			svc.Name,
		)
	}
	msaAppID := versionObj.InstanceIdentity.ClientID

	subscriptionID, err := readEnvValue(ctx, azdClient, envName, "AZURE_SUBSCRIPTION_ID")
	if err != nil {
		return err
	}
	resourceGroup, err := readEnvValue(ctx, azdClient, envName, "AZURE_RESOURCE_GROUP")
	if err != nil {
		return err
	}

	botClient, err := botservice.NewClient(subscriptionID, cred, nil)
	if err != nil {
		return err
	}

	// The API agent name is the service name (deploy fetched the version with it),
	// so the messaging endpoint and bot name must use the same value.
	agentName := svc.Name
	botName := botservice.BotName(agentName, botservice.BotScopeSalt(subscriptionID, resourceGroup))

	cfg := botservice.BotConfig{
		ResourceGroup:     resourceGroup,
		BotName:           botName,
		MsaAppID:          msaAppID,
		TenantID:          tenantID,
		MessagingEndpoint: botservice.MessagingEndpoint(projectEndpoint, agentName),
		DisplayName:       agentName,
	}

	fmt.Printf("Configuring Azure Bot %q for Teams (activity protocol)...\n", botName)
	if err := botClient.EnsureBot(ctx, cfg); err != nil {
		return err
	}

	// Write a persistent, generic setup guide next to the agent code (the azd
	// progress UI swallows postdeploy stdout, so a file is the reliable way to
	// hand the user the manual M365 steps) and print a short pointer to it.
	guidePath := writeTeamsSetupGuide(proj, svc, agentName, botName, msaAppID)
	printTeamsNextSteps(botName, msaAppID, guidePath)
	return nil
}

// teamsSetupGuideFile is the name of the generated Teams onboarding guide.
const teamsSetupGuideFile = "TEAMS_APP_SETUP.md"

// writeTeamsSetupGuide writes a generic, simplified Teams onboarding guide next
// to the agent source so the user can package and sideload their Teams app after
// deploy. It returns the written path, or "" on any failure (best-effort: never
// blocks or fails the deploy). The guide is deploy-agnostic and links to the
// official Microsoft Learn docs rather than any sample-specific scripts.
func writeTeamsSetupGuide(
	proj *azdext.ProjectConfig, svc *azdext.ServiceConfig, agentName, botName, msaAppID string,
) string {
	guidePath, err := paths.JoinAllowRoot(proj.GetPath(), svc.GetRelativePath(), teamsSetupGuideFile)
	if err != nil {
		log.Printf("postdeploy: skipping Teams setup guide: %v", err)
		return ""
	}
	if err := os.WriteFile(guidePath, []byte(teamsSetupGuideContent(agentName, botName, msaAppID)), 0o600); err != nil {
		log.Printf("postdeploy: failed to write Teams setup guide %q: %v", guidePath, err)
		return ""
	}
	return guidePath
}

// teamsSetupGuideContent renders the Teams onboarding guide markdown. It gives
// concrete, minimal step-by-step instructions for the two manual actions
// (package the Teams app, then sideload it) and links to the official docs for
// detail. The single value the user must not get wrong is the bot id: a Teams
// app manifest's bots[].botId MUST equal this bot's msaAppId, which azd bound to
// the agent instance identity.
func teamsSetupGuideContent(agentName, botName, msaAppID string) string {
	return fmt.Sprintf(`# Connect %[1]s to Microsoft Teams

`+"`azd deploy`"+` already did the Azure side for you:

- Azure Bot: `+"`%[2]s`"+` (Microsoft Teams channel enabled)
- Bot ID (msaAppId): `+"`%[3]s`"+`  <- you will paste this as the bot id

Two manual steps remain: (A) create a Teams app package, then (B) upload it.
They are the same for any activity-protocol agent.

## A. Create the Teams app package

Pick ONE of the two ways below.

### Easiest — Teams Developer Portal (no files by hand)

1. Open https://dev.teams.microsoft.com/apps and select **+ New app**; enter a name.
2. Fill **Basic information** (short/long description, developer name and URLs).
3. Left menu **App features** -> **Bot** -> **Select an existing bot** -> enter the
   Bot ID `+"`%[3]s`"+`, tick the **Personal** scope, then **Save**.
4. **Publish** -> **Download the app package** — this gives you a ready-to-upload .zip.

Developer Portal guide: https://learn.microsoft.com/microsoftteams/platform/concepts/build-and-test/teams-developer-portal

### Or by hand — build the .zip yourself

Put these three files in a folder and zip them at the **root** (not inside a subfolder):

- `+"`manifest.json`"+` (below)
- `+"`color.png`"+`  — 192x192 px
- `+"`outline.png`"+` — 32x32 px, transparent background

`+"```json"+`
{
  "$schema": "https://developer.microsoft.com/json-schemas/teams/v1.19/MicrosoftTeams.schema.json",
  "manifestVersion": "1.19",
  "version": "1.0.0",
  "id": "REPLACE-WITH-A-NEW-GUID",
  "developer": {
    "name": "Your Company",
    "websiteUrl": "https://example.com",
    "privacyUrl": "https://example.com/privacy",
    "termsOfUseUrl": "https://example.com/terms"
  },
  "name": { "short": "%[1]s", "full": "%[1]s" },
  "description": { "short": "%[1]s agent", "full": "%[1]s agent on Microsoft Teams" },
  "icons": { "color": "color.png", "outline": "outline.png" },
  "accentColor": "#FFFFFF",
  "bots": [{ "botId": "%[3]s", "scopes": ["personal"] }]
}
`+"```"+`

Note: `+"`id`"+` is a NEW GUID for the app itself (generate one) — it is NOT the Bot ID.
Only `+"`bots[].botId`"+` uses the Bot ID above.

- Package + icon requirements: https://learn.microsoft.com/microsoftteams/platform/concepts/build-and-test/apps-package
- Manifest schema reference: https://learn.microsoft.com/microsoftteams/platform/resources/schema/manifest-schema
- Validate your .zip before uploading: https://dev.teams.microsoft.com/tools/store-validation

## B. Upload (sideload) the app — just for yourself

You do NOT need a Teams admin to try it yourself:

1. In Teams, go to **Apps** -> **Manage your apps** -> **Upload an app**.
2. Select **Upload a custom app**, choose your .zip, then **Add**.
3. Select **Open**, then send a message to talk to your agent.

Upload a custom app guide: https://learn.microsoft.com/microsoftteams/platform/concepts/deploy-and-publish/apps-upload

If **Upload a custom app** is missing or greyed out, custom app upload is turned off for
your tenant, or you want everyone in your org to get it from the org app catalog. Both need
a Teams admin: https://learn.microsoft.com/microsoftteams/platform/concepts/build-and-test/prepare-your-o365-tenant

## C. Optional — do both from the command line

Steps A and B can be scripted. This is a convenience path for repeat runs; it needs extra
tooling and does NOT bypass the tenant custom-app-upload setting above.

Package: put the manifest.json from section A (its Bot ID is already filled in) next to your
two icons, then zip the three files at the root:

`+"```"+`sh
zip -j %[1]s-teams-app.zip manifest.json color.png outline.png          # bash
`+"```"+`
`+"```"+`powershell
Compress-Archive manifest.json,color.png,outline.png %[1]s-teams-app.zip # PowerShell
`+"```"+`

Sideload for yourself with the Microsoft 365 Agents Toolkit CLI (atk). `+"`--scope Personal`"+` is a
per-user install and needs NO Teams admin:

`+"```"+`sh
npm install -g @microsoft/m365agentstoolkit-cli          # one-time; requires Node.js
atk auth login                                           # sign in with your M365 account
atk install --file-path %[1]s-teams-app.zip --scope Personal
`+"```"+`

atk prints a TitleId and a Teams deep link you can open to launch the agent.
atk CLI reference: https://learn.microsoft.com/microsoftteams/platform/toolkit/microsoft-365-agents-toolkit-cli
`, agentName, botName, msaAppID)
}

// printTeamsNextSteps prints a short pointer to the generated setup guide. The
// full instructions live in the guide file because the azd progress UI does not
// reliably surface postdeploy stdout.
func printTeamsNextSteps(botName, msaAppID, guidePath string) {
	fmt.Println(output.WithHighLightFormat("\nTeams bot ready."))
	fmt.Printf("  Azure Bot:  %s (Microsoft Teams channel enabled)\n", botName)
	fmt.Printf("  Bot ID:     %s\n", msaAppID)
	if guidePath != "" {
		fmt.Println(output.WithGrayFormat(fmt.Sprintf(
			"  Next steps (package + sideload the Teams app): see %s", guidePath,
		)))
	} else {
		fmt.Println(output.WithGrayFormat(
			"  Next steps: package the Teams app (bots[].botId = the Bot ID above) and " +
				"upload it in Teams -> Apps -> Manage your apps -> Upload a custom app.",
		))
	}
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
	resourceGroup, err := readEnvValue(ctx, azdClient, envName, "AZURE_RESOURCE_GROUP")
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
		botName := botservice.BotName(agentName, botservice.BotScopeSalt(subscriptionID, resourceGroup))
		if err := botClient.DeleteBot(ctx, resourceGroup, botName); err != nil {
			log.Printf("postdown: failed to delete Azure Bot %q: %v", botName, err)
			continue
		}
		fmt.Printf("Deleted Azure Bot %q for agent %q\n", botName, agentName)
	}
}
