// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"strings"

	"azureaiagent/internal/exterrors"
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
// mark the sibling azure.ai.project service.
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
) (*project.Deployment, *FoundryProjectInfo, error) {
	azureContext, err := loadAzureContext(ctx, azdClient, env.Name)
	if err != nil {
		return nil, nil, err
	}

	// A full project resource ID already names its subscription, so seed the
	// context from it. Without this, `--no-prompt --project-id <id>` against a
	// fresh environment would fail asking for AZURE_SUBSCRIPTION_ID even though
	// the caller just supplied it.
	if strings.TrimSpace(flags.projectResourceId) != "" && azureContext.Scope.SubscriptionId == "" {
		if proj, parseErr := extractProjectDetails(flags.projectResourceId); parseErr == nil {
			azureContext.Scope.SubscriptionId = proj.SubscriptionId
		}
	}

	// A non-interactive caller may have neither a project nor an Azure context
	// yet (a fresh environment in CI). Rather than aborting after the project
	// scaffold has already been written, mirror the hosted flow: finish the
	// scaffold, warn, and print exactly which values to set before
	// `azd provision`. The agent's model still comes from --model /
	// --model-deployment / the manifest, so agent.yaml is complete.
	if strings.TrimSpace(flags.projectResourceId) == "" &&
		shouldDeferInitAzureContext(flags.noPrompt, azureContext) {
		if err := configureDeferredInitAzureContext(ctx, azdClient, env.Name, azureContext, true); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	}

	// Subscription only — location is resolved per project branch below.
	cred, err := ensureSubscription(
		ctx, azdClient, azureContext, env.Name,
		"Select an Azure subscription to find your Foundry project and models.",
	)
	if err != nil {
		return nil, nil, err
	}

	proj, err := selectPromptFoundryProject(
		ctx, azdClient, cred, azureContext, env.Name, flags.projectResourceId, flags.noPrompt,
	)
	if err != nil {
		return nil, nil, err
	}

	if proj == nil {
		// Create-new path. Prompt for a location (a new project needs one) and
		// signal Bicep to create the project + a model deployment.
		fmt.Println(output.WithGrayFormat(
			"No existing Foundry project selected. `azd up` will provision one " +
				"with the model deployment you choose next.",
		))
		if err := ensureLocation(ctx, azdClient, azureContext, env.Name); err != nil {
			return nil, nil, err
		}
		if err := setEnvValue(ctx, azdClient, env.Name, "USE_EXISTING_AI_PROJECT", "false"); err != nil {
			return nil, nil, err
		}
		if err := updatePendingProjectSignal(ctx, azdClient, env.Name, false); err != nil {
			log.Printf("warning: failed to update project provision signal: %v", err)
		}
		// A new project is provisioned by `azd up`; the harness workspace tuple
		// is filled from the provisioned env values at deploy time (overlay).
		deployment, err := resolvePromptModelDeployment(ctx, azdClient, azureContext, env, flags)
		return deployment, nil, err
	}

	// Existing project: populate the harness target and derive the location
	// from the project (no location prompt).
	settings.SubscriptionID = proj.SubscriptionId
	settings.ResourceGroup = proj.ResourceGroupName
	settings.Workspace = proj.ProjectName
	settings.ModelEndpoint = fmt.Sprintf("https://%s.services.ai.azure.com", proj.AccountName)
	// Record the Foundry project data-plane endpoint so all managed agent
	// operations route to https://<account>.services.ai.azure.com/api/projects/<project>/agents.
	settings.ProjectEndpoint = fmt.Sprintf(
		"https://%s.services.ai.azure.com/api/projects/%s", proj.AccountName, proj.ProjectName,
	)
	settings.APIVersion = project.ProjectEndpointAPIVersion

	azureContext.Scope.Location = proj.Location
	if proj.Location != "" {
		if err := setEnvValue(ctx, azdClient, env.Name, "AZURE_AI_DEPLOYMENTS_LOCATION", proj.Location); err != nil {
			return nil, nil, err
		}
		// Also seed AZURE_LOCATION from the selected project's region. The
		// infra main.parameters.json resolves `location` from ${AZURE_LOCATION};
		// without this, `azd up` re-prompts for a region even though the project
		// (and thus the target region) is already known. Deploy the model using
		// the project's region.
		if err := setEnvValue(ctx, azdClient, env.Name, "AZURE_LOCATION", proj.Location); err != nil {
			return nil, nil, err
		}
	}

	if err := setPromptFoundryProjectEnv(ctx, azdClient, env.Name, proj); err != nil {
		return nil, nil, err
	}
	if err := setEnvValue(ctx, azdClient, env.Name, "USE_EXISTING_AI_PROJECT", "true"); err != nil {
		return nil, nil, err
	}
	if err := updatePendingProjectSignal(ctx, azdClient, env.Name, true); err != nil {
		log.Printf("warning: failed to update project provision signal: %v", err)
	}

	deployment, err := resolvePromptModelForExistingProject(ctx, azdClient, cred, azureContext, env, flags, proj)
	return deployment, proj, err
}

