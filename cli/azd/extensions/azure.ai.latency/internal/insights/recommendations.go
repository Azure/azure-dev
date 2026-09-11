// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package insights

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"azure.ai.latency/internal/model"
)

func recommend(
	profile model.TrafficProfile,
	slo model.SloAssessment,
	benchmark model.BenchmarkComparison,
	rules model.ProductRules,
) []model.Recommendation {
	if !profile.HasFullRequestProfile(rules.AssessmentMetric) {
		return []model.Recommendation{outcomeRecommendation("missing_request_data", profile, benchmark, rules)}
	}
	metricSamples := profile.MetricSampleCount(rules.AssessmentMetric)
	if metricSamples == nil || *metricSamples < rules.MinimumMetricSamples {
		return []model.Recommendation{outcomeRecommendation("insufficient_metric_data", profile, benchmark, rules)}
	}
	if profile.RequestCount == nil || *profile.RequestCount < rules.MinimumRequestSamples {
		return []model.Recommendation{outcomeRecommendation("insufficient_request_data", profile, benchmark, rules)}
	}
	if slo.Status == model.SloUnavailable {
		return []model.Recommendation{outcomeRecommendation("target_unavailable", profile, benchmark, rules)}
	}

	workload := evaluateWorkloadRules(profile, rules)
	switch {
	case slo.Status == model.SloAboveTarget && benchmark.Status == model.BenchmarkCoverageGap:
		return append([]model.Recommendation{
			outcomeRecommendation("benchmark_coverage_gap", profile, benchmark, rules),
		}, workload...)
	case slo.Status == model.SloAboveTarget && benchmark.WorkloadProfileExplainsGap != nil &&
		!*benchmark.WorkloadProfileExplainsGap:
		return append([]model.Recommendation{
			outcomeRecommendation("unexplained_gap", profile, benchmark, rules),
		}, workload...)
	case len(workload) > 0:
		return workload
	case slo.Status == model.SloWithinTarget:
		return []model.Recommendation{outcomeRecommendation("within_target", profile, benchmark, rules)}
	case slo.Status == model.SloAboveTarget:
		return []model.Recommendation{outcomeRecommendation("above_target_explained", profile, benchmark, rules)}
	default:
		return []model.Recommendation{}
	}
}

func evaluateWorkloadRules(
	profile model.TrafficProfile,
	rules model.ProductRules,
) []model.Recommendation {
	recommendations := make([]model.Recommendation, 0)
	for _, rule := range rules.WorkloadRules {
		value, ok := metricValue(profile, rule.Metric)
		if !rule.Enabled || !ok || !compare(value, rule.Operator, rule.Threshold) {
			continue
		}
		evidence := replaceTemplate(rule.Evidence, map[string]string{
			"value":       formatRuleValue(rule.Metric, value),
			"threshold":   formatRuleValue(rule.Metric, rule.Threshold),
			"average_rpm": formatOptional(profile.RequestRate.AverageRPM),
			"peak_rpm":    formatOptional(profile.RequestRate.PeakRPM),
		})
		ruleID := rule.ID
		recommendations = append(recommendations, model.Recommendation{
			ObservedPattern:    rule.ObservedPattern,
			SupportingEvidence: evidence,
			RecommendedAction:  rule.Action,
			Confidence:         rule.Confidence,
			Category:           "workload",
			Interpretation:     model.RecommendationInterpretation,
			RuleID:             &ruleID,
		})
	}
	return recommendations
}

func outcomeRecommendation(
	name string,
	profile model.TrafficProfile,
	benchmark model.BenchmarkComparison,
	rules model.ProductRules,
) model.Recommendation {
	outcome := rules.Outcomes[name]
	requestCount := int64(0)
	if profile.RequestCount != nil {
		requestCount = *profile.RequestCount
	}
	metricSampleCount := int64(0)
	if count := profile.MetricSampleCount(rules.AssessmentMetric); count != nil {
		metricSampleCount = *count
	}
	evidence := replaceTemplate(outcome.Evidence, map[string]string{
		"request_count":           formatCount(requestCount),
		"metric_sample_count":     formatCount(metricSampleCount),
		"minimum_request_samples": strconv.FormatInt(rules.MinimumRequestSamples, 10),
		"minimum_metric_samples":  strconv.FormatInt(rules.MinimumMetricSamples, 10),
		"benchmark_message":       benchmark.Message,
		"metric":                  strings.ToUpper(rules.AssessmentMetric),
		"percentile":              strings.ToUpper(rules.AssessmentPercentile),
	})
	return model.Recommendation{
		ObservedPattern:    outcome.ObservedPattern,
		SupportingEvidence: evidence,
		RecommendedAction:  outcome.Action,
		Confidence:         outcome.Confidence,
		Category:           outcome.Category,
		Interpretation:     model.RecommendationInterpretation,
	}
}

func metricValue(profile model.TrafficProfile, metric string) (float64, bool) {
	switch metric {
	case "input_tokens_p95":
		return pointerValue(profile.InputTokens.P95)
	case "output_tokens_p95":
		return pointerValue(profile.OutputTokens.P95)
	case "cache_hit_ratio":
		return pointerValue(profile.CacheHitRatio)
	case "request_burst_factor":
		if profile.RequestRate.BurstFactor != nil {
			return *profile.RequestRate.BurstFactor, true
		}
		if profile.RequestRate.AverageRPM == nil || profile.RequestRate.PeakRPM == nil ||
			*profile.RequestRate.AverageRPM <= 0 {
			return 0, false
		}
		return *profile.RequestRate.PeakRPM / *profile.RequestRate.AverageRPM, true
	case "streaming_ratio":
		return pointerValue(profile.StreamingRatio)
	default:
		return 0, false
	}
}

func pointerValue(value *float64) (float64, bool) {
	if value == nil {
		return 0, false
	}
	return *value, true
}

func compare(value float64, operator string, threshold float64) bool {
	switch operator {
	case "greater_than":
		return value > threshold
	case "greater_than_or_equal":
		return value >= threshold
	case "less_than":
		return value < threshold
	case "less_than_or_equal":
		return value <= threshold
	default:
		return false
	}
}

func replaceTemplate(template string, values map[string]string) string {
	result := template
	for key, value := range values {
		result = strings.ReplaceAll(result, "{"+key+"}", value)
	}
	return result
}

func formatRuleValue(metric string, value float64) string {
	if strings.Contains(metric, "ratio") {
		return fmt.Sprintf("%.1f%%", value*100)
	}
	if metric == "request_burst_factor" {
		return strconv.FormatFloat(value, 'f', 1, 64)
	}
	return formatEvidenceNumber(value)
}

func formatOptional(value *float64) string {
	if value == nil {
		return "unavailable"
	}
	return formatCount(int64(math.Round(*value)))
}

func formatEvidenceNumber(value float64) string {
	rounded := math.Round(value)
	if math.Abs(value-rounded) < 1e-9 {
		return formatCount(int64(rounded))
	}
	whole, fraction, _ := strings.Cut(strconv.FormatFloat(value, 'f', 1, 64), ".")
	return formatCountString(whole) + "." + fraction
}

func formatCountString(value string) string {
	sign := ""
	if strings.HasPrefix(value, "-") {
		sign = "-"
		value = strings.TrimPrefix(value, "-")
	}
	for i := len(value) - 3; i > 0; i -= 3 {
		value = value[:i] + "," + value[i:]
	}
	return sign + value
}
