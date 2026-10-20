// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lifecycleBoundaryLedger() PreparationState {
	const subscription = "11111111-1111-1111-1111-111111111111"
	const registrySubscription = "22222222-2222-2222-2222-222222222222"
	parent := "/subscriptions/" + subscription + "/resourceGroups/owned-rg"
	registry := "/subscriptions/" + registrySubscription +
		"/resourceGroups/customer-rg/providers/Microsoft.ContainerRegistry/registries/customer"
	plan := Plan{
		Owner: "0123456789abcdef", SubscriptionID: subscription, ResourceGroup: "owned-rg", Location: "eastus",
	}
	account := PlannedFoundryAccount(plan)
	return PreparationState{
		Version: 2, Owner: plan.Owner, SubscriptionID: subscription, ResourceGroup: plan.ResourceGroup, Location: plan.Location,
		Resources: map[string]OwnedResource{
			"R01": {ID: parent, Complete: true},
			"R04": {ID: parent + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/hosting", Complete: true},
			"R05": {ID: parent + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/workload", Complete: true},
		},
		Assignments: map[string]OwnedAssignment{
			"project-access": {
				ID:    registry + "/providers/Microsoft.Authorization/roleAssignments/33333333-3333-3333-3333-333333333333",
				Scope: registry, PrincipalID: "44444444-4444-4444-4444-444444444444",
				RoleID: "/subscriptions/" + registrySubscription +
					"/providers/Microsoft.Authorization/roleDefinitions/7f951dda-4ed3-4680-a7ca-43fe172d538d",
				Complete: true,
			},
		},
		Foundry: &FoundryState{
			Spec: FoundrySpec{
				AccountID: account.ID, ProjectID: account.ID + "/projects/project",
				RegistryID: registry, Location: plan.Location, ModelNames: []string{"model-a"},
			},
			Attempts: []FoundryAttempt{{
				ID:            "/subscriptions/" + subscription + "/providers/Microsoft.Resources/deployments/attempt-a",
				CorrelationID: "observed-correlation", Terminal: true,
			}},
		},
	}
}

func TestCleanupPlanPreservesExactPersistedScopeAndHistory(t *testing.T) {
	for _, completed := range []bool{false, true} {
		name := "active"
		if completed {
			name = "completed tombstones"
		}
		t.Run(name, func(t *testing.T) {
			state := lifecycleBoundaryLedger()
			if completed {
				state.CleanupStarted, state.CleanupComplete = true, true
				state.Foundry.AccountDeleted, state.Foundry.Purged = true, true
				for key, record := range state.Resources {
					record.Deleted = key != "R01"
					state.Resources[key] = record
				}
				for key, record := range state.Assignments {
					record.Deleted = true
					state.Assignments[key] = record
				}
			}
			before, err := json.Marshal(state)
			require.NoError(t, err)

			plan, err := CleanupPlan(state)

			require.NoError(t, err)
			assert.Equal(t, state.Owner, plan.Owner)
			assert.Equal(t, state.SubscriptionID, plan.SubscriptionID)
			assert.Equal(t, state.ResourceGroup, plan.ResourceGroup)
			assert.Equal(t, state.Location, plan.Location)
			after, err := json.Marshal(state)
			require.NoError(t, err)
			assert.Equal(t, before, after, "resolving cleanup must not discard grants, attempts or deletion tombstones")
		})
	}
}

func TestCleanupPlanRejectsAmbiguousPersistedOwnership(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*PreparationState)
	}{
		{"absent version", func(s *PreparationState) { s.Version = 0 }},
		{"legacy without Foundry", func(s *PreparationState) { s.Version, s.Foundry = 1, nil }},
		{"legacy with Foundry", func(s *PreparationState) { s.Version = 1 }},
		{"future version", func(s *PreparationState) { s.Version = 3 }},
		{"missing owner", func(s *PreparationState) { s.Owner = "" }},
		{"missing subscription", func(s *PreparationState) { s.SubscriptionID = "" }},
		{"missing group", func(s *PreparationState) { s.ResourceGroup = "" }},
		{"missing location", func(s *PreparationState) { s.Location = "" }},
		{"invalid target group", func(s *PreparationState) { s.ResourceGroup = "owned-rg/providers/foreign/type" }},
		{"foreign owned resource", func(s *PreparationState) {
			record := s.Resources["R04"]
			record.ID = strings.Replace(record.ID, s.SubscriptionID, "55555555-5555-5555-5555-555555555555", 1)
			s.Resources["R04"] = record
		}},
		{"unknown resource step", func(s *PreparationState) { s.Resources["R99"] = s.Resources["R04"] }},
		{"wrong resource type", func(s *PreparationState) { s.Resources["R02"] = s.Resources["R04"] }},
		{"assignment scope mismatch", func(s *PreparationState) {
			record := s.Assignments["project-access"]
			record.Scope += "-other"
			s.Assignments["project-access"] = record
		}},
		{"assignment missing principal", func(s *PreparationState) {
			record := s.Assignments["project-access"]
			record.PrincipalID = ""
			s.Assignments["project-access"] = record
		}},
		{"assignment missing role", func(s *PreparationState) {
			record := s.Assignments["project-access"]
			record.RoleID = ""
			s.Assignments["project-access"] = record
		}},
		{"subnet without topology", func(s *PreparationState) {
			s.Resources["R06Subnet"] = OwnedResource{
				ID: "/subscriptions/" + s.SubscriptionID + "/resourceGroups/network-rg/" +
					"providers/Microsoft.Network/virtualNetworks/nodes/subnets/agents",
			}
		}},
		{"account target mismatch", func(s *PreparationState) { s.Foundry.Spec.AccountID += "-other" }},
		{"project parent mismatch", func(s *PreparationState) {
			s.Foundry.Spec.ProjectID = strings.Replace(s.Foundry.Spec.ProjectID, "/ais-", "/other-", 1)
		}},
		{"account location mismatch", func(s *PreparationState) { s.Foundry.Spec.Location = "westus" }},
		{"model name contains child path", func(s *PreparationState) { s.Foundry.Spec.ModelNames = []string{"model/child"} }},
		{"model name contains query", func(s *PreparationState) { s.Foundry.Spec.ModelNames = []string{"model?value"} }},
		{"model name contains encoded separator", func(s *PreparationState) {
			s.Foundry.Spec.ModelNames = []string{"model%2fchild"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := lifecycleBoundaryLedger()
			tc.mutate(&state)
			_, err := CleanupPlan(state)
			require.Error(t, err, "ambiguous persisted ownership must not become a destructive cleanup plan")
		})
	}
}

