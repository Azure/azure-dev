// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func (l *ownedLifecycle) inventoryProjectConnections(
	ctx context.Context, state *PreparationState, projectExists bool,
) ([]string, error) {
	spec := state.Foundry.Spec
	account, err := ParseID(spec.AccountID, "Microsoft.CognitiveServices/accounts")
	if err != nil {
		return nil, err
	}
	if !projectExists {
		return nil, l.inventoryAccountConnections(ctx, state, "")
	}
	project, err := ParseID(spec.ProjectID, "Microsoft.CognitiveServices/accounts/projects")
	if err != nil {
		return nil, err
	}
	registry, err := ParseID(spec.RegistryID, "Microsoft.ContainerRegistry/registries")
	if err != nil {
		return nil, err
	}
	expected := spec.ProjectID + "/connections/" + registry.Name + "-conn"
	client, err := armcognitiveservices.NewProjectConnectionsClient(account.SubscriptionID, l.credential, l.options)
	if err != nil {
		return nil, err
	}
	pager := client.NewListPager(account.ResourceGroupName, account.Name, project.Name, nil)
	var names []string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("inventory project connections before project deletion: %w", err)
		}
		for _, connection := range page.Value {
			if connection == nil || !strings.EqualFold(authString(connection.ID), expected) {
				return nil, fmt.Errorf("project contains an untracked connection; refusing cascading deletion")
			}
			if state.Foundry.ConnectionDeleted {
				return nil, fmt.Errorf("a previously deleted project connection was recreated; refusing deletion")
			}
			names = append(names, registry.Name+"-conn")
		}
	}
	if len(names) == 0 {
		return nil, l.inventoryAccountConnections(ctx, state, "")
	}
	created := map[string]bool{}
	plan := Plan{SubscriptionID: state.SubscriptionID, ResourceGroup: state.ResourceGroup,
		Location: state.Location, Owner: state.Owner}
	if _, err := l.deploymentGrantTargets(ctx, plan, state.Foundry, nil, created); err != nil {
		return nil, err
	}
	if !created[strings.ToLower(expected)] {
		return nil, fmt.Errorf("project connection has no exact succeeded Create in the verified deployment graph")
	}
	registries, err := armcontainerregistry.NewRegistriesClient(registry.SubscriptionID, l.credential, l.options)
	if err != nil {
		return nil, err
	}
	found, err := registries.Get(ctx, registry.ResourceGroupName, registry.Name, nil)
	if err != nil {
		return nil, fmt.Errorf("verify registry connection cleanup target: %w", err)
	}
	if !strings.EqualFold(authString(found.ID), spec.RegistryID) || found.Properties == nil {
		return nil, fmt.Errorf("registry connection cleanup target is unverified")
	}
	ready, err := l.registryConnectionReady(ctx, state, authString(found.Properties.LoginServer))
	if err != nil {
		return nil, err
	}
	if !ready {
		return nil, fmt.Errorf("project connection changed during cleanup inventory")
	}
	if err := l.inventoryAccountConnections(ctx, state, authString(found.Properties.LoginServer)); err != nil {
		return nil, err
	}
	return names, nil
}

