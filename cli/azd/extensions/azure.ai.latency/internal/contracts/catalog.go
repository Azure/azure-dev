// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package contracts

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"azure.ai.latency/internal/model"
)

//go:embed v1/*.json
var contractFiles embed.FS

// Catalog contains the product rules and reference data used by an assessment.
type Catalog struct {
	ProductRules     model.ProductRules
	SloTargets       []model.SloTarget
	BenchmarkCohorts []model.BenchmarkCohort
}

type productRulesDocument struct {
	SchemaVersion string `json:"schema_version"`
	Assessment    struct {
		Metric         string `json:"metric"`
		Percentile     string `json:"percentile"`
		TargetPoolType string `json:"target_pool_type"`
	} `json:"assessment"`
	Flow struct {
		BenchmarkTriggerStatuses []string `json:"benchmark_trigger_slo_statuses"`
		OfferTriggerStatuses     []string `json:"offer_trigger_slo_statuses"`
		OfferRequiredGoal        string   `json:"offer_required_goal"`
	} `json:"flow"`
	SampleRequirements struct {
		MinimumRequestSamples int64 `json:"minimum_request_samples"`
		MinimumMetricSamples  int64 `json:"minimum_metric_samples"`
	} `json:"sample_requirements"`
	BenchmarkBuckets struct {
		InputTokens    []model.NumericBucket `json:"input_tokens"`
		OutputTokens   []model.NumericBucket `json:"output_tokens"`
		StreamingRatio []model.NumericBucket `json:"streaming_ratio"`
		CacheRatio     []model.NumericBucket `json:"cache_ratio"`
	} `json:"benchmark_buckets"`
	WorkloadRules []model.RecommendationRule      `json:"workload_rules"`
	OfferRules    []model.OfferRecommendationRule `json:"offer_rules"`
	Outcomes      map[string]struct {
		ObservedPattern string `json:"observed_pattern"`
		Evidence        string `json:"evidence"`
		Action          string `json:"action"`
		Confidence      string `json:"confidence"`
	} `json:"outcomes"`
}

// Load reads and validates the bundled v1 contract catalog.
func Load() (*Catalog, error) {
	var rulesDocument productRulesDocument
	if err := readJSON("v1/product-rules.json", &rulesDocument); err != nil {
		return nil, err
	}

	var targets []model.SloTarget
	if err := readJSON("v1/sla-targets.json", &targets); err != nil {
		return nil, err
	}

	var cohorts []model.BenchmarkCohort
	if err := readJSON("v1/benchmark-cohorts.json", &cohorts); err != nil {
		return nil, err
	}

	rules := flattenRules(rulesDocument)
	if err := validate(rules, targets, cohorts); err != nil {
		return nil, fmt.Errorf("validate bundled latency contracts: %w", err)
	}

	slices.SortFunc(rules.WorkloadRules, compareRecommendationRules)
	slices.SortFunc(rules.OfferRules, compareOfferRules)

	return &Catalog{
		ProductRules:     rules,
		SloTargets:       targets,
		BenchmarkCohorts: cohorts,
	}, nil
}

func readJSON(path string, target any) error {
	content, err := contractFiles.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read bundled contract %s: %w", path, err)
	}
	if err := json.Unmarshal(content, target); err != nil {
		return fmt.Errorf("parse bundled contract %s: %w", path, err)
	}
	return nil
}

func flattenRules(document productRulesDocument) model.ProductRules {
	outcomes := make(map[string]model.OutcomeRule, len(document.Outcomes))
	for name, outcome := range document.Outcomes {
		category := "monitor"
		switch name {
		case "missing_request_data", "insufficient_metric_data", "insufficient_request_data", "target_unavailable":
			category = "data"
		case "benchmark_coverage_gap", "unexplained_gap":
			category = "escalation"
		}
		outcomes[name] = model.OutcomeRule{
			ObservedPattern: outcome.ObservedPattern,
			Evidence:        outcome.Evidence,
			Action:          outcome.Action,
			Confidence:      outcome.Confidence,
			Category:        category,
		}
	}

	return model.ProductRules{
		SchemaVersion:            document.SchemaVersion,
		AssessmentMetric:         document.Assessment.Metric,
		AssessmentPercentile:     document.Assessment.Percentile,
		TargetPoolType:           document.Assessment.TargetPoolType,
		MinimumRequestSamples:    document.SampleRequirements.MinimumRequestSamples,
		MinimumMetricSamples:     document.SampleRequirements.MinimumMetricSamples,
		BenchmarkTriggerStatuses: document.Flow.BenchmarkTriggerStatuses,
		OfferTriggerStatuses:     document.Flow.OfferTriggerStatuses,
		OfferRequiredGoal:        document.Flow.OfferRequiredGoal,
		InputTokenBuckets:        document.BenchmarkBuckets.InputTokens,
		OutputTokenBuckets:       document.BenchmarkBuckets.OutputTokens,
		StreamingRatioBuckets:    document.BenchmarkBuckets.StreamingRatio,
		CacheRatioBuckets:        document.BenchmarkBuckets.CacheRatio,
		WorkloadRules:            document.WorkloadRules,
		OfferRules:               document.OfferRules,
		Outcomes:                 outcomes,
	}
}

