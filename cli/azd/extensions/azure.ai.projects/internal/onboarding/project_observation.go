// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
)

func recordProjectEndpoint(state *PreparationState, project armcognitiveservices.Project) error {
	if project.Properties == nil {
		return nil
	}
	endpoint := authString(project.Properties.Endpoints["AI Foundry API"])
	if endpoint == "" {
		return nil
	}
	id, err := ParseID(state.Foundry.Spec.ProjectID, "Microsoft.CognitiveServices/accounts/projects")
	if err != nil {
		return err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		!strings.EqualFold(parsed.Path, "/api/projects/"+id.Name) {
		return fmt.Errorf("owned Foundry project's actual API endpoint is unverified")
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if state.Foundry.ProjectEndpoint != "" && !strings.EqualFold(state.Foundry.ProjectEndpoint, endpoint) {
		return fmt.Errorf("owned Foundry project's recorded API endpoint changed")
	}
	state.Foundry.ProjectEndpoint = endpoint
	return nil
}

func (l *ownedLifecycle) verifyDeployedProject(
	ctx context.Context, plan Plan, state *PreparationState, project armcognitiveservices.Project,
) error {
	spec := state.Foundry.Spec
	if state.Foundry.ProjectDeleted {
		return fmt.Errorf("a previously deleted Foundry project was recreated; refusing adoption or deletion")
	}
	if err := validateFoundrySpec(plan, spec); err != nil {
		return err
	}
	if spec.Deployment == nil {
		if !strings.EqualFold(authString(project.ID), spec.ProjectID) ||
			authString(project.Tags[preparationOwnerTag]) != state.Owner || project.Identity == nil {
			return fmt.Errorf("legacy Foundry project ownership/identity is unverified")
		}
		return nil
	}
	if !strings.EqualFold(authString(project.ID), spec.ProjectID) ||
		!strings.EqualFold(authString(project.Location), spec.Location) ||
		project.Identity == nil || resourceValue(project.Identity.Type) != "SystemAssigned" ||
		authString(project.Identity.PrincipalID) == "" ||
		!strings.EqualFold(authString(project.Identity.TenantID), spec.Deployment.TenantID) ||
		project.Properties == nil || (resourceValue(project.Properties.ProvisioningState) != "Succeeded" &&
		!(state.CleanupStarted && state.Foundry.ProjectDeleteStarted &&
			resourceValue(project.Properties.ProvisioningState) == "Deleting")) {
		return fmt.Errorf("deployed Foundry project exact ID/location/state/system identity/tenant is unverified")
	}
	if owner, present := project.Tags[preparationOwnerTag]; present {
		if authString(owner) != state.Owner {
			return fmt.Errorf("deployed Foundry project ownership tag conflicts with the recorded environment")
		}
		return nil
	}
	// Some Project RP responses omit tags. Absence is not ownership evidence:
	// require the exact Create in the content-bound deployment graph instead.
	created := map[string]bool{}
	if _, err := l.deploymentGrantTargets(ctx, plan, state.Foundry, nil, created); err != nil {
		return fmt.Errorf("verify untagged Foundry project deployment provenance: %w", err)
	}
	if !created[strings.ToLower(spec.ProjectID)] {
		return fmt.Errorf("untagged Foundry project has no exact succeeded Create in the verified ARM deployment graph")
	}
	return l.verifyProjectParentAccount(ctx, state)
}

func (l *ownedLifecycle) verifyProjectParentAccount(ctx context.Context, state *PreparationState) error {
	spec := state.Foundry.Spec
	account, err := ParseID(spec.AccountID, "Microsoft.CognitiveServices/accounts")
	if err != nil {
		return err
	}
	client, err := armcognitiveservices.NewAccountsClient(account.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	observed, err := client.Get(ctx, account.ResourceGroupName, account.Name, nil)
	if err != nil {
		return fmt.Errorf("verify untagged Foundry project's owned parent account: %w", err)
	}
	if state.Foundry.AccountDeleted || !strings.EqualFold(authString(observed.ID), spec.AccountID) ||
		authString(observed.Tags[preparationOwnerTag]) != state.Owner ||
		!strings.EqualFold(authString(observed.Location), spec.Location) ||
		observed.Properties == nil || resourceValue(observed.Properties.ProvisioningState) != "Succeeded" {
		return fmt.Errorf("untagged Foundry project's exact owner-tagged parent account is unverified")
	}
	return nil
}
