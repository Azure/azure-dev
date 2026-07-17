// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"text/template"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/botservice"
	"azureaiagent/internal/pkg/paths"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// ensureActivityBot runs during postdeploy for an agent that speaks the Activity
// protocol; it is a no-op for any other agent, so non-activity deployments are
// completely unaffected.
//
// It provisions ONLY the Azure resource plane: create the Azure Bot, bind it to
// the agent instance identity, enable the bot's Microsoft Teams *channel*, and
// point the bot's messaging endpoint at the agent. That "Teams channel" is an
// Azure Bot Service resource toggle — NOT a Teams app. Packaging and sideloading
// the Teams *app* live on the M365/Graph plane, stay out of azd, and are left to
// the user; postdeploy writes TEAMS_APP_SETUP.md with those manual steps.
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

	// Write a runnable pack+sideload script and a persistent, generic setup guide
	// next to the agent code (the azd progress UI swallows postdeploy stdout, so a
	// file is the reliable way to hand the user the manual M365 steps), then print
	// a short pointer to them. Generate the scripts first so the guide's fast-path
	// section only advertises a script azd actually wrote (a pre-existing
	// user-owned file with that name is preserved, not overwritten).
	scriptPaths := writeTeamsSideloadScripts(proj, svc, agentName, botName, msaAppID)
	scriptsGenerated := len(scriptPaths) > 0
	guidePath := writeTeamsSetupGuide(proj, svc, agentName, botName, msaAppID, scriptsGenerated)
	printTeamsNextSteps(botName, msaAppID, guidePath, preferredSideloadScript(scriptPaths), scriptsGenerated)
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
	proj *azdext.ProjectConfig, svc *azdext.ServiceConfig, agentName, botName, msaAppID string, scriptsGenerated bool,
) string {
	guidePath, err := paths.JoinAllowRoot(proj.GetPath(), svc.GetRelativePath(), teamsSetupGuideFile)
	if err != nil {
		log.Printf("postdeploy: skipping Teams setup guide: %v", err)
		return ""
	}
	content := teamsSetupGuideContent(agentName, botName, msaAppID, scriptsGenerated)
	if err := os.WriteFile(guidePath, []byte(content), 0o600); err != nil {
		log.Printf("postdeploy: failed to write Teams setup guide %q: %v", guidePath, err)
		return ""
	}
	return guidePath
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
func teamsSetupGuideContent(agentName, botName, msaAppID string, scriptsGenerated bool) string {
	var buf bytes.Buffer
	// Inputs are azd-controlled resource names and the template is compile-time
	// embedded, so execution cannot realistically fail.
	_ = teamsSetupGuideTmpl.Execute(&buf, struct {
		AgentName        string
		BotName          string
		MsaAppID         string
		ScriptsGenerated bool
	}{AgentName: agentName, BotName: botName, MsaAppID: msaAppID, ScriptsGenerated: scriptsGenerated})
	return buf.String()
}

// printTeamsNextSteps prints a short pointer to the generated setup guide and
// the runnable pack+sideload script. The full instructions live in the guide
// file because the azd progress UI does not reliably surface postdeploy stdout.
func printTeamsNextSteps(botName, msaAppID, guidePath, scriptPath string, scriptsGenerated bool) {
	fmt.Println(output.WithHighLightFormat("\nTeams bot ready."))
	fmt.Printf("  Azure Bot:  %s (Microsoft Teams channel enabled)\n", botName)
	fmt.Printf("  Bot ID:     %s\n", msaAppID)
	if scriptPath != "" {
		fmt.Println(output.WithGrayFormat(fmt.Sprintf(
			"  Fast path (package + sideload the Teams app for you): run %s", sideloadRunCommand(scriptPath),
		)))
	} else if !scriptsGenerated {
		fmt.Println(output.WithGrayFormat(
			"  Note: the pack-and-sideload script was not generated (a file with that name may " +
				"already exist in the service folder); see the guide for the manual steps.",
		))
	}
	if guidePath != "" {
		fmt.Println(output.WithGrayFormat(fmt.Sprintf(
			"  Manual / UI steps and prerequisites: see %s", guidePath,
		)))
	} else if scriptPath == "" {
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
