// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/inittelemetry"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// resolvePromptHarnessTarget drives the guided Foundry resolution for a prompt
// agent, mirroring the hosted agent experience: subscription -> Foundry project
// (select existing or create new) -> model deployment (version, SKU, capacity,
// name). It populates the harness workspace tuple and model endpoint on
// settings from the selected/created project, and returns the resolved model
// deployment to persist to azure.yaml along with the selected existing project
// (nil when a new one will be provisioned), which the caller needs to name and
// mark the sibling azure.ai.project service, and the credential resolved for
// the chosen subscription so later steps can read the account without
// re-authenticating.
//
// Location is NOT prompted separately: for an existing project it is derived
// from the project; for a new project it is prompted only at that point — the
// same architecture hosted agents rely on.
//
// The same walk runs under --no-prompt, resolving each step deterministically:
// the subscription and location come from the azd environment
// (AZURE_SUBSCRIPTION_ID / AZURE_LOCATION), the project from --project-id (or,
// when absent, the create-new path), and the deployment from
// --model-deployment / --model.
func resolvePromptHarnessTarget(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *initFlags,
	env *azdext.Environment,
	settings *project.PromptAgentSettings,
	harness string,
) (*project.Deployment, bool, *FoundryProjectInfo, azcore.TokenCredential, error) {
	ctx = withHarnessRegionPolicy(ctx, harness)
	defer inittelemetry.Start(ctx, inittelemetry.PromptAgentConfiguration)()

	allowedLocations, err := promptHarnessRegionsForInit(ctx, harness)
	if err != nil {
		return nil, false, nil, nil, err
	}
	azureContext, err := loadAzureContext(ctx, azdClient, env.Name)
	if err != nil {
		return nil, false, nil, nil, err
	}

	// A full project resource ID already names its subscription, so seed the
	// context from it. Without this, `--no-prompt --project-id <id>` against a
	// fresh environment would fail asking for AZURE_SUBSCRIPTION_ID even though
	// the caller just supplied it.
	if strings.TrimSpace(flags.projectResourceId) != "" {
		if proj, parseErr := extractProjectDetails(flags.projectResourceId); parseErr == nil {
			azureContext.Scope.SubscriptionId = proj.SubscriptionId
		}
	}
	// A non-interactive caller may have neither a project nor an Azure context
	// yet (a fresh environment in CI). Rather than aborting after the project
	// scaffold has already been written, mirror the hosted flow: finish the
	// scaffold, warn, and print exactly which values to set before
	// `azd provision`. The agent's model still comes from --model /
	// --model-deployment / adopted service, so the definition is complete.
	if strings.TrimSpace(flags.projectResourceId) == "" &&
		shouldDeferInitAzureContext(flags.noPrompt, azureContext) {
		if err := configureDeferredInitAzureContext(
			ctx, azdClient, env.Name, azureContext, true, false,
		); err != nil {
			return nil, false, nil, nil, err
		}
		if len(allowedLocations) > 0 {
			fmt.Println(output.WithWarningFormat(
				"The Microsoft Copilot harness requires a Foundry project in one of these regions: %s.",
				strings.Join(allowedLocations, ", "),
			))
		}
		return nil, false, nil, nil, nil
	}

	// Subscription only — location is resolved per project branch below.
	cred, err := ensureSubscription(
		ctx, azdClient, azureContext, env.Name,
		"Select an Azure subscription to find your Foundry project and models.",
	)
	if err != nil {
		return nil, false, nil, nil, err
	}

	proj, err := selectPromptFoundryProject(
		ctx, azdClient, cred, azureContext, env.Name, flags.projectResourceId, flags.noPrompt, allowedLocations,
	)
	if err != nil {
		return nil, false, nil, nil, err
	}

	if proj == nil {
		if err := validateModelDeploymentTarget(flags, nil); err != nil {
			return nil, false, nil, nil, err
		}
		// Create-new path. Prompt for a location (a new project needs one) and
		// signal Bicep to create the project + a model deployment.
		fmt.Println(output.WithGrayFormat(
			"No existing Foundry project selected. `azd up` will provision one " +
				"with the model deployment you choose next.",
		))
		err = ensureNewProjectLocation(ctx, azdClient, azureContext, env.Name, flags.noPrompt, allowedLocations)
		if err != nil {
			return nil, false, nil, nil, err
		}
		if err := setEnvValue(ctx, azdClient, env.Name, "USE_EXISTING_AI_PROJECT", "false"); err != nil {
			return nil, false, nil, nil, err
		}
		if err := updatePendingProjectSignal(ctx, azdClient, env.Name, false); err != nil {
			log.Printf("warning: failed to update project provision signal: %v", err)
		}
		// A new project is provisioned by `azd up`; its endpoint is resolved from
		// the provisioned environment at deploy time.
		deployment, provision, err := configureAgentModelDeployment(ctx, agentModelConfigOptions{
			AzdClient:    azdClient,
			Credential:   cred,
			AzureContext: azureContext,
			Env:          env,
			Flags:        flags,
			AllowSkip:    false,
		})
		if err == nil {
			err = validateInitLocation(ctx, azureContext.Scope.Location, allowedLocations)
		}
		if err == nil && len(allowedLocations) > 0 {
			location, locationErr := getEnvValue(ctx, azdClient, env.Name, "AZURE_LOCATION")
			if locationErr != nil {
				return nil, false, nil, nil, locationErr
			}
			err = validateNewProjectLocation(location, azureContext.Scope.Location)
		}
		return deployment, provision, nil, cred, err
	}

	// Existing project: populate the harness target and derive the location
	// from the project (no location prompt).
	settings.SubscriptionID = proj.SubscriptionId
	settings.ResourceGroup = proj.ResourceGroupName
	endpoint, err := url.Parse(proj.Endpoint())
	if err != nil {
		return nil, false, nil, nil, fmt.Errorf("parse resolved Foundry project endpoint: %w", err)
	}
	settings.ModelEndpoint = endpoint.Scheme + "://" + endpoint.Host
	settings.ProjectEndpoint = proj.Endpoint()

	azureContext.Scope.Location = proj.Location
	if proj.Location != "" {
		if err := setEnvValue(ctx, azdClient, env.Name, "AZURE_AI_DEPLOYMENTS_LOCATION", proj.Location); err != nil {
			return nil, false, nil, nil, err
		}
		// Also seed AZURE_LOCATION from the selected project's region. The
		// infra main.parameters.json resolves `location` from ${AZURE_LOCATION};
		// without this, `azd up` re-prompts for a region even though the project
		// (and thus the target region) is already known. Deploy the model using
		// the project's region.
		if err := setEnvValue(ctx, azdClient, env.Name, "AZURE_LOCATION", proj.Location); err != nil {
			return nil, false, nil, nil, err
		}
	}

	if err := setPromptFoundryProjectEnv(ctx, azdClient, env.Name, proj); err != nil {
		return nil, false, nil, nil, err
	}
	if err := setEnvValue(ctx, azdClient, env.Name, "USE_EXISTING_AI_PROJECT", "true"); err != nil {
		return nil, false, nil, nil, err
	}
	if err := updatePendingProjectSignal(ctx, azdClient, env.Name, true); err != nil {
		log.Printf("warning: failed to update project provision signal: %v", err)
	}

	deployment, provision, err := configureAgentModelDeployment(ctx, agentModelConfigOptions{
		AzdClient:       azdClient,
		Credential:      cred,
		AzureContext:    azureContext,
		Env:             env,
		Flags:           flags,
		SelectedProject: proj,
		AllowSkip:       false,
	})
	if err == nil {
		err = validateInitLocation(ctx, azureContext.Scope.Location, allowedLocations)
	}
	return deployment, provision, proj, cred, err
}

