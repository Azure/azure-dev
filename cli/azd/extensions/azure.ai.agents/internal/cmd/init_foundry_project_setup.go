// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
)

// foundryProjectSetupResult holds the results of configureFoundryProject.
type foundryProjectSetupResult struct {
	Credential     azcore.TokenCredential
	FoundryProject *FoundryProjectInfo
	AuthoringMode  projectAuthoringMode
}

func validateAcrConnectionInput(acrConnection string, skipACR, createsNewProject bool) error {
	if acrConnection == "" {
		return nil
	}

	if skipACR {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--acr-connection cannot be used because the selected agent mode skips "+
				"Azure Container Registry configuration",
			"Remove --acr-connection, or use a Dockerfile-based container deployment "+
				"with an existing Foundry project.",
		)
	}

	if createsNewProject {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--acr-connection requires an existing Foundry project",
			"Pass --project-id for an existing project, or omit --acr-connection to create a new project.",
		)
	}

	return nil
}

// configureFoundryProject runs the interactive (or headless) subscription and
// Foundry project selection flow. It handles three modes:
//
//  1. --project-id provided: validate + select the specified project.
//  2. --no-prompt with missing Azure context: defer setup (print what's needed).
//  3. --no-prompt with Azure context present: configure new project without prompts.
//  4. Interactive: prompt "Use an existing Foundry project" vs "Create new".
//
// Generated-agent and unified azure.yaml adoption paths share this setup.
func configureFoundryProject(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	envName string,
	projectResourceId string,
	acrConnection string,
	noPrompt bool,
	skipACR bool,
	filterHostedRegions bool,
	preserveDeferredProjectState bool,
	allowedLocations []string,
) (*foundryProjectSetupResult, error) {
	ensureProjectLocation := func() error {
		return ensureNewProjectLocation(ctx, azdClient, azureContext, envName, noPrompt, allowedLocations)
	}
	if err := validateAcrConnectionInput(
		acrConnection,
		skipACR,
		noPrompt && projectResourceId == "",
	); err != nil {
		return nil, err
	}

	result := &foundryProjectSetupResult{
		AuthoringMode: projectAuthoringCurrent,
	}

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
			acrConnection,
			skipACR,
			filterHostedRegions,
			true, // bicepless
			allowedLocations,
		)
		if err != nil {
			return nil, err
		}
		result.FoundryProject = selectedProject
		result.AuthoringMode = projectAuthoringExisting

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
			preserveDeferredProjectState,
		); err != nil {
			return nil, err
		}
		result.AuthoringMode = projectAuthoringCurrent
	} else if preserveDeferredProjectState {
		// Preserve an adopted endpoint with complete Azure context.
		result.AuthoringMode = projectAuthoringCurrent
	} else if noPrompt {
		newCred, err := ensureSubscription(
			ctx, azdClient, azureContext, envName,
			"Select an Azure subscription to provision your agent and Foundry project resources.",
		)
		if err != nil {
			return nil, err
		}
		result.Credential = newCred
		result.AuthoringMode = projectAuthoringNew
		if err := ensureProjectLocation(); err != nil {
			return nil, err
		}
		if err := setEnvValue(ctx, azdClient, envName, "USE_EXISTING_AI_PROJECT", "false"); err != nil {
			return nil, err
		}
		if err := updatePendingProjectSignal(ctx, azdClient, envName, false); err != nil {
			log.Printf("warning: failed to update project provision signal: %v", err)
		}
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
				acrConnection,
				skipACR,
				filterHostedRegions,
				true, // bicepless
				allowedLocations,
			)
			if err != nil {
				return nil, err
			}
			result.FoundryProject = selectedProject

			if selectedProject == nil {
				result.AuthoringMode = projectAuthoringNew
				_, _ = color.New(color.Faint).Println(
					"No existing Foundry project was selected. Falling back to creating new resources.",
				)
				if err := validateAcrConnectionInput(acrConnection, false, true); err != nil {
					return nil, err
				}
				if err := ensureProjectLocation(); err != nil {
					return nil, err
				}
				if err := ensureNewFoundryProjectName(ctx, azdClient, envName); err != nil {
					return nil, err
				}
				if err := setEnvValue(ctx, azdClient, envName, "USE_EXISTING_AI_PROJECT", "false"); err != nil {
					return nil, fmt.Errorf("failed to set USE_EXISTING_AI_PROJECT: %w", err)
				}
				if err := updatePendingProjectSignal(ctx, azdClient, envName, false); err != nil {
					log.Printf("warning: failed to update project provision signal: %v", err)
				}
			} else {
				result.AuthoringMode = projectAuthoringExisting
				if err := setEnvValue(ctx, azdClient, envName, "USE_EXISTING_AI_PROJECT", "true"); err != nil {
					return nil, fmt.Errorf("failed to set USE_EXISTING_AI_PROJECT: %w", err)
				}
				if err := updatePendingProjectSignal(ctx, azdClient, envName, true); err != nil {
					log.Printf("warning: failed to update project provision signal: %v", err)
				}
			}
		default:
			result.AuthoringMode = projectAuthoringNew
			if err := validateAcrConnectionInput(acrConnection, false, true); err != nil {
				return nil, err
			}
			newCred, err := ensureSubscription(
				ctx, azdClient, azureContext, envName,
				"Select an Azure subscription to provision your agent and Foundry project resources.",
			)
			if err != nil {
				return nil, err
			}
			result.Credential = newCred
			if err := ensureProjectLocation(); err != nil {
				return nil, err
			}

			if err := ensureNewFoundryProjectName(ctx, azdClient, envName); err != nil {
				return nil, err
			}
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

// ensureNewProjectLocation validates the project and effective model locations.
// A new account hosts its model deployments in the same region, so model
// selection and account provisioning must not target different locations.
// Generated and adopted agents share this policy for new Foundry projects.
func ensureNewProjectLocation(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	envName string,
	noPrompt bool,
	allowedLocations []string,
) error {
	if len(allowedLocations) == 0 {
		return ensureLocation(ctx, azdClient, azureContext, envName)
	}
	location, err := getEnvValue(ctx, azdClient, envName, "AZURE_LOCATION")
	if err != nil {
		return err
	}
	if location != "" && !initLocationAllowed(ctx, location, allowedLocations) {
		return exterrors.Validation(
			exterrors.CodeLocationMismatch,
			fmt.Sprintf("configured AZURE_LOCATION %q is not supported for this agent setup", location),
			"set AZURE_LOCATION to a supported region before retrying init: "+strings.Join(allowedLocations, ", "),
		)
	}
	if noPrompt {
		if err := validateInitLocation(ctx, azureContext.Scope.Location, allowedLocations); err != nil {
			return err
		}
	}
	if err := validateNewProjectLocation(location, azureContext.Scope.Location); err != nil {
		return err
	}
	return ensureLocationWithAllowedLocations(ctx, azdClient, azureContext, envName, allowedLocations)
}

func validateNewProjectLocation(projectLocation, modelLocation string) error {
	if normalizeLocationName(projectLocation) == "" || normalizeLocationName(modelLocation) == "" ||
		normalizeLocationName(projectLocation) == normalizeLocationName(modelLocation) {
		return nil
	}
	return exterrors.Validation(
		exterrors.CodeLocationMismatch,
		"AZURE_LOCATION and AZURE_AI_DEPLOYMENTS_LOCATION must match when creating a new Foundry project",
		"set both environment values to the same supported region, or unset AZURE_AI_DEPLOYMENTS_LOCATION "+
			"to use AZURE_LOCATION, then retry init",
	)
}
