// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func (l *ownedLifecycle) observeProgress(
	ctx context.Context, plan Plan, state *PreparationState, grants []grantRequest,
	observed, observedIdentities map[string]bool,
) error {
	data := state.Foundry.Spec.Deployment
	progress := selectedProgress(plan)
	for key, current := range progress {
		current.PreflightPassed = state.ResourceProgress[key].PreflightPassed
		progress[key] = current
	}
	state.ResourceProgress = progress
	var failures []error
	checker := &sdkResourceChecker{credential: l.credential, options: l.options}
	identities := map[string]bool{}
	for _, item := range []struct {
		key, step string
		resource  Resource
		expected  Identity
	}{
		{"hostingIdentity", "R04", data.Hosting, data.HostingIdentity},
		{"workloadIdentity", "R05", data.Workload, data.WorkloadIdentity},
	} {
		ready := state.Resources[item.step].Complete && observedIdentities[item.step]
		if item.resource.Supplied {
			actual, check := checker.readSuppliedIdentity(ctx, item.resource, data.TenantID)
			ready = check.Status == Pass && actual == item.expected
			if check.Status != Pass {
				failures = append(failures, Report{check}.Err())
			} else if actual != item.expected {
				failures = append(failures, fmt.Errorf("%s: supplied identity metadata changed after preflight", item.key))
			}
		}
		identities[item.key] = ready
	}
	storageReady, storageErr := l.storagePreparationReady(ctx, plan)
	failures = append(failures, storageErr)
	registryReady, endpoint, registryErr := l.registryPreparationReady(ctx, plan, data.RegistryABAC)
	failures = append(failures, registryErr)
	grantReady := map[string]bool{}
	for _, grant := range grants {
		ready, err := l.preparationGrantReady(ctx, plan, data, *state, grant, observed)
		if err != nil {
			failures = append(failures, err)
		}
		previous, seen := grantReady[grant.checkID]
		grantReady[grant.checkID] = ready && (!seen || previous)
	}
	connectionReady, connectionErr := l.registryConnectionReady(ctx, state, endpoint)
	failures = append(failures, connectionErr)
	complete := map[string]bool{
		"storageAccount":    storageReady && grantReady["G03"],
		"hostingIdentity":   identities["hostingIdentity"] && grantReady["G01"] && grantReady["G02"],
		"workloadIdentity":  identities["workloadIdentity"] && grantReady["G02"] && grantReady["G03"],
		"containerRegistry": registryReady && grantReady["G04"] && grantReady["F01"] && connectionReady,
	}
	for key, done := range complete {
		entry := state.ResourceProgress[key]
		entry.PreparationComplete = done
		state.ResourceProgress[key] = entry
	}
	for step, ready := range map[string]bool{"R02": storageReady, "R03": registryReady} {
		if record, exists := state.Resources[step]; exists {
			record.Complete = record.Complete || ready
			state.Resources[step] = record
		}
	}
	if err := l.journal.Save(ctx, *state); err != nil {
		return errors.Join(errors.Join(failures...), err)
	}
	return errors.Join(failures...)
}

func (l *ownedLifecycle) preparationGrantReady(
	ctx context.Context, plan Plan, data *DeploymentData, state PreparationState,
	grant grantRequest, observed map[string]bool,
) (bool, error) {
	if grant.principalID == "" {
		return false, nil
	}
	if !createsGrant(plan, grant.checkID) {
		authorizer := &scopedAuthorizer{credential: l.credential, options: l.options}
		status, description, err := authorizer.identityGrantAllows(ctx, plan.SubscriptionID, grant)
		if status == Unverified {
			if err == nil {
				err = fmt.Errorf("%s: %s", grant.checkID, description)
			}
			return false, err
		}
		return status == Pass, err
	}
	if grant.checkID == "G04" && data.KubeletRoleAssignmentID != "" {
		id, err := existingKubeletPullGrant(ctx, l.credential, l.options, plan, Facts{
			RegistryABAC: data.RegistryABAC, KubeletObjectID: data.KubeletObjectID,
		})
		return id != "", err
	}
	for id := range observed {
		record := state.Assignments[id]
		role := grant.roleID
		if !strings.HasPrefix(role, "/") {
			scope, err := ParseID(record.ID, "Microsoft.Authorization/roleAssignments")
			if err != nil {
				return false, err
			}
			role = "/subscriptions/" + scope.SubscriptionID + "/providers/Microsoft.Authorization/roleDefinitions/" + role
		}
		if record.Complete && !record.Deleted && strings.EqualFold(record.Scope, grant.scope) &&
			strings.EqualFold(record.PrincipalID, grant.principalID) && strings.EqualFold(record.RoleID, role) {
			return true, nil
		}
	}
	return false, nil
}