// selectPromptFoundryProject first asks whether to use an existing project or
// create a new one, then lists projects only for the existing-project branch.
// When projectResourceId is set it resolves that project directly without
// prompting. Returns nil when the user chose "Create a new Foundry project" or
// no existing projects were found.
//
// Unlike the hosted selectFoundryProject this only filters regions when a harness
// requires it and does not configure ACR/AppInsights connections.
//
// In non-interactive mode without --project-id there is no basis for picking
// one of the subscription's existing projects, so it returns nil to take the
// create-new path. That is the only deterministic choice: `azd up` then
// provisions a project dedicated to this agent rather than silently adopting an
// arbitrary pre-existing one.
func selectPromptFoundryProject(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	credential azcore.TokenCredential,
	azureContext *azdext.AzureContext,
	envName string,
	projectResourceId string,
	noPrompt bool,
	allowedLocations []string,
) (*FoundryProjectInfo, error) {
	subscriptionId := azureContext.Scope.SubscriptionId
	if strings.TrimSpace(projectResourceId) != "" {
		proj, err := getFoundryProject(ctx, credential, subscriptionId, projectResourceId)
		if err != nil {
			return nil, err
		}
		if err := validateInitLocation(ctx, proj.Location, allowedLocations); err != nil {
			return nil, err
		}
		return proj, nil
	}
	if noPrompt {
		return nil, nil
	}
	projectChoices := []*azdext.SelectChoice{
		{Label: "Use an existing Foundry project", Value: "existing"},
		{Label: "Create a new Foundry project", Value: "new"},
	}
	defaultIndex := int32(0)
	projectResponse, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{Options: &azdext.SelectOptions{
		Message:       "Select a Foundry project to host your agent and model",
		Choices:       projectChoices,
		SelectedIndex: &defaultIndex,
	}})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, exterrors.Cancelled("project selection was cancelled")
		}
		return nil, exterrors.FromPrompt(err, "failed to select a Foundry project configuration")
	}
	if projectChoices[*projectResponse.Value].Value == "new" {
		return nil, nil
	}

	projects, err := listFoundryProjects(ctx, credential, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("failed to list Foundry projects: %w", err)
	}
	projects = slices.DeleteFunc(projects, func(p FoundryProjectInfo) bool {
		return !initLocationAllowed(ctx, p.Location, allowedLocations)
	})
	if len(projects) == 0 {
		if len(allowedLocations) > 0 {
			fmt.Println(output.WithWarningFormat(
				"No Foundry projects were found in regions supported for the Microsoft Copilot harness. " +
					"Continue by configuring a new project.",
			))
		}
		return nil, nil
	}

	choices := make([]*azdext.SelectChoice, 0, len(projects))
	for i, p := range projects {
		label := fmt.Sprintf("%s / %s", p.AccountName, p.ProjectName)
		if p.Location != "" {
			label = fmt.Sprintf("%s (%s)", label, p.Location)
		}
		choices = append(choices, &azdext.SelectChoice{
			Label: label,
			Value: fmt.Sprintf("%d", i),
		})
	}
	resp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Select a Foundry project to host your agent and model",
			Choices: choices,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, exterrors.Cancelled("project selection was cancelled")
		}
		return nil, exterrors.Dependency(
			exterrors.CodeMissingAiProjectId,
			fmt.Sprintf("failed to select a Foundry project: %s", err),
			"pass --project-id <full resource id> to skip interactive project selection",
		)
	}

	idx := int(*resp.Value)
	selected := projects[idx]
	return getFoundryProject(ctx, credential, subscriptionId, selected.ResourceId)
}

// setPromptFoundryProjectEnv persists the core Foundry project identifiers to
// the azd environment so provisioning and deploy can resolve the project. This
// is the prompt-agent subset of configureFoundryProjectEnv (no connection
// discovery).
func setPromptFoundryProjectEnv(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	proj *FoundryProjectInfo,
) error {
	resourceId := proj.ResourceId
	if resourceId == "" {
		resourceId = fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s/projects/%s",
			proj.SubscriptionId, proj.ResourceGroupName, proj.AccountName, proj.ProjectName,
		)
	}
	values := map[string]string{
		"AZURE_AI_PROJECT_ID":      resourceId,
		"AZURE_RESOURCE_GROUP":     proj.ResourceGroupName,
		"AZURE_AI_ACCOUNT_NAME":    proj.AccountName,
		"AZURE_AI_PROJECT_NAME":    proj.ProjectName,
		"FOUNDRY_PROJECT_ENDPOINT": proj.Endpoint(),
		"AZURE_OPENAI_ENDPOINT":    proj.openAIEndpoint(),
	}
	for k, v := range values {
		if err := setEnvValue(ctx, azdClient, envName, k, v); err != nil {
			return err
		}
	}
	return nil
}
