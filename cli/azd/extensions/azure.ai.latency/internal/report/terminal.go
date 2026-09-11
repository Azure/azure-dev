// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package report

import (
	"fmt"
	"io"
	"strings"

	"azure.ai.latency/internal/model"
)

// RenderTerminal writes a human-readable assessment to writer.
func RenderTerminal(writer io.Writer, result *model.AssessmentResult) error {
	if writer == nil {
		return fmt.Errorf("terminal report writer is required")
	}
	if result == nil {
		return fmt.Errorf("assessment result is required")
	}

	var output strings.Builder
	writeTerminalHeader(&output, result)
	writeTerminalTraffic(&output, result)
	writeTerminalTarget(&output, result)
	if shouldShowBenchmark(result) {
		writeTerminalBenchmark(&output, result)
	}
	writeTerminalRecommendations(&output, result)
	if result.EvidencePackage != nil {
		writeTerminalEvidence(&output, result.EvidencePackage)
	}
	if options := offerOptions(result); len(options) > 0 {
		writeTerminalOffers(&output, result, options)
	}
	if illustrativeNotice(result) != "" {
		output.WriteString("Note: illustrative sample data.\n")
	}

	if _, err := io.WriteString(writer, output.String()); err != nil {
		return fmt.Errorf("write terminal report: %w", err)
	}
	return nil
}

func writeTerminalHeader(output *strings.Builder, result *model.AssessmentResult) {
	output.WriteString("MODEL LATENCY SELF-SERVICE TOOL\n")
	if scenario := formatScenario(result); scenario != "" {
		fmt.Fprintf(output, "Scenario: %s\n", scenario)
	}

	deployment := result.TrafficProfile.Deployment
	fmt.Fprintf(output, "Deployment: %s\n", formatDeployment(deployment))
	fmt.Fprintf(output, "Model: %s\n", formatModel(deployment))
	fmt.Fprintf(output, "Offer: %s\n", formatOffer(deployment))
	fmt.Fprintf(output, "Region: %s\n", displayOr(deployment.Region, unavailable))
	fmt.Fprintf(output, "Time range: %s\n\n", formatTimeRange(result.TimeRange))
}

func writeTerminalTraffic(output *strings.Builder, result *model.AssessmentResult) {
	profile := result.TrafficProfile
	output.WriteString("1. Traffic profile\n")
	if isBasicProfile(result) {
		output.WriteString("  Basic profile\n")
	} else {
		output.WriteString("  Full request profile\n")
	}
	fmt.Fprintf(
		output,
		"  Requests: %s\n",
		formatRate(profile.RequestRate.AverageRPM, profile.RequestRate.PeakRPM, "requests/min"),
	)
	fmt.Fprintf(output, "  Request count: %s\n", formatCount(profile.RequestCount))
	fmt.Fprintf(output, "  Input tokens: %s\n", formatDistribution(profile.InputTokens))
	fmt.Fprintf(output, "  Output tokens: %s\n", formatDistribution(profile.OutputTokens))
	fmt.Fprintf(output, "  Streaming: %s\n", formatPercent(profile.StreamingRatio))
	fmt.Fprintf(output, "  Cache hit rate: %s\n", formatPercent(profile.CacheHitRatio))
	fmt.Fprintf(output, "  TTFT: %s\n", formatLatency(profile.Latency["ttft"]))
	fmt.Fprintf(output, "  TBT: %s\n", formatLatency(profile.Latency["tbt"]))
	fmt.Fprintf(output, "  TTLT: %s\n", formatLatency(profile.Latency["ttlt"]))
	if isBasicProfile(result) {
		output.WriteString("  Unavailable from aggregate-only telemetry.\n")
		output.WriteString("  Enable resource diagnostic logs.\n")
		output.WriteString("  Required category: AzureOpenAIRequestUsage\n")
	}
	output.WriteString("\n")
}

func writeTerminalTarget(output *strings.Builder, result *model.AssessmentResult) {
	assessment := result.SloAssessment
	output.WriteString("2. Current offer target assessment\n")
	fmt.Fprintf(output, "  %s\n", assessmentLabel(result))
	fmt.Fprintf(output, "  %s\n", targetComparisonTitle(assessment))
	fmt.Fprintf(output, "  Observed: %s\n", formatMilliseconds(assessment.ActualMS))
	fmt.Fprintf(output, "  Target: %s\n", formatMilliseconds(assessment.TargetMS))
	if message := strings.TrimSpace(assessment.Message); message != "" &&
		!strings.EqualFold(message, assessmentLabel(result)) {
		fmt.Fprintf(output, "  %s\n", message)
	}
	output.WriteString("  " + model.ContractualNotice + "\n\n")
}