func TestPlannedFoundryAccountIsExactAndNotSupplied(t *testing.T) {
	state := lifecycleBoundaryLedger()
	plan, err := CleanupPlan(state)
	require.NoError(t, err)

	account := PlannedFoundryAccount(plan)

	assert.Equal(t, "ais-"+state.Owner, account.Name)
	assert.Equal(t, "/subscriptions/"+state.SubscriptionID+"/resourceGroups/"+state.ResourceGroup, account.Parent)
	assert.Equal(t, account.Parent+"/providers/Microsoft.CognitiveServices/accounts/"+account.Name, account.ID)
	assert.Equal(t, state.Foundry.Spec.AccountID, account.ID)
	assert.False(t, account.Supplied)
	id, err := ParseID(account.ID, "Microsoft.CognitiveServices/accounts")
	require.NoError(t, err)
	assert.Equal(t, state.SubscriptionID, id.SubscriptionID)
	assert.Equal(t, state.ResourceGroup, id.ResourceGroupName)
}

func TestLifecycleAttemptIntentIsDurableBeforeARM(t *testing.T) {
	state := lifecycleBoundaryLedger()
	plan, err := CleanupPlan(state)
	require.NoError(t, err)
	journal := &memoryPreparationJournal{}
	require.NoError(t, journal.Save(t.Context(), state))
	spec := state.Foundry.Spec
	spec.ModelNames = []string{"MODEL-A", "model-b"}
	id := "/subscriptions/" + plan.SubscriptionID + "/providers/Microsoft.Resources/deployments/attempt-b"

	require.NoError(t, NewLifecycle(nil, nil, journal).Start(t.Context(), plan, spec, id))

	saved, err := journal.Load(t.Context())
	require.NoError(t, err)
	require.Len(t, saved.Foundry.Attempts, 2)
	assert.Equal(t, state.Foundry.Attempts[0], saved.Foundry.Attempts[0])
	assert.Equal(t, FoundryAttempt{ID: id}, saved.Foundry.Attempts[1],
		"a new intent must not inherit a terminal state or correlation from an earlier attempt")
	assert.Equal(t, []string{"model-a", "model-b"}, saved.Foundry.Spec.ModelNames)
	assert.Equal(t, state.Resources, saved.Resources)
	assert.Equal(t, state.Assignments, saved.Assignments)
}