func validate(rules model.ProductRules, targets []model.SloTarget, cohorts []model.BenchmarkCohort) error {
	if rules.SchemaVersion != model.SchemaVersion {
		return fmt.Errorf("unsupported product rules schema version %q", rules.SchemaVersion)
	}
	if rules.AssessmentMetric == "" || rules.AssessmentPercentile == "" || rules.TargetPoolType == "" {
		return errors.New("assessment contract is incomplete")
	}
	if rules.MinimumRequestSamples <= 0 || rules.MinimumMetricSamples <= 0 {
		return errors.New("sample requirements must be positive")
	}
	if len(targets) == 0 || len(cohorts) == 0 {
		return errors.New("target and benchmark catalogs must not be empty")
	}
	if err := validateRules(rules.WorkloadRules, rules.OfferRules); err != nil {
		return err
	}
	requiredOutcomes := []string{
		"missing_request_data",
		"insufficient_metric_data",
		"insufficient_request_data",
		"target_unavailable",
		"benchmark_coverage_gap",
		"unexplained_gap",
		"above_target_explained",
		"within_target",
	}
	for _, name := range requiredOutcomes {
		if _, ok := rules.Outcomes[name]; !ok {
			return fmt.Errorf("required outcome %q is missing", name)
		}
	}
	return nil
}

func validateRules(
	workloadRules []model.RecommendationRule,
	offerRules []model.OfferRecommendationRule,
) error {
	ids := map[string]struct{}{}
	for _, rule := range workloadRules {
		if !validMetric(rule.Metric) || !validOperator(rule.Operator) {
			return fmt.Errorf("workload rule %q has an unsupported metric or operator", rule.ID)
		}
		if err := addRuleID(ids, rule.ID); err != nil {
			return err
		}
	}
	for _, rule := range offerRules {
		if !validMetric(rule.Metric) || !validOperator(rule.Operator) {
			return fmt.Errorf("offer rule %q has an unsupported metric or operator", rule.ID)
		}
		if err := addRuleID(ids, rule.ID); err != nil {
			return err
		}
	}
	return nil
}

func validMetric(metric string) bool {
	return slices.Contains([]string{
		"input_tokens_p95",
		"output_tokens_p95",
		"cache_hit_ratio",
		"request_burst_factor",
		"streaming_ratio",
	}, metric)
}

func validOperator(operator string) bool {
	return slices.Contains([]string{
		"greater_than",
		"greater_than_or_equal",
		"less_than",
		"less_than_or_equal",
	}, operator)
}

func addRuleID(ids map[string]struct{}, id string) error {
	if id == "" {
		return errors.New("rule ID must not be empty")
	}
	if _, ok := ids[id]; ok {
		return fmt.Errorf("duplicate rule ID %q", id)
	}
	ids[id] = struct{}{}
	return nil
}

func compareRecommendationRules(left, right model.RecommendationRule) int {
	if left.Priority != right.Priority {
		return left.Priority - right.Priority
	}
	switch {
	case left.ID < right.ID:
		return -1
	case left.ID > right.ID:
		return 1
	default:
		return 0
	}
}

func compareOfferRules(left, right model.OfferRecommendationRule) int {
	if left.Priority != right.Priority {
		return left.Priority - right.Priority
	}
	switch {
	case left.ID < right.ID:
		return -1
	case left.ID > right.ID:
		return 1
	default:
		return 0
	}
}
