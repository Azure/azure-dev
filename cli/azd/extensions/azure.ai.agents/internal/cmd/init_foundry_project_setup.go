// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"

	"azureaiagent/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
)

// foundryProjectSetupResult holds the results of configureFoundryProject.
type foundryProjectSetupResult struct {
	Credential     azcore.TokenCredential
	FoundryProject *FoundryProjectInfo
}

// configureFoundryProject runs the interactive (or headless) subscription and
// Foundry project selection flow. It handles three modes:
//
//  1. --project-id provided: validate + select the specified project.
//  2. --no-prompt with missing Azure context: defer setup (print what's needed).
//  3. --no-prompt with Azure context present: configure new project without prompts.
//  4. Interactive: prompt "Use an existing Foundry project" vs "Create new".
//
// This is the shared core extracted from configureModelChoice's
// !hasModelResources branch so both the agent-manifest and unified azure.yaml
// adoption paths can reuse it.
func configureFoundryProject(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	envName string,
	projectResourceId string,
	noPrompt bool,
	skipACR bool,
) (*foundryProjectSetupResult, error) {
	result := &foundryProjectSetupResult{}

	// When --project-id is provided, validate the ARM format and extract the
	// subscription ID so ensureSubscription can skip the prompt.
	if projectResourceId != "" {
		projectDetails, err := extractProjectDetails(projectResourceId)
		if err != nil {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidProjectResourceId,
				fmt.Sprintf("invalid --project-id value: %s", err),
				"Provide a valid Foundry project resource ID in the format:\n"+
					"/subscriptions/<SUBSCRIPTION_ID>/resourceGroups/<RESOURCE_GROUP>/providers/"+
					"Microsoft.CognitiveServices/accounts/<ACCOUNT_NAME>/projects/<PROJECT_NAME>",
			)
		}
		azureContext.Scope.SubscriptionId = projectDetails.SubscriptionId

		newCred, err := ensureSubscription(
			ctx, azdClient, azureContext, envName,
			"Select an Azure subscription to provision your agent and Foundry project resources.",
		)
		if err != nil {
			return nil, err
		}
		result.Credential = newCred

		selectedProject, err := selectFoundryProject(
			ctx, azdClient, newCred, azureContext, envName,
			azureContext.Scope.SubscriptionId, projectResourceId,
			skipACR,
			true, // bicepless
		)
		if err != nil {
			return nil, err
		}
		result.FoundryProject = selectedProject

		if selectedProject == nil {
			return nil, fmt.Errorf(
				"specified foundry project was not found or is not eligible for the current configuration: %s",
				projectResourceId,
			)
		}

		if err := setEnvValue(ctx, azdClient, envName, "USE_EXISTING_AI_PROJECT", "true"); err != nil {
			return nil, fmt.Errorf("failed to set USE_EXISTING_AI_PROJECT: %w", err)
		}
		if err := updatePendingProjectSignal(ctx, azdClient, envName, true); err != nil {
			log.Printf("warning: failed to update project provision signal: %v", err)
		}
	} else if shouldDeferInitAzureContext(noPrompt, azureContext) {
		// Headless init with missing Azure values: defer without blocking.
		if err := configureDeferredInitAzureContext(
			ctx, azdClient, envName, azureContext, false,
		); err != nil {
			return nil, err
		}
	} else if noPrompt {
		newCred, err := configureNewProjectForNoPrompt(
			ctx, azdClient, envName, azureContext,
			"Select an Azure subscription to provision your agent and Foundry project resources.",
		)
		if err != nil {
			return nil, err
		}
		result.Credential = newCred
	} else {
		// Interactive: prompt user to pick an existing Foundry project or create new resources
		projectChoices := []*azdext.SelectChoice{
			{Label: "Use an existing Foundry project", Value: "existing"},
			{Label: "Create a new Foundry project", Value: "new"},
		}

		defaultIdx := int32(0)
		projectResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       "Select a Foundry project to host your agent and any models or tools it uses.",
				Choices:       projectChoices,
				SelectedIndex: &defaultIdx,
			},
		})
		if err != nil {
			if exterrors.IsCancellation(err) {
				return nil, exterrors.Cancelled("project selection was cancelled")
			}
			return nil, exterrors.FromPrompt(err, "failed to prompt for Foundry project configuration choice")
		}

		switch projectChoices[*projectResp.Value].Value {
		case "existing":
			newCred, err := ensureSubscription(
				ctx, azdClient, azureContext, envName,
				"Select an Azure subscription to find existing Foundry projects.",
			)
			if err != nil {
				return nil, err
			}
			result.Credential = newCred

			selectedProject, err := selectFoundryProject(
				ctx, azdClient, newCred, azureContext, envName,
				azureContext.Scope.SubscriptionId, "",
				skipACR,
				true, // bicepless
			)
			if err != nil {
				return nil, err
			}
			result.FoundryProject = selectedProject

			if selectedProject == nil {
				_, _ = color.New(color.Faint).Println(
					"No existing Foundry project was selected. Falling back to creating new resources.",
				)
				if err := setEnvValue(ctx, azdClient, envName, "USE_EXISTING_AI_PROJECT", "false"); err != nil {
					return nil, fmt.Errorf("failed to set USE_EXISTING_AI_PROJECT: %w", err)
				}
				if err := updatePendingProjectSignal(ctx, azdClient, envName, false); err != nil {
					log.Printf("warning: failed to update project provision signal: %v", err)
				}
				if err := ensureLocation(ctx, azdClient, azureContext, envName); err != nil {
					return nil, err
				}
			} else {
				if err := setEnvValue(ctx, azdClient, envName, "USE_EXISTING_AI_PROJECT", "true"); err != nil {
					return nil, fmt.Errorf("failed to set USE_EXISTING_AI_PROJECT: %w", err)
				}
				if err := updatePendingProjectSignal(ctx, azdClient, envName, true); err != nil {
					log.Printf("warning: failed to update project provision signal: %v", err)
				}
			}
		default:
			newCred, err := ensureSubscriptionAndLocation(
				ctx, azdClient, azureContext, envName,
				"Select an Azure subscription to provision your agent and Foundry project resources.",
			)
			if err != nil {
				return nil, err
			}
			result.Credential = newCred

			if err := setEnvValue(ctx, azdClient, envName, "USE_EXISTING_AI_PROJECT", "false"); err != nil {
				return nil, fmt.Errorf("failed to set USE_EXISTING_AI_PROJECT: %w", err)
			}
			if err := updatePendingProjectSignal(ctx, azdClient, envName, false); err != nil {
				log.Printf("warning: failed to update project provision signal: %v", err)
			}
		}
	}

	// Persist the ACR-skip signal so Bicep knows whether to create a container registry.
	if err := setACREnvVar(ctx, azdClient, envName, skipACR); err != nil {
		return nil, err
	}

	return result, nil
}
