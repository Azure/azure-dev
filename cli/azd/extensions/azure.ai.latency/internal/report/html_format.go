// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package report

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"azure.ai.latency/internal/model"
)

type htmlOutcome struct {
	State   string
	Label   string
	Title   string
	Meaning string
	Icon    string
}

func htmlSemanticOutcome(result *model.AssessmentResult) htmlOutcome {
	assessment := result.SloAssessment
	if result.EvidencePackage != nil && result.BenchmarkComparison.Status == model.BenchmarkCoverageGap {
		return htmlOutcome{
			State: "danger",
			Label: "Target violation · coverage gap",
			Title: "Latency exceeds the target; benchmark coverage is unavailable",
			Meaning: "Observed " + htmlEscape(assessment.Metric) + " " + htmlEscape(assessment.Percentile) +
				" is <strong>" + htmlDuration(assessment.ActualMS) + "</strong> against a <strong>" +
				htmlDuration(assessment.TargetMS) + "</strong> current offer target. " +
				"No benchmark cohort covers this workload's strict dimensions.",
			Icon: "!",
		}
	}
	if result.EvidencePackage != nil {
		return htmlOutcome{
			State: "danger",
			Label: "Unexplained target violation",
			Title: "Latency exceeds the target and similar-workload range",
			Meaning: "Observed " + htmlEscape(assessment.Metric) + " " + htmlEscape(assessment.Percentile) +
				" is <strong>" + htmlDuration(assessment.ActualMS) + "</strong> against a <strong>" +
				htmlDuration(assessment.TargetMS) + "</strong> current offer target. " +
				"The available workload evidence does not explain the remaining gap.",
			Icon: "!",
		}
	}
	if assessment.Status == model.SloWithinTarget {
		return htmlOutcome{
			State: "within",
			Label: "Within current offer target",
			Title: "Latency is within the current offer target",
			Meaning: "Observed " + htmlEscape(assessment.Metric) + " " + htmlEscape(assessment.Percentile) +
				" is <strong>" + htmlDuration(assessment.ActualMS) + "</strong> against a <strong>" +
				htmlDuration(assessment.TargetMS) + "</strong> current offer target.",
			Icon: "✓",
		}
	}
	if assessment.Status == model.SloAboveTarget &&
		result.BenchmarkComparison.WorkloadProfileExplainsGap != nil &&
		*result.BenchmarkComparison.WorkloadProfileExplainsGap {
		return htmlOutcome{
			State: "info",
			Label: "Above target — workload explained",
			Title: "The workload profile explains the target difference",
			Meaning: "Observed " + htmlEscape(assessment.Metric) + " " + htmlEscape(assessment.Percentile) +
				" is <strong>" + htmlDuration(assessment.ActualMS) + "</strong> against a <strong>" +
				htmlDuration(assessment.TargetMS) +
				"</strong> target and is at or below the matched benchmark reference.",
			Icon: "i",
		}
	}
	return htmlOutcome{
		State:   "warning",
		Label:   "Assessment unavailable",
		Title:   "More request-level evidence is required",
		Meaning: htmlEscape(assessment.Message),
		Icon:    "?",
	}
}

func htmlNextAction(result *model.AssessmentResult) string {
	if result.EvidencePackage != nil {
		return "Escalate with the customer-safe evidence package."
	}
	for _, recommendation := range result.Recommendations {
		if recommendation.Category != "monitor" {
			return recommendation.RecommendedAction
		}
	}
	if !htmlHasFullRequestProfile(result.TrafficProfile) {
		return "Enable diagnostic logs, collect representative traffic, and rerun."
	}
	return "Continue monitoring " + result.SloAssessment.Metric + " " +
		result.SloAssessment.Percentile + " over representative traffic windows."
}

func htmlFallbackPattern(result *model.AssessmentResult) string {
	if result.SloAssessment.Status == model.SloWithinTarget {
		return "Current offer target is met"
	}
	if !htmlHasFullRequestProfile(result.TrafficProfile) {
		return "Request-level logs are unavailable"
	}
	return result.SloAssessment.Message
}

func htmlFallbackEvidence(result *model.AssessmentResult) string {
	profile := result.TrafficProfile
	return result.SloAssessment.Metric + " " + result.SloAssessment.Percentile + " is " +
		htmlDuration(result.SloAssessment.ActualMS) + "; errors are " +
		htmlPercent(profile.ErrorRate) + " and throttling is " + htmlPercent(profile.ThrottlingRate) + "."
}

