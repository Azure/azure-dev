// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
)

func (l *ownedLifecycle) cleanupFoundry(
	ctx context.Context, plan Plan, state *PreparationState, purge bool, progress func(string),
) error {
	target := PlannedFoundryAccount(plan)
	if state.Foundry != nil {
		if len(state.Foundry.Attempts) == 0 {
			return fmt.Errorf("Foundry lifecycle has no durable ARM attempt evidence")
		}
		if err := l.verifyAttempts(ctx, plan, state.Foundry); err != nil {
			return err
		}
		if err := l.journal.Save(ctx, *state); err != nil {
			return err
		}
		target.ID = state.Foundry.Spec.AccountID
	}
	id, err := ParseID(target.ID, "Microsoft.CognitiveServices/accounts")
	if err != nil {
		return err
	}
	client, err := armcognitiveservices.NewAccountsClient(id.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	observed, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err == nil {
		if state.Foundry == nil || state.Foundry.AccountDeleted ||
			!strings.EqualFold(authString(observed.ID), target.ID) ||
			authString(observed.Tags[preparationOwnerTag]) != state.Owner ||
			!strings.EqualFold(authString(observed.Location), state.Foundry.Spec.Location) {
			return fmt.Errorf("Foundry account ownership/location is unknown or changed")
		}
		if err := l.cleanupModels(ctx, plan, state, id.ResourceGroupName, id.Name); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if progress != nil {
			progress(fmt.Sprintf("Deleting owned Foundry account %s...", target.ID))
		}
		poller, err := client.BeginDelete(ctx, id.ResourceGroupName, id.Name, nil)
		if err != nil && !responseNotFound(err) {
			return err
		}
		if err == nil {
			if _, err := poller.PollUntilDone(ctx, nil); err != nil {
				return err
			}
		}
		if _, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil); !responseNotFound(err) {
			if err != nil {
				return fmt.Errorf("verify Foundry account deletion: %w", err)
			}
			return fmt.Errorf("Foundry account deletion is not yet confirmed")
		}
	} else if !responseNotFound(err) {
		return fmt.Errorf("verify Foundry account before dependency cleanup: %w", err)
	}
	if state.Foundry == nil {
		return nil
	}
	state.Foundry.AccountDeleted = true
	if err := l.journal.Save(ctx, *state); err != nil {
		return err
	}
	if !purge {
		if progress != nil && !state.Foundry.Purged {
			progress("Foundry account is deleted; soft deletion can reserve its name for 48 hours; no purge was requested")
		}
		return nil
	}
	if state.Foundry.Purged {
		return nil
	}
	deleted, err := armcognitiveservices.NewDeletedAccountsClient(id.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	softDeleted, err := deleted.Get(ctx, state.Foundry.Spec.Location, id.ResourceGroupName, id.Name, nil)
	if !responseNotFound(err) {
		if err != nil {
			return fmt.Errorf("read soft-deleted owned Foundry account: %w", err)
		}
		if authString(softDeleted.Tags[preparationOwnerTag]) != state.Owner ||
			!strings.EqualFold(authString(softDeleted.Location), state.Foundry.Spec.Location) {
			return fmt.Errorf("soft-deleted account ownership/location changed; refusing irreversible purge")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if progress != nil {
			progress("Permanently purging the explicitly requested owned Foundry account...")
		}
		poller, err := deleted.BeginPurge(ctx, state.Foundry.Spec.Location, id.ResourceGroupName, id.Name, nil)
		if err != nil && !responseNotFound(err) {
			return err
		}
		if err == nil {
			if _, err := poller.PollUntilDone(ctx, nil); err != nil {
				return err
			}
		}
		if _, err := deleted.Get(ctx, state.Foundry.Spec.Location, id.ResourceGroupName, id.Name, nil); !responseNotFound(err) {
			if err != nil {
				return err
			}
			return fmt.Errorf("Foundry purge is not confirmed; retain the journal")
		}
	}
	state.Foundry.Purged = true
	return l.journal.Save(ctx, *state)
}

func (l *ownedLifecycle) cleanupModels(ctx context.Context, plan Plan, state *PreparationState, group, account string) error {
	spec := state.Foundry.Spec
	accountID, err := ParseID(spec.AccountID, "Microsoft.CognitiveServices/accounts")
	if err != nil {
		return err
	}
	projects, err := armcognitiveservices.NewProjectsClient(accountID.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	pager := projects.NewListPager(group, account, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("inventory Foundry projects before account deletion: %w", err)
		}
		for _, project := range page.Value {
			if project == nil || !strings.EqualFold(authString(project.ID), spec.ProjectID) ||
				project.Identity == nil ||
				(state.Foundry.ProjectPrincipalID != "" &&
					!strings.EqualFold(authString(project.Identity.PrincipalID), state.Foundry.ProjectPrincipalID)) {
				return fmt.Errorf("account contains an untracked/changed project; refusing cascading deletion")
			}
			if err := l.verifyDeployedProject(ctx, plan, state, *project); err != nil {
				return fmt.Errorf("account project ownership is unverified; refusing cascading deletion: %w", err)
			}
		}
	}
	deployments, err := armcognitiveservices.NewDeploymentsClient(accountID.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	models := deployments.NewListPager(group, account, nil)
	var owned []string
	for models.More() {
		page, err := models.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("inventory model deployments before account deletion: %w", err)
		}
		for _, model := range page.Value {
			if model == nil {
				return fmt.Errorf("model deployment inventory is incomplete")
			}
			id, err := ParseID(authString(model.ID), "Microsoft.CognitiveServices/accounts/deployments")
			if err != nil || !strings.EqualFold(id.Parent.String(), spec.AccountID) ||
				!slices.ContainsFunc(spec.ModelNames, func(name string) bool { return strings.EqualFold(name, id.Name) }) {
				return fmt.Errorf("account contains an untracked model deployment; refusing cascading deletion")
			}
			if state.Foundry.ModelsDeleted[strings.ToLower(id.Name)] {
				return fmt.Errorf("a previously deleted model deployment was recreated; refusing cascading deletion")
			}
			owned = append(owned, id.Name)
		}
	}
	for _, name := range owned {
		if err := ctx.Err(); err != nil {
			return err
		}
		poller, err := deployments.BeginDelete(ctx, group, account, name, nil)
		if err != nil && !responseNotFound(err) {
			return err
		}
		if err == nil {
			if _, err := poller.PollUntilDone(ctx, nil); err != nil {
				return err
			}
		}
		if _, err := deployments.Get(ctx, group, account, name, nil); !responseNotFound(err) {
			if err != nil {
				return err
			}
			return fmt.Errorf("model deployment deletion is not confirmed; account retained")
		}
		if state.Foundry.ModelsDeleted == nil {
			state.Foundry.ModelsDeleted = map[string]bool{}
		}
		state.Foundry.ModelsDeleted[strings.ToLower(name)] = true
		if err := l.journal.Save(ctx, *state); err != nil {
			return err
		}
	}
	return nil
}

func (l *ownedLifecycle) verifyServiceFICsGone(ctx context.Context, state PreparationState) error {
	for _, step := range []string{"R04", "R05"} {
		record, exists := state.Resources[step]
		if !exists || record.Deleted {
			continue
		}
		id, err := ParseID(record.ID, "Microsoft.ManagedIdentity/userAssignedIdentities")
		if err != nil {
			return err
		}
		identities, err := armmsi.NewUserAssignedIdentitiesClient(id.SubscriptionID, l.credential, l.options)
		if err != nil {
			return err
		}
		identity, err := identities.Get(ctx, id.ResourceGroupName, id.Name, nil)
		if responseNotFound(err) {
			continue
		} else if err != nil {
			return err
		}
		if err := verifyOwnedIdentity(state, step, identity.Identity); err != nil {
			return err
		}
		client, err := armmsi.NewFederatedIdentityCredentialsClient(id.SubscriptionID, l.credential, l.options)
		if err != nil {
			return err
		}
		pager := client.NewListPager(id.ResourceGroupName, id.Name, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return err
			}
			if len(page.Value) != 0 {
				return fmt.Errorf("service-owned federated credentials remain; azd does not remove them")
			}
		}
	}
	return nil
}
