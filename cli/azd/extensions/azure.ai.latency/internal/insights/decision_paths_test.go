// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package insights

import (
	"encoding/json"
	"strings"
	"testing"

	"azure.ai.latency/internal/contracts"
	"azure.ai.latency/internal/model"
)

func TestDemoDecisionPaths(t *testing.T) {
	catalog, err := contracts.Load()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		scenario        string
		slo             string
		benchmark       string
		comparison      string
		recommendations int
		options         int
		offerShown      bool
		evidence        bool
	}{
		{
			scenario:        "within-target",
			slo:             model.SloWithinTarget,
			benchmark:       model.BenchmarkNotRequired,
			recommendations: 1,
			options:         2,
		},
		{
			scenario:        "lower-latency-needed",
			slo:             model.SloWithinTarget,
			benchmark:       model.BenchmarkNotRequired,
			recommendations: 1,
			options:         2,
			offerShown:      true,
		},
		{
			scenario:        "workload-explained",
			slo:             model.SloAboveTarget,
			benchmark:       model.BenchmarkMatched,
			comparison:      model.ComparisonAtOrBelow,
			recommendations: 3,
			options:         2,
		},
		{
			scenario:        "unexplained-gap",
			slo:             model.SloAboveTarget,
			benchmark:       model.BenchmarkMatched,
			comparison:      model.ComparisonAbove,
			recommendations: 1,
			evidence:        true,
		},
		{
			scenario:        "no-benchmark-coverage",
			slo:             model.SloAboveTarget,
			benchmark:       model.BenchmarkCoverageGap,
			recommendations: 2,
			evidence:        true,
		},
		{
			scenario:        "logs-unavailable",
			slo:             model.SloUnavailable,
			benchmark:       model.BenchmarkNotRun,
			recommendations: 1,
			options:         2,
		},
	}

	for _, test := range tests {
		t.Run(test.scenario, func(t *testing.T) {
			scenario, ok := FindDemoScenario(test.scenario)
			if !ok {
				t.Fatalf("scenario %q not found", test.scenario)
			}
			result, err := AssessDemo(t.Context(), catalog, scenario)
			if err != nil {
				t.Fatal(err)
			}
			if result.SloAssessment.Status != test.slo {
				t.Fatalf("SLO status = %q, want %q", result.SloAssessment.Status, test.slo)
			}
			if result.BenchmarkComparison.Status != test.benchmark {
				t.Fatalf("benchmark status = %q, want %q", result.BenchmarkComparison.Status, test.benchmark)
			}
			if test.comparison != "" {
				if result.BenchmarkComparison.Comparison == nil ||
					*result.BenchmarkComparison.Comparison != test.comparison {
					t.Fatalf("comparison = %v, want %q", result.BenchmarkComparison.Comparison, test.comparison)
				}
			}
			if len(result.Recommendations) != test.recommendations {
				t.Fatalf("recommendations = %d, want %d", len(result.Recommendations), test.recommendations)
			}
			if len(result.EligibleOfferOptions) != test.options {
				t.Fatalf("eligible options = %d, want %d", len(result.EligibleOfferOptions), test.options)
			}
			if result.OfferGuidance.Shown != test.offerShown {
				t.Fatalf("offer shown = %t, want %t", result.OfferGuidance.Shown, test.offerShown)
			}
			if (result.EvidencePackage != nil) != test.evidence {
				t.Fatalf("evidence present = %t, want %t", result.EvidencePackage != nil, test.evidence)
			}
		})
	}
}

func TestLowerLatencyScenarioRecommendsOnlyPTUM(t *testing.T) {
	catalog, err := contracts.Load()
	if err != nil {
		t.Fatal(err)
	}
	scenario, _ := FindDemoScenario("lower-latency-needed")
	result, err := AssessDemo(t.Context(), catalog, scenario)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result.OfferGuidance.Options); got != 1 {
		t.Fatalf("guided options = %d, want 1", got)
	}
	if result.OfferGuidance.Options[0].Offer != model.OfferPTUM {
		t.Fatalf("guided offer = %q, want %q", result.OfferGuidance.Options[0].Offer, model.OfferPTUM)
	}
}

func TestAssessmentJSONDoesNotExposeProhibitedFields(t *testing.T) {
	catalog, err := contracts.Load()
	if err != nil {
		t.Fatal(err)
	}
	scenario, _ := FindDemoScenario("unexplained-gap")
	result, err := AssessDemo(t.Context(), catalog, scenario)
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, prohibited := range []string{
		"cold_pool",
		"hot_pool",
		"pool_status",
		"pool_type",
		"raw_prompt",
		"internal_endpoint",
		"kusto_query",
		"credentials",
		"secrets",
	} {
		if strings.Contains(text, `"`+prohibited+`":`) {
			t.Fatalf("serialized result contains prohibited field %q", prohibited)
		}
	}
	if result.DataNotice != model.DataNoticeDemo {
		t.Fatalf("data notice = %q, want %q", result.DataNotice, model.DataNoticeDemo)
	}
}