func htmlHasFullRequestProfile(profile model.TrafficProfile) bool {
	ttft := htmlLatency(profile, "ttft")
	tbt := htmlLatency(profile, "tbt")
	ttlt := htmlLatency(profile, "ttlt")
	values := []*float64{
		profile.InputTokens.P50,
		profile.InputTokens.P95,
		profile.OutputTokens.P50,
		profile.OutputTokens.P95,
		ttft.P50MS,
		ttft.P95MS,
		tbt.P50MS,
		tbt.P95MS,
		ttlt.P50MS,
		ttlt.P95MS,
	}
	for _, value := range values {
		if value == nil {
			return false
		}
	}
	return true
}

func htmlLatency(profile model.TrafficProfile, metric string) model.LatencyDistribution {
	return profile.Latency[strings.ToLower(metric)]
}

func htmlBulletChart(p50, p95, target *float64, metric string) string {
	values := make([]float64, 0, 3)
	for _, value := range []*float64{p50, p95, target} {
		if value != nil {
			values = append(values, *value)
		}
	}
	if len(values) == 0 {
		return `<div class="chart-unavailable">Request-level ` + htmlEscape(metric) +
			` distribution unavailable</div>`
	}

	maximum := values[0]
	for _, value := range values[1:] {
		maximum = max(maximum, value)
	}
	maximum *= 1.25
	if maximum == 0 {
		maximum = 1
	}
	useSeconds := maximum >= 1000
	position := func(value *float64) float64 {
		if value == nil {
			return 0
		}
		return min(96, max(2, 100**value/maximum))
	}

	var markers strings.Builder
	if p50 != nil {
		fmt.Fprintf(
			&markers,
			`<i class="marker p50" style="left:%.1f%%"><span>P50 %s</span></i>`,
			position(p50),
			htmlMetricDuration(p50, useSeconds),
		)
	}
	if p95 != nil {
		fmt.Fprintf(
			&markers,
			`<i class="marker p95" style="left:%.1f%%"><span>P95 %s</span></i>`,
			position(p95),
			htmlMetricDuration(p95, useSeconds),
		)
	}
	if target != nil {
		fmt.Fprintf(
			&markers,
			`<i class="marker target" style="left:%.1f%%"><span>Target %s</span></i>`,
			position(target),
			htmlMetricDuration(target, useSeconds),
		)
	}
	aria := metric + " P50 " + htmlDuration(p50) + ", P95 " + htmlDuration(p95) +
		", current offer target " + htmlDuration(target)
	return `<div class="bullet" role="img" aria-label="` + htmlEscape(aria) +
		`"><div class="track"></div>` + markers.String() + `</div>`
}

func htmlTokenBand(value *float64, input bool) string {
	if value == nil {
		return ""
	}
	if input {
		switch {
		case *value < 2000:
			return "<2K"
		case *value < 8000:
			return "2-8K"
		case *value < 16000:
			return "8-16K"
		default:
			return ">=16K"
		}
	}
	if *value < 500 {
		return "<500"
	}
	return "≥500"
}

func htmlDisplayTokenBucket(value string) string {
	switch value {
	case "2-8K":
		return "2–8K"
	case "8-16K":
		return "8–16K"
	case ">=16K":
		return "≥16K"
	case ">=500":
		return "≥500"
	default:
		return value
	}
}

func htmlBurstFactor(rate model.RequestRate) *float64 {
	if rate.AverageRPM == nil || *rate.AverageRPM == 0 || rate.PeakRPM == nil {
		return nil
	}
	return new(*rate.PeakRPM / *rate.AverageRPM)
}

func htmlBurstLabel(value *float64) string {
	if value == nil {
		return "Burst unavailable"
	}
	return fmt.Sprintf("Peak %.1f× average", *value)
}

func htmlOfferingTypeLabel(value string) string {
	switch value {
	case "GlobalStandard":
		return "Global Standard"
	case "DataZoneStandard":
		return "Data Zone Standard"
	case "Standard":
		return "Regional Standard"
	case "GlobalProvisionedManaged":
		return "Global Provisioned"
	case "DataZoneProvisionedManaged":
		return "Data Zone Provisioned"
	case "ProvisionedManaged":
		return "Regional Provisioned"
	default:
		return value
	}
}

