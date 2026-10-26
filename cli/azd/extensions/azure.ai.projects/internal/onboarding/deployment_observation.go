// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func deploymentResources(plan Plan) map[string]Resource {
	return map[string]Resource{
		"R01": {ID: "/subscriptions/" + plan.SubscriptionID + "/resourceGroups/" + plan.ResourceGroup,
			Supplied: plan.ResourceGroupSupplied},
		"R02": plan.Storage, "R03": plan.Registry, "R04": plan.Hosting, "R05": plan.Workload,
	}
}

func validateDeploymentData(plan Plan, spec FoundrySpec, state PreparationState) error {
	data := spec.Deployment
	if data == nil {
		return nil // Earlier journal entries retain their exact cleanup contract.
	}
	role, err := arm.ParseResourceID(data.FederatedIdentityRoleDefinitionID)
	if err != nil || !validResourceIDSyntax(data.FederatedIdentityRoleDefinitionID) ||
		!strings.EqualFold(role.ResourceType.String(), "Microsoft.Authorization/roleDefinitions") || data.TenantID == "" {
		return fmt.Errorf("deployment requires the verified tenant and full G02 role-definition ID")
	}
	if data.KubeletRoleAssignmentID != "" {
		if !reusedKubeletGrantScope(data.KubeletRoleAssignmentID, data.RegistryID) {
			return fmt.Errorf("reused kubelet role assignment must apply to the registry or its RG/subscription")
		}
	}
	expected, err := PlannedDeployment(plan, Facts{
		TenantID: data.TenantID, KubeletObjectID: data.KubeletObjectID, RegistryABAC: spec.RegistryABAC,
		HostingIdentity: data.HostingIdentity, WorkloadIdentity: data.WorkloadIdentity,
	})
	if err != nil {
		return err
	}
	if !plan.Storage.Supplied && data.StorageSupplied {
		record, tracked := state.Resources["R02"]
		if !tracked || record.Deleted || state.CleanupComplete || !strings.EqualFold(record.ID, data.StorageID) {
			return fmt.Errorf("R02: owned Storage reference requires the exact active journaled resource")
		}
		expected.StorageSupplied = true
	}
	if !strings.EqualFold(data.ClusterID, expected.ClusterID) || !strings.EqualFold(data.Hosting.ID, expected.Hosting.ID) ||
		!strings.EqualFold(data.Workload.ID, expected.Workload.ID) ||
		!strings.EqualFold(data.StorageID, expected.StorageID) || !strings.EqualFold(data.RegistryID, expected.RegistryID) ||
		data.StorageSupplied != expected.StorageSupplied || data.RegistrySupplied != expected.RegistrySupplied ||
		data.ResourceGroupSupplied != expected.ResourceGroupSupplied || data.StorageSKU != expected.StorageSKU ||
		data.Hosting.Supplied != expected.Hosting.Supplied || data.Workload.Supplied != expected.Workload.Supplied ||
		data.RegistryABAC != spec.RegistryABAC || data.FederatedIdentityRoleDefinitionID == "" {
		return fmt.Errorf("deployment bindings differ from the internally planned resource graph")
	}
	return nil
}

