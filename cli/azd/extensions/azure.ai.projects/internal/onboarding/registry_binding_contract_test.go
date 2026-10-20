// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreparedRegistryBindingIsDurableForCreatedAndSuppliedRegistries(t *testing.T) {
	for _, supplied := range []bool{false, true} {
		for _, abac := range []bool{false, true} {
			f, facts := preparationContractFixture(t, false, supplied, abac)
			calls := 0
			preparer := NewPreparerWithVerifier(contractCredential{}, f.options(), f.journal,
				verifiedPreparationNetwork{&calls})
			prepared, err := preparer.Prepare(t.Context(), f.plan, facts)
			require.NoError(t, err)
			state, err := f.journal.Load(t.Context())
			require.NoError(t, err)
			require.Equal(t, &RegistryBinding{
				ID: prepared.RegistryID, Endpoint: prepared.RegistryEndpoint, ABAC: prepared.RegistryABAC, Verified: true,
			}, state.Registry)
			prior := len(f.writes)
			again, err := preparer.Prepare(t.Context(), f.plan, facts)
			require.NoError(t, err)
			assert.Equal(t, prepared, again)
			assert.Len(t, f.writes, prior, "retry must reuse the same prepared resources and grants")
			require.Equal(t, 2, calls, "both retries must verify actual prepared network paths")
		}
	}
}

func TestPreparationRetryRejectsRegistrySelectionDriftBeforeWrites(t *testing.T) {
	f, facts := preparationContractFixture(t, false, true, false)
	calls := 0
	preparer := NewPreparerWithVerifier(contractCredential{}, f.options(), f.journal,
		verifiedPreparationNetwork{&calls})
	_, err := preparer.Prepare(t.Context(), f.plan, facts)
	require.NoError(t, err)
	before := len(f.writes)
	plan := f.plan
	plan.Registry.ID += "different"
	_, err = preparer.Prepare(t.Context(), plan, facts)
	require.ErrorContains(t, err, "registry differs")
	require.Len(t, f.writes, before)
}

func TestPreparedRegistryReadbackRejectsEndpointOrModeDrift(t *testing.T) {
	for _, field := range []string{"loginServer", "roleAssignmentMode"} {
		t.Run(field, func(t *testing.T) {
			f, facts := preparationContractFixture(t, true, true, false)
			calls := 0
			preparer := &ownedPreparer{credential: contractCredential{}, options: f.options(),
				journal: f.journal, network: verifiedPreparationNetwork{&calls}}
			_, err := preparer.Prepare(t.Context(), f.plan, facts)
			require.NoError(t, err)
			state, err := f.journal.Load(t.Context())
			require.NoError(t, err)
			properties := cleanupObject(t, f.resources[strings.ToLower(f.plan.Registry.ID)]["properties"])
			if field == "loginServer" {
				properties[field] = "changed.azurecr.io"
			} else {
				properties[field] = "AbacRepositoryPermissions"
			}
			prior := len(f.writes)
			_, _, err = preparer.prepareRegistry(t.Context(), f.plan, &state, field == "roleAssignmentMode")
			require.ErrorContains(t, err, "prepared binding")
			require.Len(t, f.writes, prior)
		})
	}
}

func TestFoundryRetryRejectsRegistryModeAndTargetChangesBeforeAzureAccess(t *testing.T) {
	for _, field := range []string{"registry", "mode", "project", "account"} {
		t.Run(field, func(t *testing.T) {
			state := lifecycleBoundaryLedger()
			plan, err := CleanupPlan(state)
			require.NoError(t, err)
			spec := state.Foundry.Spec
			switch field {
			case "registry":
				spec.RegistryID += "different"
			case "mode":
				spec.RegistryABAC = !spec.RegistryABAC
			case "project":
				spec.ProjectID += "different"
			case "account":
				spec.AccountID += "different"
			}
			journal := &memoryPreparationJournal{}
			require.NoError(t, journal.Save(t.Context(), state))
			before := string(journal.data)
			lifecycle := NewLifecycle(nil, nil, journal)
			_, err = lifecycle.Check(t.Context(), plan, spec)
			require.Error(t, err)
			require.Error(t, lifecycle.Start(context.WithoutCancel(t.Context()), plan, spec,
				"/subscriptions/"+plan.SubscriptionID+"/providers/Microsoft.Resources/deployments/new-attempt"))
			require.Equal(t, before, string(journal.data))
		})
	}
}
