// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureprovider

import (
	"errors"
	"strings"
	"testing"

	"azure.ai.latency/internal/model"
)

func TestGetOptionsCombinesCapabilityCapacityAndQuota(t *testing.T) {
	t.Parallel()
	client := eligibleOfferClient()
	provider := &Provider{
		subscriptionID: testSubscription,
		cognitive:      client,
	}

	options, err := provider.GetOptions(t.Context(), offerDeployment())
	if err != nil {
		t.Fatalf("GetOptions returned an error: %v", err)
	}
	priority := optionByName(t, options, model.OfferPriorityProcessing)
	if priority.Available == nil || !*priority.Available || priority.Availability != "Available" {
		t.Fatalf("Priority Processing should be available: %+v", priority)
	}
	ptum := optionByName(t, options, model.OfferPTUM)
	if ptum.Available == nil || !*ptum.Available || ptum.Availability != "Available" {
		t.Fatalf("PTU-M should be available: %+v", ptum)
	}
	if ptum.MinimumPTUs == nil || *ptum.MinimumPTUs != 15 {
		t.Fatalf("unexpected minimum PTUs: %v", ptum.MinimumPTUs)
	}
	if !ptum.RequiresLiveCapacityCheck ||
		!containsAll(ptum.Note, "current capacity 20 PTUs", "quota headroom 20 PTUs") {
		t.Fatalf("PTU-M note is missing live checks: %+v", ptum)
	}
	if client.capacityFormat != "OpenAI" ||
		client.capacityName != "gpt-5.6-luna" ||
		client.capacityVer != "2026-07-09" ||
		client.usageRegion != "East US 2" {
		t.Fatalf("offer queries were not scoped correctly: %+v", client)
	}
}

func TestGetOptionsReturnsConditionalWhenCapacityIsUnknown(t *testing.T) {
	t.Parallel()
	client := eligibleOfferClient()
	client.capacitiesErr = errors.New("capacity endpoint unavailable")
	provider := &Provider{
		subscriptionID: testSubscription,
		cognitive:      client,
	}

	options, err := provider.GetOptions(t.Context(), offerDeployment())
	if err != nil {
		t.Fatalf("capacity failure should produce conditional eligibility: %v", err)
	}
	ptum := optionByName(t, options, model.OfferPTUM)
	if ptum.Available != nil || ptum.Availability != "Conditional" {
		t.Fatalf("expected conditional PTU-M eligibility: %+v", ptum)
	}
	if !containsAll(ptum.Note, "capacity could not be established", "quota headroom 20 PTUs") {
		t.Fatalf("conditional note did not explain missing capacity: %q", ptum.Note)
	}
}

func TestGetOptionsReturnsConditionalWhenQuotaIsUnknown(t *testing.T) {
	t.Parallel()
	client := eligibleOfferClient()
	client.usagesErr = errors.New("quota endpoint unavailable")
	provider := &Provider{
		subscriptionID: testSubscription,
		cognitive:      client,
	}

	options, err := provider.GetOptions(t.Context(), offerDeployment())
	if err != nil {
		t.Fatalf("quota failure should produce conditional eligibility: %v", err)
	}
	ptum := optionByName(t, options, model.OfferPTUM)
	if ptum.Available != nil || ptum.Availability != "Conditional" {
		t.Fatalf("expected conditional PTU-M eligibility: %+v", ptum)
	}
	if !containsAll(ptum.Note, "current capacity 20 PTUs", "quota headroom could not be established") {
		t.Fatalf("conditional note did not explain missing quota: %q", ptum.Note)
	}
}

func TestGetOptionsReportsInsufficientQuota(t *testing.T) {
	t.Parallel()
	client := eligibleOfferClient()
	client.usages[0].Current = model.Float64(96)
	provider := &Provider{
		subscriptionID: testSubscription,
		cognitive:      client,
	}

	options, err := provider.GetOptions(t.Context(), offerDeployment())
	if err != nil {
		t.Fatalf("GetOptions returned an error: %v", err)
	}
	ptum := optionByName(t, options, model.OfferPTUM)
	if ptum.Available == nil || *ptum.Available || ptum.Availability != "Unavailable" {
		t.Fatalf("PTU-M should be unavailable with insufficient quota: %+v", ptum)
	}
}

func TestPTUMUsesDeploymentSpecificSKU(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"global_standard":    "GlobalProvisionedManaged",
		"data_zone_standard": "DataZoneProvisionedManaged",
		"regional_standard":  "ProvisionedManaged",
	}
	for deploymentType, expected := range tests {
		t.Run(deploymentType, func(t *testing.T) {
			t.Parallel()
			if actual := provisionedSKU(deploymentType); actual != expected {
				t.Fatalf("expected %q, got %q", expected, actual)
			}
		})
	}
}

func eligibleOfferClient() *fakeCognitiveClient {
	return &fakeCognitiveClient{
		models: []regionalModel{{
			Name:    "gpt-5.6-luna",
			Version: "2026-07-09",
			Format:  "OpenAI",
			Capabilities: map[string]string{
				"priorityTierSkus": "DataZoneStandard, GlobalStandard",
			},
			SKUs: []regionalSKU{
				{Name: "GlobalStandard"},
				{
					Name:      "GlobalProvisionedManaged",
					UsageName: "OpenAI.GlobalProvisionedManaged",
					Minimum:   model.Int(15),
				},
			},
		}},
		capacities: []modelCapacity{
			{
				Region:            "West US",
				SKUName:           "GlobalProvisionedManaged",
				AvailableCapacity: model.Float64(1000),
			},
			{
				Region:            "eastus2",
				SKUName:           "GlobalProvisionedManaged",
				AvailableCapacity: model.Float64(20),
			},
		},
		usages: []quotaUsage{{
			Name:    "OpenAI.GlobalProvisionedManaged",
			Current: model.Float64(80),
			Limit:   model.Float64(100),
		}},
	}
}

func offerDeployment() model.DeploymentContext {
	return model.DeploymentContext{
		SubscriptionID: testSubscription,
		Model:          "gpt-5.6-luna",
		ModelVersion:   "2026-07-09",
		SKUName:        "GlobalStandard",
		DeploymentType: "global_standard",
		Region:         "East US 2",
	}
}

func optionByName(t *testing.T, options []model.OfferOption, name string) model.OfferOption {
	t.Helper()
	for _, option := range options {
		if option.Offer == name {
			return option
		}
	}
	t.Fatalf("offer %q was not returned: %v", name, options)
	return model.OfferOption{}
}

func containsAll(value string, expected ...string) bool {
	for _, item := range expected {
		if !strings.Contains(value, item) {
			return false
		}
	}
	return true
}
