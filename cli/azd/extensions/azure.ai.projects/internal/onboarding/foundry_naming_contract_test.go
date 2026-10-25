// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFoundryResourceNaming(t *testing.T) {
	for _, tt := range []struct {
		name string
		part string
	}{
		{"Simple-Foundry_Project", "simple"},
		{"Ab12", "ab12"},
		{"A", "a"},
		{"123456789", "123456"},
		{"--中 A_1. B/2 éC!?-D", "a1b2cd"},
		{"", ""},
		{"--中_é !", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := contractPlanInput()
			in.StorageID, in.RegistryID = "", ""
			in.FoundryName = tt.name
			in.Environment, in.ProjectPath = "DEV ", `C:\Project\..\Raw `
			plan, err := BuildPlan(in)
			if tt.part == "" {
				require.ErrorContains(t, err, "I03")
				require.ErrorContains(t, err, "foundryProjectName")
				return
			}
			require.NoError(t, err)
			parent := "/subscriptions/" + in.SubscriptionID + "/resourceGroups/" + in.ResourceGroup
			sum := sha256.Sum256([]byte(strings.ToLower(parent) + "|" + in.Environment + "|" + in.ProjectPath))
			token := fmt.Sprintf("%x", sum[:8])
			assert.Equal(t, token, plan.Owner, "Foundry name must not change the owner algorithm")
			assert.Equal(t, "sa"+tt.part+token, plan.Storage.Name)
			assert.Regexp(t, `^[a-z0-9]{3,24}$`, plan.Storage.Name)
			assert.LessOrEqual(t, len(plan.Storage.Name), 24)
			assert.Equal(t, "hosting-"+tt.part+token, plan.Hosting.Name)
			assert.Equal(t, "workload-"+tt.part+token, plan.Workload.Name)
			for _, resource := range []Resource{plan.Storage, plan.Hosting, plan.Workload} {
				assert.True(t, strings.HasSuffix(resource.ID, "/"+resource.Name))
			}
			assert.Equal(t, "azd"+token, plan.Registry.Name, "ACR naming is unchanged")
			assert.Equal(t, "ais-"+token, PlannedFoundryAccount(plan).Name)
			retry, err := BuildPlan(in)
			require.NoError(t, err)
			assert.Equal(t, plan, retry)
			in.FoundryName = "AnotherProject"
			other, err := BuildPlan(in)
			require.NoError(t, err)
			assert.Equal(t, plan.Owner, other.Owner)
			assert.NotEqual(t, plan.Storage.Name, other.Storage.Name)
		})
	}
}

func TestFoundryResourceNamingPreservesSuppliedStorage(t *testing.T) {
	in := contractPlanInput()
	plan, err := BuildPlan(in)
	require.NoError(t, err)
	assert.Equal(t, in.StorageID, plan.Storage.ID)
	assert.Equal(t, "customer", plan.Storage.Name)
	assert.True(t, plan.Storage.Supplied)
	assert.Equal(t, in.RegistryID, plan.Registry.ID)
	data, err := PlannedDeployment(plan, Facts{TenantID: "tenant", KubeletObjectID: "kubelet"})
	require.NoError(t, err)
	assert.True(t, data.StorageSupplied)
	assert.Equal(t, in.StorageID, data.StorageID)
}

func TestFoundryNamingRetainsLegacyJournalForExactCleanup(t *testing.T) {
	f := newCleanupARM(t, false, false)
	current := f.base.plan
	for _, item := range []struct {
		resource *Resource
		name     string
	}{
		{&f.base.plan.Storage, "azd" + current.Owner},
		{&f.base.plan.Hosting, "hosting-" + current.Owner},
		{&f.base.plan.Workload, "workload-" + current.Owner},
	} {
		r := item.resource
		r.ID = strings.TrimSuffix(r.ID, r.Name) + item.name
		r.Name = item.name
	}

	f.spec.Deployment.StorageID = f.base.plan.Storage.ID
	f.spec.Deployment.Hosting = f.base.plan.Hosting
	f.spec.Deployment.Workload = f.base.plan.Workload
	f.deploy(t)
	journal := append([]byte(nil), f.base.journal.data...)
	next := f.spec
	bindings := *f.spec.Deployment
	bindings.StorageID, bindings.Hosting, bindings.Workload = current.Storage.ID, current.Hosting, current.Workload
	next.Deployment = &bindings
	_, err := f.lifecycle.Check(t.Context(), current, next)
	require.Error(t, err, "new names cannot replace or adopt an active legacy deployment")
	require.Equal(t, journal, f.base.journal.data, "failed retry must retain exact historical IDs")
	state, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	cleanupPlan, err := CleanupPlan(state)
	require.NoError(t, err)
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), cleanupPlan, false, nil))
	for _, resource := range []Resource{f.base.plan.Storage, f.base.plan.Hosting, f.base.plan.Workload} {
		assert.Contains(t, f.deletes, resource.ID)
	}
	for _, resource := range []Resource{current.Storage, current.Hosting, current.Workload} {
		assert.NotContains(t, f.deletes, resource.ID, "cleanup never recomputes resource names")
	}
}

func TestFoundryNamingRetryUsesExactCurrentOwnedResources(t *testing.T) {
	f := newCleanupARM(t, false, false)
	f.deploy(t)
	journal := append([]byte(nil), f.base.journal.data...)
	data, err := ResolveDeploymentBindings(t.Context(), contractCredential{}, f.options, f.base.plan, Facts{
		TenantID: f.spec.Deployment.TenantID, KubeletObjectID: f.spec.Deployment.KubeletObjectID,
	})
	require.NoError(t, err)
	assert.True(t, data.StorageSupplied, "an exact owned retry references rather than recreates Storage")
	assert.Equal(t, f.base.plan.Storage.ID, data.StorageID)
	assert.Equal(t, f.base.plan.Hosting, data.Hosting)
	assert.Equal(t, f.base.plan.Workload, data.Workload)
	spec := f.spec
	spec.Deployment = &data
	report, err := f.lifecycle.Check(t.Context(), f.base.plan, spec)
	require.NoError(t, err)
	require.NoError(t, report.Err())
	assert.Equal(t, journal, f.base.journal.data)
}