// A project connection can also appear in the account list. This is not proof
// that azd owns an independent account connection: never delete that view.
// It must disappear with the project before the account can be deleted.
func (l *ownedLifecycle) inventoryAccountConnections(
	ctx context.Context, state *PreparationState, projectRegistryEndpoint string,
) error {
	spec := state.Foundry.Spec
	account, err := ParseID(spec.AccountID, "Microsoft.CognitiveServices/accounts")
	if err != nil {
		return err
	}
	registry, err := ParseID(spec.RegistryID, "Microsoft.ContainerRegistry/registries")
	if err != nil {
		return err
	}
	client, err := armcognitiveservices.NewAccountConnectionsClient(account.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	pager := client.NewListPager(account.ResourceGroupName, account.Name, nil)
	resources, err := armresources.NewClient(account.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("inventory account connections before account deletion: %w", err)
		}
		for _, connection := range page.Value {
			if projectRegistryEndpoint == "" || connection == nil ||
				!strings.EqualFold(authString(connection.ID), spec.AccountID+"/connections/"+registry.Name+"-conn") {
				return fmt.Errorf("account contains an untracked or remaining connection; refusing cascading deletion")
			}
			actual, err := resources.GetByID(ctx, authString(connection.ID), "2025-04-01-preview", nil)
			if err != nil {
				return fmt.Errorf("verify account connection before child deletion: %w", err)
			}
			if !strings.EqualFold(authString(actual.ID), authString(connection.ID)) || actual.Identity != nil {
				return fmt.Errorf("account connection identity or exact ID is unverified")
			}
			raw, err := json.Marshal(actual.Properties)
			if err != nil {
				return fmt.Errorf("encode account connection evidence: %w", err)
			}
			var properties registryConnectionProperties
			if err := json.Unmarshal(raw, &properties); err != nil {
				return fmt.Errorf("decode account connection evidence: %w", err)
			}
			if properties.Category != "ContainerRegistry" || properties.AuthType != "ManagedIdentity" ||
				!strings.EqualFold(properties.Target, projectRegistryEndpoint) ||
				!strings.EqualFold(properties.Metadata.ResourceID, spec.RegistryID) ||
				properties.UseWorkspaceManagedIdentity == nil || *properties.UseWorkspaceManagedIdentity ||
				(properties.Credentials != nil &&
					(!strings.EqualFold(authString(properties.Credentials.ClientID), state.Foundry.ProjectPrincipalID) ||
						!strings.EqualFold(authString(properties.Credentials.ResourceID), spec.RegistryID))) {
				return fmt.Errorf("account connection does not match the verified project connection; refusing deletion")
			}
			if err := l.verifyCurrentRegistryConnection(
				ctx, state, authString(connection.ID), projectRegistryEndpoint); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *ownedLifecycle) cleanupProject(
	ctx context.Context, state *PreparationState, exists bool, connections []string, progress func(string),
) error {
	id, err := ParseID(state.Foundry.Spec.ProjectID, "Microsoft.CognitiveServices/accounts/projects")
	if err != nil {
		return err
	}
	project, err := armcognitiveservices.NewProjectsClient(id.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	client, err := armcognitiveservices.NewProjectConnectionsClient(id.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	for _, name := range connections {
		if err := ctx.Err(); err != nil {
			return err
		}
		if progress != nil {
			progress(fmt.Sprintf("Deleting owned Foundry project connection %s/connections/%s...", id, name))
		}
		if _, err := client.Delete(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, name, nil); err != nil && !responseNotFound(err) {
			return fmt.Errorf("delete owned project connection: %w", err)
		}
		if _, err := client.Get(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, name, nil); !responseNotFound(err) {
			if err != nil {
				return fmt.Errorf("confirm project connection deletion: %w", err)
			}
			return fmt.Errorf("project connection deletion is not confirmed; project and dependencies retained")
		}
		state.Foundry.ConnectionDeleted = true
		if err := l.journal.Save(ctx, *state); err != nil {
			return err
		}
	}
	state.Foundry.ConnectionDeleted = true
	if exists {
		if err := ctx.Err(); err != nil {
			return err
		}
		state.Foundry.ProjectDeleteStarted = true
		if err := l.journal.Save(ctx, *state); err != nil {
			return fmt.Errorf("persist owned Project delete intent: %w", err)
		}
		if progress != nil {
			progress(fmt.Sprintf("Deleting owned Foundry project %s...", id))
		}
		poller, err := project.BeginDelete(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, nil)
		if err != nil && !responseNotFound(err) {
			return fmt.Errorf("delete owned Foundry project: %w", err)
		}
		if err == nil {
			if _, err := poller.PollUntilDone(ctx, nil); err != nil {
				return fmt.Errorf("wait for owned Foundry project deletion: %w", err)
			}
		}
	}
	if _, err := project.Get(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, nil); !responseNotFound(err) {
		if err != nil {
			return fmt.Errorf("confirm Foundry project deletion: %w", err)
		}
		return fmt.Errorf("Foundry project deletion is not confirmed; account and dependencies retained")
	}
	state.Foundry.ProjectDeleted = true
	return l.journal.Save(ctx, *state)
}