func (l *ownedLifecycle) storagePreparationReady(ctx context.Context, plan Plan) (bool, error) {
	checker := &sdkResourceChecker{credential: l.credential, options: l.options}
	account, report := checker.readStorage(ctx, plan.Storage)
	if account == nil {
		for _, check := range report {
			if check.Cause != nil && !responseNotFound(check.Cause) {
				return false, report.Err()
			}
		}
		return false, nil
	}
	report = append(report, storageResourceChecks(*account)...)
	if err := report.Err(); err != nil {
		return false, err
	}
	if !plan.Storage.Supplied {
		p := account.Properties
		if p == nil || resourceValue(p.PublicNetworkAccess) != "Enabled" ||
			p.NetworkRuleSet == nil || resourceValue(p.NetworkRuleSet.DefaultAction) != "Allow" {
			return false, fmt.Errorf("Storage preparation requires the planned public Enabled/default Allow configuration")
		}
	}
	return true, nil
}

func (l *ownedLifecycle) registryPreparationReady(
	ctx context.Context, plan Plan, expectedABAC bool,
) (bool, string, error) {
	id, err := ParseID(plan.Registry.ID, "Microsoft.ContainerRegistry/registries")
	if err != nil {
		return false, "", err
	}
	client, err := armcontainerregistry.NewRegistriesClient(id.SubscriptionID, l.credential, l.options)
	if err != nil {
		return false, "", err
	}
	found, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if responseNotFound(err) {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("confirm prepared registry configuration: %w", err)
	}
	abac, check := registryResourceCheck(found.Registry)
	if !strings.EqualFold(authString(found.ID), plan.Registry.ID) || abac != expectedABAC {
		return false, "", fmt.Errorf("prepared registry ID or permission mode differs from the deployment binding")
	}
	if check.Status != Pass {
		return false, "", Report{check}.Err()
	}
	return true, authString(found.Properties.LoginServer), nil
}

func (l *ownedLifecycle) registryConnectionReady(
	ctx context.Context, state *PreparationState, endpoint string,
) (bool, error) {
	spec := state.Foundry.Spec
	if state.Foundry.ProjectPrincipalID == "" || endpoint == "" {
		return false, nil
	}
	registry, err := ParseID(spec.RegistryID, "Microsoft.ContainerRegistry/registries")
	if err != nil {
		return false, err
	}
	connectionID := spec.ProjectID + "/connections/" + registry.Name + "-conn"
	client, err := armresources.NewClient(state.SubscriptionID, l.credential, l.options)
	if err != nil {
		return false, err
	}
	found, err := client.GetByID(ctx, connectionID, "2025-04-01-preview", nil)
	if responseNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("confirm prepared project registry connection: %w", err)
	}
	var properties registryConnectionProperties
	raw, err := json.Marshal(found.Properties)
	if err != nil {
		return false, fmt.Errorf("encode project registry connection evidence: %w", err)
	}
	if err := json.Unmarshal(raw, &properties); err != nil {
		return false, fmt.Errorf("decode project registry connection evidence: %w", err)
	}
	if !strings.EqualFold(authString(found.ID), connectionID) ||
		properties.Category != "ContainerRegistry" || properties.AuthType != "ManagedIdentity" ||
		!strings.EqualFold(properties.Target, endpoint) ||
		!strings.EqualFold(properties.Metadata.ResourceID, spec.RegistryID) || found.Identity != nil ||
		properties.UseWorkspaceManagedIdentity != nil && *properties.UseWorkspaceManagedIdentity {
		return false, fmt.Errorf("project registry connection identity, target or registry binding is unverified")
	}
	if properties.Credentials == nil {
		if properties.UseWorkspaceManagedIdentity == nil {
			return false, fmt.Errorf("project registry connection workspace identity selection is unverified")
		}
		if err := l.verifyMaskedRegistryConnection(ctx, state, connectionID, endpoint); err != nil {
			return false, err
		}
	} else if !strings.EqualFold(authString(properties.Credentials.ClientID), state.Foundry.ProjectPrincipalID) ||
		!strings.EqualFold(authString(properties.Credentials.ResourceID), spec.RegistryID) {
		return false, fmt.Errorf("project registry connection explicit credential identity or registry binding conflicts")
	}
	return true, nil
}
