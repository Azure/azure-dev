// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package report

import (
	"fmt"
	"strconv"
	"strings"

	"azure.ai.latency/internal/model"
)

const unavailable = "Unavailable"

func assessmentLabel(result *model.AssessmentResult) string {
	switch result.SloAssessment.Status {
	case model.SloWithinTarget:
		return "Within current offer target"
	case model.SloAboveTarget:
		switch {
		case result.BenchmarkComparison.Status == model.BenchmarkCoverageGap:
			return "Target violation · coverage gap"
		case result.BenchmarkComparison.WorkloadProfileExplainsGap != nil &&
			*result.BenchmarkComparison.WorkloadProfileExplainsGap:
			return "Above target — workload explained"
		default:
			return "Unexplained target violation"
		}
	case model.SloInsufficient:
		return "Insufficient data"
	case model.SloUnavailable:
		return "Current offer target unavailable"
	default:
		if value := strings.TrimSpace(result.SloAssessment.Message); value != "" {
			return value
		}
		return unavailable
	}
}

func benchmarkLabel(comparison model.BenchmarkComparison) string {
	switch {
	case comparison.Status == model.BenchmarkCoverageGap:
		return "Coverage gap"
	case comparison.Status == model.BenchmarkMatched &&
		comparison.WorkloadProfileExplainsGap != nil &&
		*comparison.WorkloadProfileExplainsGap:
		return "Similar workload explains the difference"
	case comparison.Status == model.BenchmarkMatched:
		return "Observed latency remains above the similar workload"
	case comparison.Status == model.BenchmarkNotRun:
		return "Not run"
	case comparison.Status == model.BenchmarkNotRequired:
		return "Not required"
	default:
		return displayOr(comparison.Status, unavailable)
	}
}

func targetComparisonTitle(assessment model.SloAssessment) string {
	metric := displayOr(strings.ToUpper(assessment.Metric), "TBT")
	percentile := displayOr(strings.ToUpper(assessment.Percentile), "P95")
	return fmt.Sprintf("Observed %s %s versus current offer target", metric, percentile)
}

func formatScenario(result *model.AssessmentResult) string {
	title := pointerText(result.ScenarioTitle)
	name := pointerText(result.Scenario)
	switch {
	case title != "" && name != "" && !strings.EqualFold(title, name):
		return fmt.Sprintf("%s (%s)", title, name)
	case title != "":
		return title
	default:
		return name
	}
}

func formatModel(deployment model.DeploymentContext) string {
	name := displayOr(deployment.Model, unavailable)
	if version := strings.TrimSpace(deployment.ModelVersion); version != "" {
		return fmt.Sprintf("%s (%s)", name, version)
	}
	return name
}

func formatDeployment(deployment model.DeploymentContext) string {
	if name := strings.TrimSpace(deployment.DeploymentName); name != "" {
		return name
	}
	if id := strings.TrimSpace(deployment.DeploymentID); id != "" {
		return id
	}
	return unavailable
}

func formatOffer(deployment model.DeploymentContext) string {
	offer := strings.TrimSpace(deployment.Offer)
	sku := strings.TrimSpace(deployment.SKUName)
	switch {
	case offer != "" && sku != "" && !strings.EqualFold(offer, sku):
		return fmt.Sprintf("%s (%s)", offer, sku)
	case offer != "":
		return offer
	case sku != "":
		return sku
	case strings.TrimSpace(deployment.BillingModel) != "":
		return strings.TrimSpace(deployment.BillingModel)
	default:
		return unavailable
	}
}

func formatTimeRange(window model.TimeWindow) string {
	label := strings.TrimSpace(window.Label)
	start := strings.TrimSpace(window.StartTime)
	end := strings.TrimSpace(window.EndTime)
	switch {
	case start != "" && end != "" && label != "":
		return fmt.Sprintf("%s · %s – %s", label, start, end)
	case start != "" && end != "":
		return fmt.Sprintf("%s – %s", start, end)
	case label != "":
		return label
	case start != "":
		return start
	case end != "":
		return end
	default:
		return unavailable
	}
}

func formatMilliseconds(value *float64) string {
	if value == nil {
		return unavailable
	}
	return formatNumber(*value) + " ms"
}

func formatNumberPointer(value *float64) string {
	if value == nil {
		return unavailable
	}
	return formatNumber(*value)
}

func formatPercent(value *float64) string {
	if value == nil {
		return unavailable
	}
	return formatNumber(*value*100) + "%"
}

func formatCount(value *int64) string {
	if value == nil {
		return unavailable
	}
	return strconv.FormatInt(*value, 10)
}

func formatRate(average, peak *float64, unit string) string {
	switch {
	case average != nil && peak != nil:
		return fmt.Sprintf("%s average · %s peak %s", formatNumber(*average), formatNumber(*peak), unit)
	case average != nil:
		return fmt.Sprintf("%s average %s", formatNumber(*average), unit)
	case peak != nil:
		return fmt.Sprintf("%s peak %s", formatNumber(*peak), unit)
	default:
		return unavailable
	}
}

func formatDistribution(distribution model.Distribution) string {
	p50 := formatNumberPointer(distribution.P50)
	p95 := formatNumberPointer(distribution.P95)
	if p50 == unavailable && p95 == unavailable {
		return unavailable
	}
	unit := strings.TrimSpace(distribution.Unit)
	value := fmt.Sprintf("P50 %s · P95 %s", p50, p95)
	if unit != "" {
		value += " " + unit
	}
	return value
}

func formatLatency(distribution model.LatencyDistribution) string {
	if distribution.P50MS == nil && distribution.P90MS == nil && distribution.P95MS == nil {
		return unavailable
	}
	values := make([]string, 0, 3)
	if distribution.P50MS != nil {
		values = append(values, "P50 "+formatCompactMilliseconds(distribution.P50MS))
	}
	if distribution.P90MS != nil {
		values = append(values, "P90 "+formatCompactMilliseconds(distribution.P90MS))
	}
	if distribution.P95MS != nil {
		values = append(values, "P95 "+formatCompactMilliseconds(distribution.P95MS))
	}
	return strings.Join(values, " · ")
}

func formatCompactMilliseconds(value *float64) string {
	if value == nil {
		return unavailable
	}
	if *value >= 1000 {
		return strconv.FormatFloat(*value/1000, 'f', 2, 64) + "s"
	}
	return strconv.FormatFloat(*value, 'f', 0, 64) + "ms"
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func displayOr(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}

func pointerText(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func isBasicProfile(result *model.AssessmentResult) bool {
	metric := displayOr(result.SloAssessment.Metric, "TBT")
	return !result.TrafficProfile.HasFullRequestProfile(metric)
}

func shouldShowBenchmark(result *model.AssessmentResult) bool {
	return result.SloAssessment.Status != model.SloWithinTarget
}

func offerOptions(result *model.AssessmentResult) []model.OfferOption {
	if result.EvidencePackage != nil {
		return nil
	}
	if len(result.OfferGuidance.Options) > 0 {
		return result.OfferGuidance.Options
	}
	return result.EligibleOfferOptions
}

func illustrativeNotice(result *model.AssessmentResult) string {
	if notice := strings.TrimSpace(result.DataNotice); notice != "" {
		return notice
	}
	if result.Mode == model.ModeDemo || len(result.IllustrativeComponents) > 0 {
		return model.DataNoticeDemo
	}
	return ""
}
