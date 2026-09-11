// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package insights

import (
	"fmt"
	"strings"

	"azure.ai.latency/internal/model"
)

type benchmarkLookup struct {
	cohort     *model.BenchmarkCohort
	dimensions []model.BenchmarkDimension
	message    string
}

func assessBenchmark(
	profile model.TrafficProfile,
	slo model.SloAssessment,
	cohorts []model.BenchmarkCohort,
	rules model.ProductRules,
) model.BenchmarkComparison {
	metric := fmt.Sprintf("%s_%s", strings.ToUpper(rules.AssessmentMetric),
		strings.ToUpper(rules.AssessmentPercentile))
	base := model.BenchmarkComparison{
		Metric:      metric,
		ActualMS:    slo.ActualMS,
		Dimensions:  []model.BenchmarkDimension{},
		SourceLabel: "LLM-Runner benchmark cohorts",
	}

	if !containsFold(rules.BenchmarkTriggerStatuses, slo.Status) {
		if slo.Status == model.SloWithinTarget {
			base.Status = model.BenchmarkNotRequired
			base.Message = "The current offer target is met, so benchmark comparison is not required by default."
		} else {
			base.Status = model.BenchmarkNotRun
			base.Message = "Benchmark comparison was not run for this assessment."
		}
		return base
	}

	lookup := matchBenchmark(profile, cohorts, rules, metric)
	base.Dimensions = lookup.dimensions
	if lookup.cohort == nil {
		base.Status = model.BenchmarkCoverageGap
		base.Message = lookup.message
		if base.Message == "" {
			base.Message = "No benchmark lookup was available."
		}
		return base
	}

	cohort := lookup.cohort
	confidence := "High"
	freshness := cohort.IngestionTime
	value := cohort.Value
	sampleSize := cohort.SampleSize
	explained := slo.ActualMS != nil && *slo.ActualMS <= value
	comparison := model.ComparisonAbove
	message := "Observed latency is above the benchmark reference for the matched workload cohort."
	if explained {
		comparison = model.ComparisonAtOrBelow
		message = "Observed latency is at or below the benchmark reference for the matched workload cohort."
	}

	base.Status = model.BenchmarkMatched
	base.Message = message
	base.BenchmarkValueMS = &value
	base.SampleSize = &sampleSize
	base.Confidence = &confidence
	base.Freshness = &freshness
	base.Comparison = &comparison
	base.WorkloadProfileExplainsGap = &explained
	base.SelectedCohort = &model.SelectedBenchmarkCohort{
		Model:             cohort.Model,
		ModelVersion:      cohort.ModelVersion,
		OfferingType:      cohort.OfferingType,
		Region:            cohort.Region,
		APIPath:           cohort.APIPath,
		StreamingMode:     cohort.StreamingMode,
		InputTokenBucket:  cohort.InputTokenBucket,
		OutputTokenBucket: cohort.OutputTokenBucket,
		CacheHitRate:      cohort.CacheHitRate,
		Metric:            cohort.Metric,
		ValueMS:           cohort.Value,
		SampleSize:        cohort.SampleSize,
		IngestionTime:     cohort.IngestionTime,
	}
	return base
}

func matchBenchmark(
	profile model.TrafficProfile,
	cohorts []model.BenchmarkCohort,
	rules model.ProductRules,
	metric string,
) benchmarkLookup {
	if profile.InputTokens.P95 == nil || profile.OutputTokens.P95 == nil {
		return benchmarkLookup{
			message: "Token distributions are unavailable, so no valid benchmark can be selected.",
		}
	}

	inputBucket := bucketFor(*profile.InputTokens.P95, rules.InputTokenBuckets)
	outputBucket := bucketFor(*profile.OutputTokens.P95, rules.OutputTokenBuckets)
	streamingBucket := ""
	if profile.StreamingRatio != nil {
		streamingBucket = bucketFor(*profile.StreamingRatio, rules.StreamingRatioBuckets)
	}

	dimensions := []model.BenchmarkDimension{
		{
			Dimension:      "Input tokens",
			WorkloadValue:  *profile.InputTokens.P95,
			BenchmarkValue: inputBucket,
			Rule:           "Exact configured token bucket",
			Status:         "matched",
		},
		{
			Dimension:      "Output tokens",
			WorkloadValue:  *profile.OutputTokens.P95,
			BenchmarkValue: outputBucket,
			Rule:           "Exact configured token bucket",
			Status:         "matched",
		},
		{
			Dimension:      "Streaming",
			WorkloadValue:  profile.StreamingRatio,
			BenchmarkValue: streamingBucket,
			Rule:           "Strict streaming bucket",
			Status:         "matched",
		},
		{
			Dimension:      "Offering type",
			WorkloadValue:  profile.Deployment.SKUName,
			BenchmarkValue: profile.Deployment.SKUName,
			Rule:           "Recorded only",
			Status:         "recorded_only",
		},
		{
			Dimension:      "Region",
			WorkloadValue:  profile.Deployment.Region,
			BenchmarkValue: nil,
			Rule:           "Recorded only",
			Status:         "recorded_only",
		},
		{
			Dimension:      "API path",
			WorkloadValue:  profile.APIPath,
			BenchmarkValue: nil,
			Rule:           "Recorded only",
			Status:         "recorded_only",
		},
		{
			Dimension:      "Cache hit rate",
			WorkloadValue:  profile.CacheHitRatio,
			BenchmarkValue: nil,
			Rule:           "Recorded only",
			Status:         "recorded_only",
		},
	}

	strict := make([]model.BenchmarkCohort, 0)
	for _, cohort := range cohorts {
		if strings.EqualFold(cohort.Model, profile.Deployment.Model) &&
			strings.EqualFold(cohort.ModelVersion, profile.Deployment.ModelVersion) &&
			strings.EqualFold(cohort.OfferingType, profile.Deployment.SKUName) &&
			strings.EqualFold(cohort.StreamingMode, streamingBucket) &&
			strings.EqualFold(cohort.Metric, metric) {
			strict = append(strict, cohort)
		}
	}
	if len(strict) == 0 {
		return benchmarkLookup{
			dimensions: dimensions,
			message:    "No cohort matches all strict model, version, offer, streaming, and metric dimensions.",
		}
	}
	for i := range strict {
		cohort := &strict[i]
		if cohort.InputTokenBucket == inputBucket && cohort.OutputTokenBucket == outputBucket {
			return benchmarkLookup{
				cohort:     cohort,
				dimensions: dimensions,
				message:    "Matched an exact workload cohort without relaxing token buckets or strict dimensions.",
			}
		}
	}
	return benchmarkLookup{
		dimensions: dimensions,
		message:    "No benchmark cohort matches the workload's exact input and output token buckets.",
	}
}

func bucketFor(value float64, buckets []model.NumericBucket) string {
	for _, bucket := range buckets {
		if bucket.Operator == "otherwise" || (bucket.Threshold != nil &&
			compare(value, bucket.Operator, *bucket.Threshold)) {
			return bucket.Label
		}
	}
	return ""
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
