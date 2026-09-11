// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package contracts

import "testing"

func TestLoadCurrentCatalog(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(catalog.BenchmarkCohorts), 32; got != want {
		t.Fatalf("benchmark cohort count = %d, want %d", got, want)
	}
	if got, want := len(catalog.SloTargets), 36; got != want {
		t.Fatalf("SLO target count = %d, want %d", got, want)
	}
	if got := catalog.ProductRules.MinimumRequestSamples; got != 20 {
		t.Fatalf("minimum request samples = %d, want 20", got)
	}

	var benchmarkRow, targetRow bool
	for _, cohort := range catalog.BenchmarkCohorts {
		if cohort.Model == "gpt-4.1-mini" &&
			cohort.ModelVersion == "2025-04-14" &&
			cohort.OfferingType == "GlobalStandard" &&
			cohort.InputTokenBucket == "2-8K" &&
			cohort.OutputTokenBucket == "<500" &&
			cohort.Metric == "TBT_P95" {
			benchmarkRow = cohort.Value > 0
		}
	}
	for _, target := range catalog.SloTargets {
		if target.Model == "gpt-4.1-mini" &&
			target.ModelVersion == "2025-04-14" &&
			target.OfferingType == "Standard" &&
			target.PoolType == "cold" &&
			target.Target == "SLA_P95_TBT" {
			targetRow = target.Value > 0
		}
	}
	if !benchmarkRow || !targetRow {
		t.Fatal("expected representative benchmark and target contract rows")
	}
}

func TestLoadSortsRulesByPriority(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	rules := catalog.ProductRules.WorkloadRules
	for i := 1; i < len(rules); i++ {
		if rules[i-1].Priority > rules[i].Priority {
			t.Fatalf("rules are not sorted: %s appears before %s", rules[i-1].ID, rules[i].ID)
		}
	}
}