func writeTerminalBenchmark(output *strings.Builder, result *model.AssessmentResult) {
	comparison := result.BenchmarkComparison
	output.WriteString("3. LLM-Runner benchmark comparison\n")
	output.WriteString("  Comparison with a similar workload\n")
	fmt.Fprintf(output, "  %s\n", benchmarkLabel(comparison))
	fmt.Fprintf(output, "  Observed: %s\n", formatMilliseconds(comparison.ActualMS))
	fmt.Fprintf(output, "  Benchmark: %s\n", formatMilliseconds(comparison.BenchmarkValueMS))
	if message := strings.TrimSpace(comparison.Message); message != "" {
		fmt.Fprintf(output, "  %s\n", message)
	}
	output.WriteString("\n")
}

func writeTerminalRecommendations(output *strings.Builder, result *model.AssessmentResult) {
	output.WriteString("4. Guided recommendations\n")
	output.WriteString("  Recommended next steps\n")
	if len(result.Recommendations) == 0 {
		output.WriteString("  No additional recommendations for this assessment.\n\n")
		return
	}
	for index, recommendation := range result.Recommendations {
		fmt.Fprintf(output, "  %d. %s\n", index+1, displayOr(recommendation.ObservedPattern, "Next step"))
		if evidence := strings.TrimSpace(recommendation.SupportingEvidence); evidence != "" {
			fmt.Fprintf(output, "     Evidence: %s\n", evidence)
		}
		if action := strings.TrimSpace(recommendation.RecommendedAction); action != "" {
			fmt.Fprintf(output, "     Action: %s\n", action)
		}
		if confidence := strings.TrimSpace(recommendation.Confidence); confidence != "" {
			fmt.Fprintf(output, "     Confidence: %s\n", confidence)
		}
	}
	output.WriteString("\n")
}

func writeTerminalEvidence(output *strings.Builder, evidence *model.EvidencePackage) {
	output.WriteString("5. Evidence package\n")
	output.WriteString("  Evidence package for support\n")
	if summary := strings.TrimSpace(evidence.Summary); summary != "" {
		fmt.Fprintf(output, "  %s\n", summary)
	}
	fmt.Fprintf(output, "  Deployment: %s\n", displayOr(evidence.DeploymentID, unavailable))
	fmt.Fprintf(output, "  Time range: %s\n", formatTimeRange(evidence.TimeRange))
	fmt.Fprintf(output, "  Observed: %s\n", formatMilliseconds(evidence.ActualMS))
	fmt.Fprintf(output, "  Target: %s\n", formatMilliseconds(evidence.TargetMS))
	fmt.Fprintf(output, "  Benchmark: %s\n", formatMilliseconds(evidence.BenchmarkValueMS))
	writeTerminalList(output, "Tested actions", evidence.TestedActions)
	writeTerminalList(output, "Recommended tests", evidence.RecommendedTests)
	writeTerminalList(output, "Representative request IDs", evidence.RepresentativeRequestIDs)
	output.WriteString("  " + model.EvidenceNotice + "\n\n")
}

func writeTerminalOffers(
	output *strings.Builder,
	result *model.AssessmentResult,
	options []model.OfferOption,
) {
	output.WriteString("5. Explore offer options\n")
	if headline := strings.TrimSpace(result.OfferGuidance.Headline); headline != "" {
		fmt.Fprintf(output, "  %s\n", headline)
	}
	for _, option := range options {
		fmt.Fprintf(output, "  - %s", displayOr(option.Offer, unavailable))
		if availability := strings.TrimSpace(option.Availability); availability != "" {
			fmt.Fprintf(output, " · %s", availability)
		}
		output.WriteString("\n")
		if option.FitReason != nil && strings.TrimSpace(*option.FitReason) != "" {
			fmt.Fprintf(output, "    Fit: %s\n", strings.TrimSpace(*option.FitReason))
		}
		if option.NextTest != nil && strings.TrimSpace(*option.NextTest) != "" {
			fmt.Fprintf(output, "    Next test: %s\n", strings.TrimSpace(*option.NextTest))
		}
	}
	if notice := strings.TrimSpace(result.OfferGuidance.Notice); notice != "" {
		fmt.Fprintf(output, "  %s\n", notice)
	}
	output.WriteString("\n")
}

func writeTerminalList(output *strings.Builder, label string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(output, "  %s:\n", label)
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			fmt.Fprintf(output, "    - %s\n", value)
		}
	}
}