func TestLifecycleAttemptRejectsAmbiguousJournalWithoutSaving(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*PreparationState, *string)
	}{
		{"legacy without Foundry", func(s *PreparationState, _ *string) { s.Version, s.Foundry = 1, nil }},
		{"unknown version", func(s *PreparationState, _ *string) { s.Version = 3 }},
		{"different environment", func(s *PreparationState, _ *string) { s.Owner = "another-environment" }},
		{"unfinished cleanup", func(s *PreparationState, _ *string) { s.CleanupStarted = true }},
		{"latest attempt unresolved", func(s *PreparationState, _ *string) {
			s.Foundry.Attempts = append(s.Foundry.Attempts, FoundryAttempt{
				ID: "/subscriptions/" + s.SubscriptionID + "/providers/Microsoft.Resources/deployments/unknown-attempt",
			})
		}},
		{"terminal attempt ID reused", func(s *PreparationState, id *string) {
			*id = strings.ToUpper(s.Foundry.Attempts[0].ID)
		}},
		{"different deployment subscription", func(s *PreparationState, id *string) {
			*id = strings.Replace(*id, s.SubscriptionID, "55555555-5555-5555-5555-555555555555", 1)
		}},
		{"resource group deployment", func(s *PreparationState, id *string) {
			*id = strings.Replace(*id, "/providers/", "/resourceGroups/"+s.ResourceGroup+"/providers/", 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := lifecycleBoundaryLedger()
			plan, err := CleanupPlan(state)
			require.NoError(t, err)
			spec := state.Foundry.Spec
			id := "/subscriptions/" + plan.SubscriptionID + "/providers/Microsoft.Resources/deployments/new-attempt"
			tc.mutate(&state, &id)
			journal := &memoryPreparationJournal{}
			require.NoError(t, journal.Save(t.Context(), state))
			before := slices.Clone(journal.data)

			err = NewLifecycle(nil, nil, journal).Start(t.Context(), plan, spec, id)

			require.Error(t, err)
			assert.Equal(t, before, journal.data, "an invalid lifecycle must not overwrite recovery evidence")
		})
	}
}

func TestLifecycleAttemptCancellationAndSaveFailureKeepPriorEvidence(t *testing.T) {
	state := lifecycleBoundaryLedger()
	plan, err := CleanupPlan(state)
	require.NoError(t, err)
	journal := &memoryPreparationJournal{}
	require.NoError(t, journal.Save(t.Context(), state))
	before := slices.Clone(journal.data)
	lifecycle := NewLifecycle(nil, nil, journal)
	id := "/subscriptions/" + plan.SubscriptionID + "/providers/Microsoft.Resources/deployments/attempt-b"
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, lifecycle.Start(ctx, plan, state.Foundry.Spec, id), context.Canceled)
	assert.Equal(t, before, journal.data)

	journal.fail = true
	require.ErrorContains(t, lifecycle.Start(t.Context(), plan, state.Foundry.Spec, id), "journal unavailable")
	assert.Equal(t, before, journal.data)

	journal.fail = false
	require.NoError(t, lifecycle.Start(t.Context(), plan, state.Foundry.Spec, id))
	saved, err := journal.Load(t.Context())
	require.NoError(t, err)
	require.Len(t, saved.Foundry.Attempts, 2)
	assert.Equal(t, id, saved.Foundry.Attempts[1].ID)
}
