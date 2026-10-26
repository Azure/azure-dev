// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

type registryConnectionProperties struct {
	Category                    string                                          `json:"category"`
	Target                      string                                          `json:"target"`
	AuthType                    string                                          `json:"authType"`
	UseWorkspaceManagedIdentity *bool                                           `json:"useWorkspaceManagedIdentity"`
	Credentials                 *armcognitiveservices.ConnectionManagedIdentity `json:"credentials"`
	Metadata                    struct {
		ResourceID string `json:"ResourceId"`
	} `json:"metadata"`
}

// Connection GET can mask ManagedIdentity credentials. Require both deployment
// provenance and the current identity returned by the read-only listSecrets action.
func (l *ownedLifecycle) verifyMaskedRegistryConnection(
	ctx context.Context, state *PreparationState, connectionID, endpoint string,
) error {
	plan := Plan{SubscriptionID: state.SubscriptionID, ResourceGroup: state.ResourceGroup,
		Location: state.Location, Owner: state.Owner}
	if err := l.verifyAttempts(ctx, plan, state.Foundry); err != nil {
		return err
	}
	succeeded, created := map[string]bool{}, map[string]bool{}
	targets, err := l.deploymentGrantTargets(ctx, plan, state.Foundry, succeeded, created)
	if err != nil {
		return fmt.Errorf("verify masked registry connection deployment provenance: %w", err)
	}
	if !created[strings.ToLower(connectionID)] {
		return fmt.Errorf("masked registry connection has no exact succeeded Create in the verified ARM deployment graph")
	}
	spec := state.Foundry.Spec
	account, err := ParseID(spec.AccountID, "Microsoft.CognitiveServices/accounts")
	if err != nil {
		return err
	}
	project, err := ParseID(spec.ProjectID, "Microsoft.CognitiveServices/accounts/projects")
	if err != nil {
		return err
	}
	client, err := armcognitiveservices.NewProjectsClient(account.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	found, err := client.Get(ctx, account.ResourceGroupName, account.Name, project.Name, nil)
	if err != nil {
		return fmt.Errorf("verify masked registry connection's actual Project identity: %w", err)
	}
	if err := l.verifyDeployedProject(ctx, plan, state, found.Project); err != nil {
		return err
	}
	if !strings.EqualFold(authString(found.Identity.PrincipalID), state.Foundry.ProjectPrincipalID) {
		return fmt.Errorf("masked registry connection's recorded Project principal differs from the actual Project")
	}
	if err := l.verifyProjectParentAccount(ctx, state); err != nil {
		return err
	}
	registry, err := ParseID(spec.RegistryID, "Microsoft.ContainerRegistry/registries")
	if err != nil {
		return err
	}
	expected := grantRequest{scope: spec.RegistryID, principalID: state.Foundry.ProjectPrincipalID,
		roleID: "/subscriptions/" + registry.SubscriptionID + "/providers/Microsoft.Authorization/roleDefinitions/" +
			registryRoleID(spec.RegistryABAC)}
	grants, err := armauthorization.NewRoleAssignmentsClient(registry.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	for _, record := range state.Assignments {
		key := strings.ToLower(record.ID)
		if !record.Complete || record.Deleted || !targets[key] || !succeeded[key] ||
			!strings.EqualFold(record.Scope, expected.scope) ||
			!strings.EqualFold(record.PrincipalID, expected.principalID) ||
			!strings.EqualFold(record.RoleID, expected.roleID) {
			continue
		}
		id, err := ParseID(record.ID, "Microsoft.Authorization/roleAssignments")
		if err != nil || !strings.EqualFold(id.Parent.String(), expected.scope) {
			return fmt.Errorf("masked registry connection's recorded Project pull grant ID is unverified")
		}
		actual, err := grants.GetByID(ctx, record.ID, nil)
		if err != nil {
			return fmt.Errorf("verify masked registry connection's exact Project pull grant: %w", err)
		}
		if !strings.EqualFold(authString(actual.ID), record.ID) || !compatibleGrant(actual.RoleAssignment, expected) ||
			authString(actual.Properties.Description) != state.Owner {
			return fmt.Errorf("masked registry connection's Project pull grant ownership or configuration changed")
		}
		return l.verifyCurrentRegistryConnection(ctx, state, connectionID, endpoint)
	}
	return fmt.Errorf("masked registry connection lacks the actual Project principal's exact owned registry pull grant")
}

// The pinned SDK has no Project connection listSecrets method. The exact account
// view uses the same current-identity contract without becoming an owned target.
// Keep this adapter scoped to the already-verified ManagedIdentity connection; never log its body.
func (l *ownedLifecycle) verifyCurrentRegistryConnection(
	ctx context.Context, state *PreparationState, connectionID, endpoint string,
) error {
	kind := "Microsoft.CognitiveServices/accounts/projects/connections"
	if id, err := arm.ParseResourceID(connectionID); err == nil &&
		strings.EqualFold(id.ResourceType.String(), "Microsoft.CognitiveServices/accounts/connections") {
		registry, err := ParseID(state.Foundry.Spec.RegistryID, "Microsoft.ContainerRegistry/registries")
		if err != nil || !strings.EqualFold(connectionID,
			state.Foundry.Spec.AccountID+"/connections/"+registry.Name+"-conn") {
			return fmt.Errorf("account registry connection is outside the verified project binding")
		}
		kind = "Microsoft.CognitiveServices/accounts/connections"
	}
	if _, err := ParseID(connectionID, kind); err != nil {
		return err
	}
	options := onboardingARMOptions(l.options)
	options.Logging.IncludeBody = false
	client, err := arm.NewClient("azure.ai.projects.onboarding", "v1.0.0", l.credential, options)
	if err != nil {
		return err
	}
	request, err := runtime.NewRequest(ctx, http.MethodPost,
		strings.TrimRight(client.Endpoint(), "/")+connectionID+"/listSecrets?api-version=2026-07-15-preview")
	if err != nil {
		return err
	}
	response, err := client.Pipeline().Do(request)
	if err != nil {
		return fmt.Errorf("read current managed-identity registry connection at %s: %w", connectionID, err)
	}
	defer response.Body.Close()
	if !runtime.HasStatusCode(response, http.StatusOK) {
		// NewResponseError logs the body even with IncludeBody disabled.
		// Retain the service code/status without parsing or logging credential fields.
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		raw, decodeErr := io.ReadAll(response.Body)
		if decodeErr == nil {
			decodeErr = json.Unmarshal(raw, &failure)
		}
		safe := *response
		safe.Body = http.NoBody
		serviceError := &azcore.ResponseError{
			ErrorCode: failure.Error.Code, StatusCode: response.StatusCode, RawResponse: &safe,
		}
		return fmt.Errorf("read current registry connection requires %s/listSecrets/action at %s: %w",
			kind, connectionID, errors.Join(serviceError, decodeErr))
	}
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read current managed-identity registry connection response: %w", err)
	}
	var found struct {
		ID         string                        `json:"id"`
		Properties *registryConnectionProperties `json:"properties"`
		Identity   *armresources.Identity        `json:"identity"`
	}
	if err := json.Unmarshal(raw, &found); err != nil {
		return fmt.Errorf("decode current managed-identity registry connection: %w", err)
	}
	p := found.Properties
	if !strings.EqualFold(found.ID, connectionID) || found.Identity != nil || p == nil ||
		p.Category != "ContainerRegistry" || p.AuthType != "ManagedIdentity" ||
		!strings.EqualFold(p.Target, endpoint) || !strings.EqualFold(p.Metadata.ResourceID, state.Foundry.Spec.RegistryID) ||
		p.UseWorkspaceManagedIdentity == nil || *p.UseWorkspaceManagedIdentity || p.Credentials == nil ||
		!strings.EqualFold(authString(p.Credentials.ClientID), state.Foundry.ProjectPrincipalID) ||
		!strings.EqualFold(authString(p.Credentials.ResourceID), state.Foundry.Spec.RegistryID) {
		return fmt.Errorf("current project registry connection identity, target or registry binding is unverified")
	}
	return nil
}
