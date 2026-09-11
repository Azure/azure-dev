// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package insights

import (
	"testing"

	"azure.ai.latency/internal/contracts"
	"azure.ai.latency/internal/model"
)

func TestRecommendationEvidenceFormattingMatchesReference(t *testing.T) {
	tests := []struct {
		name     string
		metric   string
		value    float64
		expected string
	}{
		{name: "ratio", metric: "cache_hit_ratio", value: 0.25, expected: "25.0%"},
		{name: "tokens", metric: "output_tokens_p95", value: 1200, expected: "1,200"},
		{name: "burst", metric: "request_burst_factor", value: 3.46, expected: "3.5"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := formatRuleValue(test.metric, test.value); actual != test.expected {
				t.Fatalf("formatRuleValue() = %q, want %q", actual, test.expected)
			}
		})
	}

	if actual := formatOptional(model.Float64(5.6923076923076925)); actual != "6" {
		t.Fatalf("formatOptional() = %q, want 6", actual)
	}
}

func TestEnrichOfferOptionsUsesReferenceAvailabilityLabel(t *testing.T) {
	catalog, err := contracts.Load()
	if err != nil {
		t.Fatal(err)
	}
	profile := model.TrafficProfile{
		RequestRate: model.RequestRate{
			AverageRPM:  model.Float64(6),
			PeakRPM:     model.Float64(20),
			BurstFactor: model.Float64(3.5),
		},
	}
	options := []model.OfferOption{
		{
			Offer:        model.OfferPriorityProcessing,
			Available:    model.Bool(true),
			Availability: "Available",
		},
		{
			Offer:        model.OfferPTUM,
			Available:    model.Bool(true),
			Availability: "Available",
		},
	}

	enriched := enrichOfferOptions(profile, options, catalog.ProductRules)
	for _, option := range enriched {
		if option.Availability != "Available now" {
			t.Fatalf("%s availability = %q, want Available now", option.Offer, option.Availability)
		}
	}
}