func htmlDeploymentTypeLabel(value string) string {
	switch value {
	case "global_standard":
		return "Global Standard"
	case "data_zone_standard":
		return "Data Zone Standard"
	case "regional_standard":
		return "Regional Standard"
	case "global_provisioned":
		return "Global Provisioned"
	case "data_zone_provisioned":
		return "Data Zone Provisioned"
	case "regional_provisioned":
		return "Regional Provisioned"
	case "global_batch":
		return "Global Batch"
	case "data_zone_batch":
		return "Data Zone Batch"
	case "developer":
		return "Developer"
	default:
		return unavailable
	}
}

func htmlOfferLabel(value string) string {
	switch value {
	case "PayGo", "Standard PayGo":
		return "PayGo"
	case model.OfferPriorityProcessing:
		return model.OfferPriorityProcessing
	case model.OfferPTUM:
		return model.OfferPTUM
	case "Batch":
		return "Batch"
	default:
		return unavailable
	}
}

func htmlIsPayGo(value string) bool {
	return value == "PayGo" || value == "Standard PayGo"
}

func htmlRegionLabel(value string) string {
	switch strings.ToLower(value) {
	case "eastus2":
		return "East US 2"
	case "swedencentral":
		return "Sweden Central"
	default:
		if value == "" {
			return unavailable
		}
		return value
	}
}

func htmlShortScenario(value string) string {
	switch value {
	case "within-target":
		return "Within target"
	case "workload-explained":
		return "Above target · workload explained"
	case "unexplained-gap":
		return "Above target · unexplained"
	case "logs-unavailable":
		return "Request logs unavailable"
	case "":
		return "Scenario"
	default:
		return value
	}
}

func htmlText(value string) string {
	if value == "" {
		return unavailable
	}
	return value
}

func htmlNumber(value *float64) string {
	if value == nil {
		return unavailable
	}
	return htmlNumberValue(*value)
}

func htmlNumberValue(value float64) string {
	switch {
	case math.IsNaN(value):
		return "nan"
	case math.IsInf(value, 1):
		return "inf"
	case math.IsInf(value, -1):
		return "-inf"
	}
	precision := 0
	if value != math.Trunc(value) {
		precision = 1
	}
	return htmlGroupNumber(strconv.FormatFloat(value, 'f', precision, 64))
}

func htmlInteger(value *int64) string {
	if value == nil {
		return unavailable
	}
	return htmlGroupNumber(strconv.FormatInt(*value, 10))
}

func htmlInt(value *int) string {
	if value == nil {
		return unavailable
	}
	return htmlGroupNumber(strconv.Itoa(*value))
}

func htmlGroupNumber(value string) string {
	sign := ""
	if strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		sign, value = value[:1], value[1:]
	}
	integer, fraction, found := strings.Cut(value, ".")
	if len(integer) > 3 {
		var grouped strings.Builder
		first := len(integer) % 3
		if first == 0 {
			first = 3
		}
		grouped.WriteString(integer[:first])
		for index := first; index < len(integer); index += 3 {
			grouped.WriteByte(',')
			grouped.WriteString(integer[index : index+3])
		}
		integer = grouped.String()
	}
	if found {
		return sign + integer + "." + fraction
	}
	return sign + integer
}

func htmlToken(value *float64) string {
	if value == nil {
		return unavailable
	}
	if *value >= 1000 {
		return fmt.Sprintf("%.1fK", *value/1000)
	}
	return htmlNumber(value)
}

func htmlPercent(value *float64) string {
	if value == nil {
		return unavailable
	}
	return fmt.Sprintf("%.1f%%", *value*100)
}

func htmlDuration(value *float64) string {
	if value == nil {
		return unavailable
	}
	if *value >= 1000 {
		return fmt.Sprintf("%.2fs", *value/1000)
	}
	return fmt.Sprintf("%.0fms", *value)
}

func htmlMetricDuration(value *float64, useSeconds bool) string {
	if value == nil {
		return unavailable
	}
	if useSeconds {
		return fmt.Sprintf("%.2fs", *value/1000)
	}
	return fmt.Sprintf("%.0fms", *value)
}