func (l *ownedLifecycle) checkDeploymentResources(ctx context.Context, plan Plan, state PreparationState) error {
	check := func(step, id string, tags map[string]*string) error {
		if state.CleanupComplete {
			return fmt.Errorf("%s: resource exists after completed cleanup; refusing adoption", step)
		}
		return confirmOwned(state, step, id, tags)
	}
	if !plan.ResourceGroupSupplied {
		groups, err := armresources.NewResourceGroupsClient(plan.SubscriptionID, l.credential, l.options)
		if err != nil {
			return err
		}
		found, err := groups.Get(ctx, plan.ResourceGroup, nil)
		if err == nil {
			if err := check("R01", authString(found.ID), found.Tags); err != nil {
				return err
			}
		} else if !responseNotFound(err) {
			return fmt.Errorf("read planned resource-group creation target: %w", err)
		}
	}
	// List only existing identities at the target RG. No GET of a future identity is a preflight prerequisite.
	seenIdentities := map[string]bool{}
	if !plan.Hosting.Supplied || !plan.Workload.Supplied {
		identities, err := armmsi.NewUserAssignedIdentitiesClient(plan.SubscriptionID, l.credential, l.options)
		if err != nil {
			return err
		}
		pager := identities.NewListByResourceGroupPager(plan.ResourceGroup, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if responseNotFound(err) {
				break
			}
			if err != nil {
				return fmt.Errorf("read existing target identities for collision protection: %w", err)
			}
			for _, identity := range page.Value {
				if identity == nil {
					return fmt.Errorf("target identity listing is incomplete")
				}
				for step, resource := range map[string]Resource{"R04": plan.Hosting, "R05": plan.Workload} {
					if resource.Supplied {
						continue
					}
					if strings.EqualFold(authString(identity.ID), resource.ID) {
						seenIdentities[step] = true
						if err := check(step, authString(identity.ID), identity.Tags); err != nil {
							return err
						}
						if err := verifyOwnedIdentity(state, step, *identity); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	for _, step := range []string{"R04", "R05"} {
		if record, tracked := state.Resources[step]; tracked && record.Complete && !record.Deleted &&
			!state.CleanupComplete && !seenIdentities[step] {
			return fmt.Errorf("%s: a previously confirmed identity disappeared; refusing silent replacement", step)
		}
	}
	client, err := armresources.NewClient(plan.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	for step, resource := range map[string]Resource{"R02": plan.Storage, "R03": plan.Registry} {
		if resource.Supplied {
			record, tracked := state.Resources[step]
			if !tracked || state.CleanupComplete {
				continue
			}
			if !strings.EqualFold(record.ID, resource.ID) {
				return fmt.Errorf("%s: supplied selection differs from the active owned resource", step)
			}
		}
		version := "2024-01-01"
		if step == "R03" {
			version = "2025-05-01-preview"
		}
		found, err := client.GetByID(ctx, resource.ID, version, nil)
		if responseNotFound(err) {
			if record, tracked := state.Resources[step]; tracked && record.Complete &&
				!record.Deleted && !state.CleanupComplete {
				return fmt.Errorf("%s: a previously confirmed resource disappeared; refusing silent replacement", step)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: read creation target: %w", step, err)
		}
		if err := check(step, authString(found.ID), found.Tags); err != nil {
			return err
		}
	}
	return nil
}

func (l *ownedLifecycle) observeDeployment(ctx context.Context, plan Plan, state *PreparationState) error {
	if state.Foundry == nil || state.Foundry.Spec.Deployment == nil {
		return nil
	}
	data := state.Foundry.Spec.Deployment
	observedIdentities := map[string]bool{}
	for _, step := range []string{"R04", "R05"} {
		record, exists := state.Resources[step]
		if !exists || record.Deleted {
			continue
		}
		id, err := ParseID(record.ID, "Microsoft.ManagedIdentity/userAssignedIdentities")
		if err != nil {
			return err
		}
		client, err := armmsi.NewUserAssignedIdentitiesClient(id.SubscriptionID, l.credential, l.options)
		if err != nil {
			return err
		}
		found, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
		if responseNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: recover deployed identity: %w", step, err)
		}
		if err := confirmOwned(*state, step, authString(found.ID), found.Tags); err != nil {
			return err
		}
		if found.Properties == nil {
			if record.Identity.PrincipalID != "" {
				return fmt.Errorf("%s: previously observed identity properties disappeared", step)
			}
			record.Identity, record.Complete = Identity{ID: authString(found.ID)}, false
			state.Resources[step] = record
			if err := l.journal.Save(ctx, *state); err != nil {
				return err
			}
			continue
		}
		actual := Identity{
			ID: authString(found.ID), PrincipalID: authString(found.Properties.PrincipalID),
			ClientID: authString(found.Properties.ClientID), TenantID: authString(found.Properties.TenantID),
		}
		if actual.TenantID != "" && !strings.EqualFold(actual.TenantID, data.TenantID) {
			return fmt.Errorf("%s: deployed identity tenant differs from preflight", step)
		}
		if record.Identity.PrincipalID != "" && record.Identity != actual {
			return fmt.Errorf("%s: deployed identity differs from persisted ownership", step)
		}
		record.Identity = actual
		record.Complete = actual.PrincipalID != "" && actual.ClientID != "" && actual.TenantID != ""
		observedIdentities[step] = record.Complete
		state.Resources[step] = record
		if err := l.journal.Save(ctx, *state); err != nil {
			return err
		}
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
	projects, err := armcognitiveservices.NewProjectsClient(account.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	found, err := projects.Get(ctx, account.ResourceGroupName, account.Name, project.Name, nil)
	if err == nil {
		if err := l.verifyDeployedProject(ctx, plan, state, found.Project); err != nil {
			return err
		}
		principal := authString(found.Identity.PrincipalID)
		if state.Foundry.ProjectPrincipalID != "" && !strings.EqualFold(state.Foundry.ProjectPrincipalID, principal) {
			return fmt.Errorf("deployed Foundry project identity changed")
		}
		state.Foundry.ProjectPrincipalID = principal
	} else if !responseNotFound(err) {
		return fmt.Errorf("recover deployed Foundry project principal: %w", err)
	}
	if err := l.journal.Save(ctx, *state); err != nil {
		return err
	}
	recovered := plan
	recovered.Cluster.ID = data.ClusterID
	recovered.Hosting, recovered.Workload = data.Hosting, data.Workload
	recovered.Storage.ID, recovered.Registry.ID = data.StorageID, data.RegistryID
	hosting, workload := state.Resources["R04"].Identity, state.Resources["R05"].Identity
	if data.Hosting.Supplied {
		hosting = data.HostingIdentity
	}
	if data.Workload.Supplied {
		workload = data.WorkloadIdentity
	}
	grants := grantRequests(recovered, Facts{RegistryABAC: data.RegistryABAC, KubeletObjectID: data.KubeletObjectID},
		hosting, workload)
	grants = append(grants, grantRequest{
		checkID: "F01", scope: data.RegistryID, principalID: state.Foundry.ProjectPrincipalID,
		roleID: registryRoleID(data.RegistryABAC),
	})
	for index := range grants {
		if grants[index].checkID == "G02" {
			grants[index].roleID = data.FederatedIdentityRoleDefinitionID
		}
	}
	targets, err := l.deploymentGrantTargets(ctx, plan, state.Foundry)
	if err != nil {
		return err
	}
	for _, record := range state.Assignments {
		targets[strings.ToLower(record.ID)] = true
	}
	observedGrants := map[string]bool{}
	for _, grant := range grants {
		if !createsGrant(recovered, grant.checkID) {
			continue
		}
		if grant.checkID == "G04" && data.KubeletRoleAssignmentID != "" {
			continue
		}
		if grant.checkID == "G02" {
			grant.roleID = data.FederatedIdentityRoleDefinitionID
		}
		if err := l.observeGrant(ctx, state, grant, targets, observedGrants); err != nil {
			return err
		}
	}
	for index := range state.Foundry.Attempts {
		state.Foundry.Attempts[index].OwnershipObserved = true
	}
	if !state.CleanupStarted {
		if err := l.observeProgress(ctx, recovered, state, grants, observedGrants, observedIdentities); err != nil {
			return err
		}
	}
	return l.journal.Save(ctx, *state)
}

func (l *ownedLifecycle) observeGrant(
	ctx context.Context, state *PreparationState, grant grantRequest, targets map[string]bool,
	observed ...map[string]bool,
) error {
	id, err := ParseID(grant.scope, func() string {
		switch grant.checkID {
		case "G01":
			return "Microsoft.ContainerService/managedClusters"
		case "G02":
			return "Microsoft.ManagedIdentity/userAssignedIdentities"
		case "G03":
			return "Microsoft.Storage/storageAccounts"
		default:
			return "Microsoft.ContainerRegistry/registries"
		}
	}())
	if err != nil {
		return err
	}
	if !strings.HasPrefix(grant.roleID, "/") {
		grant.roleID = "/subscriptions/" + id.SubscriptionID +
			"/providers/Microsoft.Authorization/roleDefinitions/" + grant.roleID
	}
	client, err := armauthorization.NewRoleAssignmentsClient(id.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	pager := client.NewListForScopePager(grant.scope,
		&armauthorization.RoleAssignmentsClientListForScopeOptions{Filter: new("atScope()")})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if responseNotFound(err) {
			break // A future scope may not have been created by a failed graph.
		}
		if err != nil {
			return fmt.Errorf("%s: recover deployed grants: %w", grant.checkID, err)
		}
		for _, assignment := range page.Value {
			if assignment == nil || assignment.Properties == nil ||
				authString(assignment.Properties.Description) != state.Owner ||
				!targets[strings.ToLower(authString(assignment.ID))] {
				continue
			}
			expected := grant
			if expected.principalID == "" {
				// A partial graph can retain an exact grant after its identity was removed.
				// Bind the real principal only from the attempted graph's actual assignment ID.
				expected.principalID = authString(assignment.Properties.PrincipalID)
			}
			if expected.principalID == "" || !compatibleGrant(*assignment, expected) {
				continue
			}
			actualID := authString(assignment.ID)
			parsed, err := ParseID(actualID, "Microsoft.Authorization/roleAssignments")
			if err != nil || !strings.EqualFold(parsed.Parent.String(), grant.scope) {
				return fmt.Errorf("%s: owned grant exact ID is unverified", grant.checkID)
			}
			key := strings.ToLower(actualID)
			record := OwnedAssignment{ID: actualID, Scope: grant.scope, PrincipalID: expected.principalID,
				RoleID: grant.roleID, Complete: true}
			for _, old := range state.Assignments {
				if strings.EqualFold(old.ID, actualID) && (old.Deleted ||
					!strings.EqualFold(old.PrincipalID, record.PrincipalID) ||
					!strings.EqualFold(old.RoleID, record.RoleID)) {
					return fmt.Errorf("%s: recorded grant ownership changed", grant.checkID)
				}
			}
			state.Assignments[key] = record
			if len(observed) > 0 {
				observed[0][key] = true
			}
			if err := l.journal.Save(ctx, *state); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *ownedLifecycle) deploymentGrantTargets(
	ctx context.Context, plan Plan, foundry *FoundryState, succeeded ...map[string]bool,
) (map[string]bool, error) {
	// Optional collectors record succeeded grants and path-bound Project/Connection Creates, respectively.
	targets := map[string]bool{}
	visited := map[string]bool{}
	var read func(string, bool, bool, bool, bool) error
	read = func(deploymentID string, observedBefore, failedAttempt, rejectedBeforeCreate, boundPath bool) error {
		key := strings.ToLower(deploymentID)
		if len(succeeded) > 1 {
			key += fmt.Sprint("|bound=", boundPath)
		}
		if visited[key] {
			return nil
		}
		if len(visited) >= 256 {
			return fmt.Errorf("deployment operation graph exceeds the supported observation bound")
		}
		visited[key] = true
		id, err := arm.ParseResourceID(deploymentID)
		if err != nil || !validResourceIDSyntax(deploymentID) ||
			!strings.EqualFold(id.ResourceType.String(), "Microsoft.Resources/deployments") {
			return fmt.Errorf("deployment operation contains an invalid nested deployment ID")
		}
		boundPath = boundPath && strings.EqualFold(id.SubscriptionID, plan.SubscriptionID) &&
			(id.ResourceGroupName == "" || strings.EqualFold(id.ResourceGroupName, plan.ResourceGroup))
		pager, err := NewDeploymentOperationsPager(deploymentID, l.credential, l.options)
		if err != nil {
			return err
		}
		var children []struct {
			id       string
			rejected bool
			bound    bool
		}
		collect := func(operations []*armresources.DeploymentOperation) error {
			for _, operation := range operations {
				if operation == nil || operation.Properties == nil || operation.Properties.TargetResource == nil {
					continue
				}
				target := operation.Properties.TargetResource
				targetID := authString(target.ID)
				switch {
				case strings.EqualFold(authString(target.ResourceType), "Microsoft.Authorization/roleAssignments"):
					if _, err := ParseID(targetID, "Microsoft.Authorization/roleAssignments"); err != nil {
						return fmt.Errorf("deployment operation role-assignment ID is invalid: %w", err)
					}
					targets[strings.ToLower(targetID)] = true
					if len(succeeded) > 0 && succeeded[0] != nil &&
						strings.EqualFold(authString(operation.Properties.ProvisioningState), "Succeeded") {
						succeeded[0][strings.ToLower(targetID)] = true
					}
				case strings.EqualFold(authString(target.ResourceType), "Microsoft.Resources/deployments"):
					children = append(children, struct {
						id       string
						rejected bool
						bound    bool
					}{targetID, failedAttempt && nestedValidationRejected(operation, deploymentID),
						boundPath && succeededCreateOperation(operation, deploymentID)})
				case strings.EqualFold(authString(target.ResourceType), "Microsoft.CognitiveServices/accounts/projects"),
					strings.EqualFold(authString(target.ResourceType), "Microsoft.CognitiveServices/accounts/projects/connections"):
					if len(succeeded) > 1 && succeeded[1] != nil &&
						boundPath && succeededCreateOperation(operation, deploymentID) {
						succeeded[1][strings.ToLower(targetID)] = true
					}
				}
			}
			return nil
		}
		if id.ResourceGroupName == "" && !strings.EqualFold(id.SubscriptionID, plan.SubscriptionID) {
			return fmt.Errorf("unexpected nested subscription deployment outside the recorded target")
		}
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if deploymentRecordNotFound(err) && rejectedBeforeCreate {
				if err := l.confirmRejectedDeploymentAbsent(ctx, deploymentID); err != nil {
					return err
				}
				break // No nested record or operations exist; this branch supplies no owned-grant evidence.
			}
			if responseNotFound(err) && observedBefore {
				break
			}
			if err != nil {
				return fmt.Errorf("read exact ARM graph operations for ownership recovery: %w", err)
			}
			if err := collect(page.Value); err != nil {
				return err
			}
		}
		for _, child := range children {
			if err := read(child.id, observedBefore, failedAttempt, child.rejected, child.bound); err != nil {
				return err
			}
		}
		return nil
	}
	for _, attempt := range foundry.Attempts {
		if err := read(attempt.ID, attempt.OwnershipObserved,
			attempt.Terminal && attempt.ProvisioningState == "Failed", false, attempt.Terminal); err != nil {
			return nil, err
		}
	}
	return targets, nil
}

func succeededCreateOperation(operation *armresources.DeploymentOperation, deploymentID string) bool {
	id, err := arm.ParseResourceID(authString(operation.ID))
	return err == nil && validResourceIDSyntax(authString(operation.ID)) &&
		strings.EqualFold(id.ResourceType.String(), "Microsoft.Resources/deployments/operations") &&
		strings.EqualFold(id.Parent.String(), deploymentID) &&
		operation.Properties != nil &&
		operation.Properties.ProvisioningOperation != nil &&
		*operation.Properties.ProvisioningOperation == armresources.ProvisioningOperationCreate &&
		strings.EqualFold(authString(operation.Properties.ProvisioningState), "Succeeded")
}

func nestedValidationRejected(operation *armresources.DeploymentOperation, deploymentID string) bool {
	id, err := arm.ParseResourceID(authString(operation.ID))
	if err != nil || !strings.EqualFold(id.ResourceType.String(), "Microsoft.Resources/deployments/operations") ||
		!strings.EqualFold(id.Parent.String(), deploymentID) {
		return false
	}
	p := operation.Properties
	if p.ProvisioningOperation == nil || *p.ProvisioningOperation != armresources.ProvisioningOperationCreate ||
		authString(p.ProvisioningState) != "Failed" || authString(p.StatusCode) != "BadRequest" ||
		p.StatusMessage == nil || authString(p.StatusMessage.Status) != "Failed" || p.StatusMessage.Error == nil {
		return false
	}
	serviceError := p.StatusMessage.Error
	if authString(serviceError.Code) != "InvalidTemplateDeployment" ||
		!strings.Contains(strings.ToLower(authString(serviceError.Message)), "preflight validation") {
		return false
	}
	for _, detail := range serviceError.Details {
		if detail != nil && authString(detail.Code) == "InvalidResourceProperties" {
			return true
		}
	}
	return false
}

func (l *ownedLifecycle) confirmRejectedDeploymentAbsent(ctx context.Context, deploymentID string) error {
	id, err := arm.ParseResourceID(deploymentID)
	if err != nil || id.ResourceGroupName == "" {
		return fmt.Errorf("validation-rejected nested deployment has no exact resource-group scope")
	}
	client, err := armresources.NewDeploymentsClient(id.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	_, err = client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if deploymentRecordNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify exact validation-rejected nested deployment absence: %w", err)
	}
	return fmt.Errorf("validation-rejected nested deployment exists but its operations are unavailable")
}

func deploymentRecordNotFound(err error) bool {
	response, ok := errors.AsType[*azcore.ResponseError](err)
	return ok && response.StatusCode == 404 &&
		(response.ErrorCode == "DeploymentNotFound" || response.ErrorCode == "ResourceNotFound")
}
