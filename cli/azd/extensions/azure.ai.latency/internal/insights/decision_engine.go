// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package insights

import (
	"fmt"
	"slices"
	"strings"

	"azure.ai.latency/internal/model"
)

func assessSLO(
	profile model.TrafficProfile,
	target *model.SloTarget,
	rules model.ProductRules,
) model.SloAssessment {
	metric := strings.ToUpper(rules.AssessmentMetric)
	percentile := strings.ToUpper(rules.AssessmentPercentile)
	assessment := model.SloAssessment{
		Metric:            metric,
		Percentile:        percentile,
		SourceLabel:       "Current-offer target catalog",
		ContractualNotice: model.ContractualNotice,
	}

	if target == nil {
		assessment.Status = model.SloUnavailable
		assessment.Message = "No current-offer target is available for this exact model, version, " +
			"offering type, metric, and percentile."
		return assessment
	}
	assessment.TargetMS = model.Float64(target.Value)

	latency := profile.Latency[strings.ToLower(metric)]
	actual := latency.P95MS
	if actual == nil {
		assessment.Status = model.SloUnavailable
		assessment.Message = fmt.Sprintf(
			"The current-offer target was found, but observed %s %s is unavailable because "+
				"request-level logs are unavailable.",
			metric,
			percentile,
		)
		return assessment
	}
	assessment.ActualMS = actual

	metricSamples := profile.MetricSampleCount(metric)
	if metricSamples == nil || *metricSamples < rules.MinimumMetricSamples {
		count := int64(0)
		if metricSamples != nil {
			count = *metricSamples
		}
		assessment.Status = model.SloInsufficient
		assessment.Message = fmt.Sprintf(
			"Observed %s %s is based on only %s usable request-level samples; at least %d are required "+
				"for an assessment.",
			metric,
			percentile,
			formatCount(count),
			rules.MinimumMetricSamples,
		)
		return assessment
	}
	if profile.RequestCount == nil || *profile.RequestCount < rules.MinimumRequestSamples {
		count := int64(0)
		if profile.RequestCount != nil {
			count = *profile.RequestCount
		}
		assessment.Status = model.SloInsufficient
		assessment.Message = fmt.Sprintf(
			"The selected window contains only %s request-level samples; at least %d are required "+
				"for an assessment.",
			formatCount(count),
			rules.MinimumRequestSamples,
		)
		return assessment
	}

	if *actual <= target.Value {
		assessment.Status = model.SloWithinTarget
		assessment.Message = "Within current offer target"
	} else {
		assessment.Status = model.SloAboveTarget
		assessment.Message = "Above current offer target"
	}
	return assessment
}

func hasUnexplainedTargetViolation(
	slo model.SloAssessment,
	benchmark model.BenchmarkComparison,
) bool {
	if slo.Status != model.SloAboveTarget {
		return false
	}
	return benchmark.Status == model.BenchmarkCoverageGap ||
		(benchmark.WorkloadProfileExplainsGap != nil && !*benchmark.WorkloadProfileExplainsGap)
}

func buildOfferGuidance(
	profile model.TrafficProfile,
	slo model.SloAssessment,
	recommendations []model.Recommendation,
	options []model.OfferOption,
	userGoal string,
	rules model.ProductRules,
	unexplained bool,
) model.OfferGuidance {
	const notice = "Offer guidance is directional. Verify current regional availability, quota, capacity, and pricing."
	if unexplained {
		return model.OfferGuidance{
			Reason:  "A paid offer change is not used to explain this unresolved target miss.",
			Notice:  notice,
			Options: []model.OfferOption{},
		}
	}
	if !slices.Contains(rules.OfferTriggerStatuses, slo.Status) || userGoal != rules.OfferRequiredGoal {
		return model.OfferGuidance{
			Reason:  "Offer guidance was not requested for this assessment outcome.",
			Notice:  notice,
			Options: []model.OfferOption{},
		}
	}
	if profile.RequestCount == nil || *profile.RequestCount < rules.MinimumRequestSamples {
		return model.OfferGuidance{
			Shown:    true,
			Headline: "Collect representative traffic before evaluating another offer.",
			Reason:   "The current sample is too small for offer-fit classification.",
			Notice:   notice,
			Options:  []model.OfferOption{},
		}
	}
	for _, recommendation := range recommendations {
		if recommendation.Category == "workload" {
			return model.OfferGuidance{
				Reason:  "Test the workload recommendation before comparing paid offer options.",
				Notice:  notice,
				Options: []model.OfferOption{},
			}
		}
	}

	confirmed := make([]model.OfferOption, 0, len(options))
	for _, option := range options {
		if option.Recommended && option.Available != nil && *option.Available {
			confirmed = append(confirmed, option)
		}
	}
	if len(confirmed) == 0 {
		return model.OfferGuidance{
			Shown:    true,
			Headline: "No eligible lower-latency offer is confirmed for this workload.",
			Reason:   "Verify live capacity and quota before changing the deployment.",
			Notice:   notice,
			Options:  []model.OfferOption{},
		}
	}
	return model.OfferGuidance{
		Shown:    true,
		Headline: "Explore an eligible offer with the same representative workload.",
		Reason:   "The current target is met and a lower or more predictable latency goal was requested.",
		Options:  confirmed,
		Notice:   notice,
	}
}

func enrichOfferOptions(
	profile model.TrafficProfile,
	options []model.OfferOption,
	rules model.ProductRules,
) []model.OfferOption {
	enriched := make([]model.OfferOption, 0, len(options))
	for _, option := range options {
		if option.Available != nil && *option.Available {
			option.Availability = "Available now"
		}
		for _, rule := range rules.OfferRules {
			if !rule.Enabled || !strings.EqualFold(option.Offer, rule.Offer) {
				continue
			}
			value, ok := metricValue(profile, rule.Metric)
			fit := rule.Fit
			tradeoff := rule.Tradeoff
			nextTest := rule.Action
			ruleID := rule.ID
			option.FitReason = &fit
			option.Tradeoff = &tradeoff
			option.NextTest = &nextTest
			option.RuleID = &ruleID
			option.Recommended = ok && compare(value, rule.Operator, rule.Threshold)
			break
		}
		enriched = append(enriched, option)
	}
	return enriched
}

func formatCount(value int64) string {
	text := fmt.Sprintf("%d", value)
	for i := len(text) - 3; i > 0; i -= 3 {
		text = text[:i] + "," + text[i:]
	}
	return text
}