// selectPromptFoundryProject lists the Foundry projects in the subscription and
// prompts the user to pick one (or to create a new one). When projectResourceId
// is set it resolves that project directly without prompting. Returns nil when
// the user chose "Create a new Foundry project" or none were found.
//
// Unlike the hosted selectFoundryProject this does NOT filter by region or
// configure ACR/AppInsights connections, which are irrelevant to prompt agents.
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
) (*FoundryProjectInfo, error) {
	subscriptionId := azureContext.Scope.SubscriptionId
	if strings.TrimSpace(projectResourceId) != "" {
		return getFoundryProject(ctx, credential, subscriptionId, projectResourceId)
	}
	if noPrompt {
		return nil, nil
	}

	projects, err := listFoundryProjects(ctx, credential, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("failed to list Foundry projects: %w", err)
	}
	if len(projects) == 0 {
		return nil, nil
	}

	choices := make([]*azdext.SelectChoice, 0, len(projects)+1)
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
	const createNewValue = "__create_new__"
	choices = append(choices, &azdext.SelectChoice{
		Label: "Create a new Foundry project (provisioned by `azd up`)",
		Value: createNewValue,
	})

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
	if idx < 0 || idx >= len(projects) {
		// "Create a new Foundry project"
		return nil, nil
	}
	selected := projects[idx]
	return &selected, nil
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
	foundryEndpoint := fmt.Sprintf(
		"https://%s.services.ai.azure.com/api/projects/%s", proj.AccountName, proj.ProjectName,
	)
	values := map[string]string{
		"AZURE_AI_PROJECT_ID":      resourceId,
		"AZURE_RESOURCE_GROUP":     proj.ResourceGroupName,
		"AZURE_AI_ACCOUNT_NAME":    proj.AccountName,
		"AZURE_AI_PROJECT_NAME":    proj.ProjectName,
		"FOUNDRY_PROJECT_ENDPOINT": foundryEndpoint,
	}
	for k, v := range values {
		if err := setEnvValue(ctx, azdClient, envName, k, v); err != nil {
			return err
		}
	}
	return nil
}

// resolvePromptModelForExistingProject resolves a model deployment for a prompt
// agent on an already-selected Foundry project. It offers the project's
// existing deployments first (reuse a live deployment), plus a "deploy a new
// model" option that runs the full catalog -> version -> SKU -> capacity flow.
func resolvePromptModelForExistingProject(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	credential azcore.TokenCredential,
	azureContext *azdext.AzureContext,
	env *azdext.Environment,
	flags *initFlags,
	proj *FoundryProjectInfo,
) (*project.Deployment, error) {
	// --model-deployment names an existing deployment in this project to reuse
	// verbatim, which is the non-interactive equivalent of picking one from the
	// list below. It wins over --model so `--model-deployment x --model y` does
	// not silently provision a second deployment.
	if requested := strings.TrimSpace(flags.modelDeployment); requested != "" {
		return findExistingPromptDeployment(ctx, credential, proj, requested)
	}

	// --model short-circuits to the new-deployment configuration so the named
	// model is resolved (version/SKU/capacity) and provisioned.
	if strings.TrimSpace(flags.model) == "" {
		deployments, err := listProjectDeployments(
			ctx, credential, proj.SubscriptionId, proj.ResourceGroupName, proj.AccountName,
		)
		if err != nil {
			fmt.Println(output.WithWarningFormat(
				"Could not list existing model deployments: %s. Choosing from the catalog instead.\n", err,
			))
		} else if len(deployments) > 0 && !flags.noPrompt {
			const newModelValue = "__new_model__"
			choices := make([]*azdext.SelectChoice, 0, len(deployments)+1)
			byName := make(map[string]*FoundryDeploymentInfo, len(deployments))
			for i := range deployments {
				d := &deployments[i]
				byName[d.Name] = d
				label := d.Name
				if d.ModelName != "" {
					label = fmt.Sprintf("%s (%s", d.Name, d.ModelName)
					if d.Version != "" {
						label += " " + d.Version
					}
					label += ")"
				}
				choices = append(choices, &azdext.SelectChoice{Label: label, Value: d.Name})
			}
			choices = append(choices, &azdext.SelectChoice{
				Label: "Deploy a new model from the catalog",
				Value: newModelValue,
			})

			defaultIndex := int32(0)
			resp, selErr := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
				Options: &azdext.SelectOptions{
					Message:       "Select the model deployment your agent will call",
					Choices:       choices,
					SelectedIndex: &defaultIndex,
				},
			})
			if selErr != nil {
				if exterrors.IsCancellation(selErr) {
					return nil, exterrors.Cancelled("model selection was cancelled")
				}
				return nil, fmt.Errorf("prompting for model deployment: %w", selErr)
			}
			if selected := choices[*resp.Value].Value; selected != newModelValue {
				return promptDeploymentFromFoundry(byName[selected]), nil
			}
		}
	}

	return resolvePromptModelDeployment(ctx, azdClient, azureContext, env, flags)
}

