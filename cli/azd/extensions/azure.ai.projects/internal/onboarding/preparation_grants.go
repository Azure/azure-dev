// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/google/uuid"
)

func stableAssignmentName(scope, principal, role string) string {
	key := strings.ToLower(strings.TrimRight(scope, "/") + "|" + principal + "|" + role)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
}

func compatibleGrant(assignment armauthorization.RoleAssignment, grant grantRequest) bool {
	p := assignment.Properties
	return p != nil && strings.EqualFold(authString(p.Scope), grant.scope) &&
		strings.EqualFold(authString(p.PrincipalID), grant.principalID) &&
		strings.EqualFold(authString(p.RoleDefinitionID), grant.roleID) &&
		p.PrincipalType != nil && *p.PrincipalType == armauthorization.PrincipalTypeServicePrincipal &&
		authString(p.Condition) == "" && authString(p.ConditionVersion) == "" &&
		authString(p.DelegatedManagedIdentityResourceID) == ""
}

func (p *ownedPreparer) prepareGrant(
	ctx context.Context, state *PreparationState, grant grantRequest,
) error {
	if _, err := uuid.Parse(grant.principalID); err != nil {
		return fmt.Errorf("%s: a principal object GUID is required, not an ARM ID or client name", grant.checkID)
	}
	id, err := arm.ParseResourceID(grant.scope)
	if err != nil || id.SubscriptionID == "" {
		return fmt.Errorf("%s: invalid grant scope", grant.checkID)
	}
	client, err := armauthorization.NewRoleAssignmentsClient(id.SubscriptionID, p.credential, p.options)
	if err != nil {
		return err
	}
	name := stableAssignmentName(grant.scope, grant.principalID, grant.roleID)
	assignmentID := grant.scope + "/providers/Microsoft.Authorization/roleAssignments/" + name
	// Reuse an exact existing compatible grant even when its GUID was selected by an administrator.
	pager := client.NewListForScopePager(grant.scope,
		&armauthorization.RoleAssignmentsClientListForScopeOptions{Filter: new("atScope()")})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("%s: read existing grants: %w", grant.checkID, err)
		}
		for _, existing := range page.Value {
			if existing != nil && compatibleGrant(*existing, grant) {
				return p.recoverGrant(ctx, state, name, grant.checkID, *existing)
			}
		}
	}
	found, err := client.GetByID(ctx, assignmentID, nil)
	if err == nil {
		if !compatibleGrant(found.RoleAssignment, grant) {
			return fmt.Errorf("%s: deterministic assignment ID already has an incompatible grant", grant.checkID)
		}
		return p.recoverGrant(ctx, state, name, grant.checkID, found.RoleAssignment)
	}
	if !responseNotFound(err) {
		return fmt.Errorf("%s: read planned grant: %w", grant.checkID, err)
	}
	record := OwnedAssignment{
		ID: assignmentID, Scope: grant.scope, PrincipalID: grant.principalID, RoleID: grant.roleID,
	}
	if old, ok := state.Assignments[name]; ok &&
		(!strings.EqualFold(old.ID, record.ID) || !strings.EqualFold(old.PrincipalID, record.PrincipalID) ||
			!strings.EqualFold(old.RoleID, record.RoleID) || !strings.EqualFold(old.Scope, record.Scope)) {
		return fmt.Errorf("%s: assignment intent differs from the journal", grant.checkID)
	}
	state.Assignments[name] = record
	if err := p.journal.Save(ctx, *state); err != nil {
		return fmt.Errorf("%s: persist grant intent before write: %w", grant.checkID, err)
	}
	parameters := armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			PrincipalID: new(grant.principalID), RoleDefinitionID: new(grant.roleID),
			PrincipalType: new(armauthorization.PrincipalTypeServicePrincipal), Description: new(state.Owner),
		},
	}
	err = retryPrincipalReplication(ctx, func() error {
		_, err := client.Create(ctx, grant.scope, name, parameters, nil)
		return err
	})
	if err != nil {
		return fmt.Errorf("%s: create scoped grant: %w", grant.checkID, err)
	}
	readback, err := client.GetByID(ctx, assignmentID, nil)
	if err != nil {
		return fmt.Errorf("%s: read created grant: %w", grant.checkID, err)
	}
	if !compatibleGrant(readback.RoleAssignment, grant) ||
		authString(readback.Properties.Description) != state.Owner {
		return fmt.Errorf("%s: created grant readback does not match the journaled contract", grant.checkID)
	}
	record.Complete = true
	state.Assignments[name] = record
	if err := p.journal.Save(ctx, *state); err != nil {
		return fmt.Errorf("%s: persist grant result: %w", grant.checkID, err)
	}
	// ARM readback proves configuration only. Hosting/workload/kubelet access is verified by its later real operation.
	return nil
}

func (p *ownedPreparer) recoverGrant(
	ctx context.Context, state *PreparationState, name, checkID string, existing armauthorization.RoleAssignment,
) error {
	record, tracked := state.Assignments[name]
	if !tracked {
		return nil
	}
	properties := existing.Properties
	if properties == nil || !strings.EqualFold(record.ID, authString(existing.ID)) ||
		!strings.EqualFold(record.Scope, authString(properties.Scope)) ||
		!strings.EqualFold(record.PrincipalID, authString(properties.PrincipalID)) ||
		!strings.EqualFold(record.RoleID, authString(properties.RoleDefinitionID)) ||
		authString(properties.Description) != state.Owner {
		return fmt.Errorf("%s: tracked grant ownership changed", checkID)
	}
	record.Complete = true
	state.Assignments[name] = record
	if err := p.journal.Save(ctx, *state); err != nil {
		return fmt.Errorf("%s: persist recovered grant: %w", checkID, err)
	}
	return nil
}

func retryPrincipalReplication(ctx context.Context, operation func() error) error {
	var err error
	for attempt := range 5 {
		if err := ctx.Err(); err != nil {
			return err
		}
		err = operation()
		if err == nil {
			return nil
		}
		response, ok := errors.AsType[*azcore.ResponseError](err)
		if !ok || response.ErrorCode != "PrincipalNotFound" || attempt == 4 {
			return err
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}