func htmlDataFreshness(result *model.AssessmentResult) string {
	generated, generatedErr := htmlParseTimestamp(result.GeneratedAt)
	end, endErr := htmlParseTimestamp(result.TimeRange.EndTime)
	if generatedErr != nil || endErr != nil {
		return "Data through " + result.TimeRange.EndTime
	}
	seconds := max(0, generated.Sub(end).Seconds())
	switch {
	case seconds < 60:
		return "Current through the selected window"
	case seconds < 3600:
		minutes := int64(math.RoundToEven(seconds / 60))
		return "Data is " + htmlGroupNumber(strconv.FormatInt(minutes, 10)) + " minutes old"
	case seconds < 86400:
		return fmt.Sprintf("Data is %.1f hours old", seconds/3600)
	default:
		return "Data through " + htmlDisplayTimestamp(result.TimeRange.EndTime)
	}
}

func htmlDisplayTimeRange(result *model.AssessmentResult) string {
	start, startErr := htmlParseTimestamp(result.TimeRange.StartTime)
	end, endErr := htmlParseTimestamp(result.TimeRange.EndTime)
	if startErr != nil || endErr != nil {
		return result.TimeRange.StartTime + " – " + result.TimeRange.EndTime
	}
	duration := htmlDisplayWindowDuration(max(end.Sub(start).Seconds(), 0))
	if start.Year() == end.Year() && start.YearDay() == end.YearDay() {
		return fmt.Sprintf(
			"%s %d, %d · %s–%s UTC · %s",
			start.Format("Jan"),
			start.Day(),
			start.Year(),
			start.Format("15:04"),
			end.Format("15:04"),
			duration,
		)
	}
	return fmt.Sprintf(
		"%s %d, %d, %s – %s %d, %d, %s UTC · %s",
		start.Format("Jan"),
		start.Day(),
		start.Year(),
		start.Format("15:04"),
		end.Format("Jan"),
		end.Day(),
		end.Year(),
		end.Format("15:04"),
		duration,
	)
}

func htmlDisplayWindowDuration(seconds float64) string {
	minutes := max(int64(math.RoundToEven(seconds/60)), 1)
	if minutes < 60 {
		return strconv.FormatInt(minutes, 10) + " min"
	}
	hours, remainingMinutes := minutes/60, minutes%60
	if hours < 24 {
		value := strconv.FormatInt(hours, 10) + " hr"
		if remainingMinutes != 0 {
			value += " " + strconv.FormatInt(remainingMinutes, 10) + " min"
		}
		return value
	}
	days, remainingHours := hours/24, hours%24
	value := strconv.FormatInt(days, 10) + " day"
	if days != 1 {
		value += "s"
	}
	if remainingHours != 0 {
		value += " " + strconv.FormatInt(remainingHours, 10) + " hr"
	}
	return value
}

func htmlParseTimestamp(value string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		timestamp, err := time.Parse(layout, value)
		if err == nil {
			return timestamp.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q", value)
}

func htmlDisplayTimestamp(value string) string {
	timestamp, err := htmlParseTimestamp(value)
	if err != nil {
		return value
	}
	return fmt.Sprintf(
		"%s %d, %d %s UTC",
		timestamp.Format("Jan"),
		timestamp.Day(),
		timestamp.Year(),
		timestamp.Format("15:04"),
	)
}

func htmlDisplayDate(value *string) string {
	if value == nil {
		return unavailable
	}
	timestamp, err := htmlParseTimestamp(*value)
	if err != nil {
		return *value
	}
	return fmt.Sprintf("%s %d, %d", timestamp.Format("Jan"), timestamp.Day(), timestamp.Year())
}

func htmlPythonTitle(value string) string {
	var result strings.Builder
	startWord := true
	for _, character := range value {
		if unicode.IsLetter(character) {
			if startWord {
				result.WriteRune(unicode.ToTitle(character))
			} else {
				result.WriteRune(unicode.ToLower(character))
			}
			startWord = false
		} else {
			result.WriteRune(character)
			startWord = true
		}
	}
	return result.String()
}

func htmlEscape(value string) string {
	var result strings.Builder
	for _, character := range value {
		switch character {
		case '&':
			result.WriteString("&amp;")
		case '<':
			result.WriteString("&lt;")
		case '>':
			result.WriteString("&gt;")
		case '"':
			result.WriteString("&quot;")
		case '\'':
			result.WriteString("&#x27;")
		default:
			result.WriteRune(character)
		}
	}
	return result.String()
}