// findExistingPromptDeployment resolves a named model deployment in the given
// Foundry project so `--model-deployment` can reference a live deployment
// without provisioning a new one. A missing deployment is an error rather than
// a fallback to the catalog: silently deploying a different model than the one
// the caller named would surprise them and cost them quota.
func findExistingPromptDeployment(
	ctx context.Context,
	credential azcore.TokenCredential,
	proj *FoundryProjectInfo,
	deploymentName string,
) (*project.Deployment, error) {
	deployments, err := listProjectDeployments(
		ctx, credential, proj.SubscriptionId, proj.ResourceGroupName, proj.AccountName,
	)
	if err != nil {
		return nil, fmt.Errorf("listing model deployments for Foundry project %q: %w", proj.ProjectName, err)
	}

	available := make([]string, 0, len(deployments))
	for i := range deployments {
		if strings.EqualFold(deployments[i].Name, deploymentName) {
			return promptDeploymentFromFoundry(&deployments[i]), nil
		}
		available = append(available, deployments[i].Name)
	}

	suggestion := "create the deployment first, or pass --model <model-name> to have `azd up` deploy it"
	if len(available) > 0 {
		suggestion = fmt.Sprintf("deployments in this project: %s. %s", strings.Join(available, ", "), suggestion)
	}
	return nil, exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf(
			"model deployment %q was not found in Foundry project %q", deploymentName, proj.ProjectName,
		),
		suggestion,
	)
}

// promptDeploymentFromFoundry converts a discovered Foundry model deployment
// into the azure.yaml deployment entry recorded on the prompt agent service.
func promptDeploymentFromFoundry(d *FoundryDeploymentInfo) *project.Deployment {
	return &project.Deployment{
		Name: d.Name,
		Model: project.DeploymentModel{
			Name:    d.ModelName,
			Format:  d.ModelFormat,
			Version: d.Version,
		},
		Sku: project.DeploymentSku{
			Name:     d.SkuName,
			Capacity: d.SkuCapacity,
		},
	}
}

// resolvePromptModelDeployment runs the full "deploy a new model" flow — model
// selection from the catalog, then version / SKU / capacity via the shared
// modelSelector, then a deployment-name prompt — and returns the resulting
// deployment. It reuses the exact hosted helpers so prompt agents get the same
// deployment configuration UX.
func resolvePromptModelDeployment(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	env *azdext.Environment,
	flags *initFlags,
) (*project.Deployment, error) {
	selector := &modelSelector{
		azdClient:    azdClient,
		azureContext: azureContext,
		environment:  env,
		flags:        flags,
	}

	defaultModel := strings.TrimSpace(flags.model)
	if defaultModel == "" {
		defaultModel = "gpt-4.1-mini"
	}

	// getModelDetails handles model confirm/change, location-availability and
	// quota retries, and the version / SKU / capacity selection (via
	// PromptAiDeployment). allowSkip=false: a prompt agent must have a model.
	modelDetails, err := selector.getModelDetails(ctx, defaultModel, false)
	if err != nil {
		return nil, err
	}

	// Deployment name (defaults to the model name), matching hosted.
	// --model-deployment names the deployment explicitly, which is how a
	// non-interactive caller controls it on the create-new-project path where
	// there is no existing deployment to look up.
	deploymentName := modelDetails.ModelName
	if requested := strings.TrimSpace(flags.modelDeployment); requested != "" {
		deploymentName = requested
	} else if !flags.noPrompt {
		resp, promptErr := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message: fmt.Sprintf(
					"Enter model deployment name for model '%s' (defaults to model name)",
					modelDetails.ModelName,
				),
				IgnoreHintKeys: true,
				DefaultValue:   modelDetails.ModelName,
			},
		})
		if promptErr != nil {
			if exterrors.IsCancellation(promptErr) {
				return nil, exterrors.Cancelled("deployment name prompt was cancelled")
			}
			return nil, fmt.Errorf("prompting for deployment name: %w", promptErr)
		}
		if v := strings.TrimSpace(resp.Value); v != "" {
			deploymentName = v
		}
	}

	deployment := &project.Deployment{
		Name: deploymentName,
		Model: project.DeploymentModel{
			Name:    modelDetails.ModelName,
			Format:  modelDetails.Format,
			Version: modelDetails.Version,
		},
		Sku: project.DeploymentSku{
			Name:     modelDetails.Sku.Name,
			Capacity: int(modelDetails.Capacity),
		},
	}
	return deployment, nil
}
