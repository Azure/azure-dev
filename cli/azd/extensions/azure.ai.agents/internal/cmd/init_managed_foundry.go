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
// deployment to persist to azure.yaml.
//
// Location is NOT prompted separately: for an existing project it is derived
// from the project; for a new project it is prompted only at that point — the
// same architecture hosted agents rely on.
func resolvePromptHarnessTarget(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	flags *initFlags,
	env *azdext.Environment,
	settings *project.PromptAgentSettings,
) (*project.Deployment, error) {
	azureContext, err := loadAzureContext(ctx, azdClient, env.Name)
	if err != nil {
		return nil, err
	}

	// Subscription only — location is resolved per project branch below.
	cred, err := ensureSubscription(
		ctx, azdClient, azureContext, env.Name,
		"Select an Azure subscription to find your Foundry project and models.",
	)
	if err != nil {
		return nil, err
	}

	proj, err := selectPromptFoundryProject(
		ctx, azdClient, cred, azureContext, env.Name, flags.projectResourceId,
	)
	if err != nil {
		return nil, err
	}

	if proj == nil {
		// Create-new path. Prompt for a location (a new project needs one) and
		// signal Bicep to create the project + a model deployment.
		fmt.Println(output.WithGrayFormat(
			"No existing Foundry project selected. `azd up` will provision one " +
				"with the model deployment you choose next.",
		))
		if err := ensureLocation(ctx, azdClient, azureContext, env.Name); err != nil {
			return nil, err
		}
		if err := setEnvValue(ctx, azdClient, env.Name, "USE_EXISTING_AI_PROJECT", "false"); err != nil {
			return nil, err
		}
		if err := updatePendingProjectSignal(ctx, azdClient, env.Name, false); err != nil {
			log.Printf("warning: failed to update project provision signal: %v", err)
		}
		// A new project is provisioned by `azd up`; the harness workspace tuple
		// is filled from the provisioned env values at deploy time (overlay).
		return resolvePromptModelDeployment(ctx, azdClient, azureContext, env, flags)
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
			return nil, err
		}
	}

	if err := setPromptFoundryProjectEnv(ctx, azdClient, env.Name, proj); err != nil {
		return nil, err
	}
	if err := setEnvValue(ctx, azdClient, env.Name, "USE_EXISTING_AI_PROJECT", "true"); err != nil {
		return nil, err
	}
	if err := updatePendingProjectSignal(ctx, azdClient, env.Name, true); err != nil {
		log.Printf("warning: failed to update project provision signal: %v", err)
	}

	return resolvePromptModelForExistingProject(ctx, azdClient, cred, azureContext, env, flags, proj)
}

// selectPromptFoundryProject lists the Foundry projects in the subscription and
// prompts the user to pick one (or to create a new one). When projectResourceId
// is set it resolves that project directly without prompting. Returns nil when
// the user chose "Create a new Foundry project" or none were found.
//
// Unlike the hosted selectFoundryProject this does NOT filter by region or
// configure ACR/AppInsights connections, which are irrelevant to prompt agents.
func selectPromptFoundryProject(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	credential azcore.TokenCredential,
	azureContext *azdext.AzureContext,
	envName string,
	projectResourceId string,
) (*FoundryProjectInfo, error) {
	subscriptionId := azureContext.Scope.SubscriptionId
	if strings.TrimSpace(projectResourceId) != "" {
		return getFoundryProject(ctx, credential, subscriptionId, projectResourceId)
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
				d := byName[selected]
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
				}, nil
			}
		}
	}

	return resolvePromptModelDeployment(ctx, azdClient, azureContext, env, flags)
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
	deploymentName := modelDetails.ModelName
	if !flags.noPrompt {
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
