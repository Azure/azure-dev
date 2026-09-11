// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package insights

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"azure.ai.latency/internal/contracts"
	"azure.ai.latency/internal/model"
)

// TrafficProfileProvider loads deployment metadata and telemetry for an assessment.
type TrafficProfileProvider interface {
	GetProfile(context.Context, model.DeploymentReference, model.TimeWindow) (model.TrafficProfile, error)
}

// OfferEligibilityProvider resolves live offer availability for a deployment.
type OfferEligibilityProvider interface {
	GetOptions(context.Context, model.DeploymentContext) ([]model.OfferOption, error)
}

// AssessmentRequest contains the resolved inputs for one assessment.
type AssessmentRequest struct {
	Reference              model.DeploymentReference
	TimeWindow             model.TimeWindow
	UserGoal               string
	Mode                   string
	Scenario               *string
	ScenarioTitle          *string
	TestedActions          []string
	IllustrativeComponents []string
	GeneratedAt            string
}

// Service runs the target, benchmark, recommendation, and offer decision flow.
type Service struct {
	catalog       *contracts.Catalog
	profileSource TrafficProfileProvider
	offerSource   OfferEligibilityProvider
	now           func() time.Time
}

// NewService creates an assessment service.
func NewService(
	catalog *contracts.Catalog,
	profileSource TrafficProfileProvider,
	offerSource OfferEligibilityProvider,
) *Service {
	return &Service{
		catalog:       catalog,
		profileSource: profileSource,
		offerSource:   offerSource,
		now:           time.Now,
	}
}

// Assess executes the complete model latency decision flow.
func (s *Service) Assess(ctx context.Context, request AssessmentRequest) (*model.AssessmentResult, error) {
	if s.catalog == nil {
		return nil, fmt.Errorf("latency contract catalog is required")
	}
	if s.profileSource == nil {
		return nil, fmt.Errorf("traffic profile provider is required")
	}

	profile, err := s.profileSource.GetProfile(ctx, request.Reference, request.TimeWindow)
	if err != nil {
		return nil, fmt.Errorf("load traffic profile: %w", err)
	}

	rules := s.catalog.ProductRules
	target := findTarget(s.catalog.SloTargets, profile.Deployment, rules)
	slo := assessSLO(profile, target, rules)
	benchmark := assessBenchmark(profile, slo, s.catalog.BenchmarkCohorts, rules)
	recommendations := recommend(profile, slo, benchmark, rules)
	unexplained := hasUnexplainedTargetViolation(slo, benchmark)

	eligibleOptions := []model.OfferOption{}
	availability := slices.Clone(profile.DataAvailability)
	if strings.EqualFold(profile.Deployment.BillingModel, "PayGo") && !unexplained && s.offerSource != nil {
		options, offerErr := s.offerSource.GetOptions(ctx, profile.Deployment)
		if offerErr != nil {
			availability = append(availability, model.DataAvailability{
				Source: "Offer eligibility",
				Status: model.AvailabilityUnavailable,
				Fields: []string{"Priority Processing", "PTU-M"},
				Note:   offerErr.Error(),
			})
		} else {
			eligibleOptions = enrichOfferOptions(profile, options, rules)
			availability = append(availability, model.DataAvailability{
				Source: "Offer eligibility",
				Status: availabilityStatus(request.Mode),
				Fields: []string{"Priority Processing", "PTU-M"},
				Note: "Offer availability is evaluated from the current regional model, " +
					"capacity, and quota data.",
				IllustrativeData: request.Mode == model.ModeDemo,
			})
		}
	}

	availability = append(availability, catalogAvailability(request.Mode)...)
	offerGuidance := buildOfferGuidance(
		profile,
		slo,
		recommendations,
		eligibleOptions,
		request.UserGoal,
		rules,
		unexplained,
	)

	var evidence *model.EvidencePackage
	if unexplained {
		evidence = buildEvidencePackage(profile, request.TimeWindow, slo, benchmark, recommendations, request.TestedActions)
	}

	generatedAt := request.GeneratedAt
	if generatedAt == "" {
		generatedAt = s.now().UTC().Truncate(time.Second).Format(time.RFC3339)
	}
	illustrative := slices.Clone(request.IllustrativeComponents)
	dataNotice := ""
	if len(illustrative) > 0 {
		dataNotice = model.DataNoticeDemo
	}

	return &model.AssessmentResult{
		SchemaVersion:          model.SchemaVersion,
		GeneratedAt:            generatedAt,
		Mode:                   request.Mode,
		Scenario:               request.Scenario,
		ScenarioTitle:          request.ScenarioTitle,
		DataNotice:             dataNotice,
		IllustrativeComponents: illustrative,
		TimeRange:              request.TimeWindow,
		TrafficProfile:         profile,
		SloAssessment:          slo,
		BenchmarkComparison:    benchmark,
		Recommendations:        recommendations,
		OfferGuidance:          offerGuidance,
		EligibleOfferOptions:   eligibleOptions,
		EvidencePackage:        evidence,
		DataAvailability:       availability,
	}, nil
}

func findTarget(
	targets []model.SloTarget,
	deployment model.DeploymentContext,
	rules model.ProductRules,
) *model.SloTarget {
	if strings.EqualFold(deployment.Offer, model.OfferPriorityProcessing) {
		return nil
	}
	targetName := fmt.Sprintf("SLA_%s_%s", strings.ToUpper(rules.AssessmentPercentile),
		strings.ToUpper(rules.AssessmentMetric))
	for i := range targets {
		target := &targets[i]
		if strings.EqualFold(target.Model, deployment.Model) &&
			strings.EqualFold(target.ModelVersion, deployment.ModelVersion) &&
			strings.EqualFold(target.OfferingType, deployment.SKUName) &&
			strings.EqualFold(target.PoolType, rules.TargetPoolType) &&
			strings.EqualFold(target.Target, targetName) {
			return target
		}
	}
	return nil
}

func availabilityStatus(mode string) string {
	if mode == model.ModeDemo {
		return model.AvailabilityIllustrative
	}
	return model.AvailabilityAvailable
}

func catalogAvailability(mode string) []model.DataAvailability {
	illustrative := mode == model.ModeDemo
	status := availabilityStatus(mode)
	return []model.DataAvailability{
		{
			Source:           "Current-offer target catalog",
			Status:           status,
			Fields:           []string{"TBT P95 target"},
			Note:             "Bundled schema 1.0 current-offer target catalog.",
			IllustrativeData: illustrative,
		},
		{
			Source:           "LLM-Runner benchmark cohorts",
			Status:           status,
			Fields:           []string{"Comparable workload benchmark"},
			Note:             "Bundled schema 1.0 benchmark cohort catalog.",
			IllustrativeData: illustrative,
		},
	}
}

func buildEvidencePackage(
	profile model.TrafficProfile,
	window model.TimeWindow,
	slo model.SloAssessment,
	benchmark model.BenchmarkComparison,
	recommendations []model.Recommendation,
	testedActions []string,
) *model.EvidencePackage {
	recommendedTests := make([]string, 0, len(recommendations))
	for _, recommendation := range recommendations {
		recommendedTests = append(recommendedTests, recommendation.RecommendedAction)
	}

	latency := func(metric string) model.LatencyDistribution {
		return profile.Latency[strings.ToLower(metric)]
	}
	traffic := map[string]any{
		"input_tokens_p50":                 profile.InputTokens.P50,
		"input_tokens_p95":                 profile.InputTokens.P95,
		"output_tokens_p50":                profile.OutputTokens.P50,
		"output_tokens_p95":                profile.OutputTokens.P95,
		"streaming_ratio":                  profile.StreamingRatio,
		"cache_hit_ratio":                  profile.CacheHitRatio,
		"average_requests_per_minute":      profile.RequestRate.AverageRPM,
		"peak_requests_per_minute":         profile.RequestRate.PeakRPM,
		"average_tokens_per_minute":        profile.TokenRate.AverageTPM,
		"peak_tokens_per_minute":           profile.TokenRate.PeakTPM,
		"average_input_tokens_per_minute":  profile.TokenRate.InputAverageTPM,
		"peak_input_tokens_per_minute":     profile.TokenRate.InputPeakTPM,
		"average_output_tokens_per_minute": profile.TokenRate.OutputAverageTPM,
		"peak_output_tokens_per_minute":    profile.TokenRate.OutputPeakTPM,
		"deployment_tpm_limit":             profile.TokenRate.LimitTPM,
		"ttft_p50_ms":                      latency("ttft").P50MS,
		"ttft_p95_ms":                      latency("ttft").P95MS,
		"tbt_p50_ms":                       latency("tbt").P50MS,
		"tbt_p95_ms":                       latency("tbt").P95MS,
		"ttlt_p50_ms":                      latency("ttlt").P50MS,
		"ttlt_p95_ms":                      latency("ttlt").P95MS,
		"error_rate":                       profile.ErrorRate,
		"throttling_rate":                  profile.ThrottlingRate,
	}

	return &model.EvidencePackage{
		Summary:                  "Latency remains above the current-offer target without a matching workload explanation.",
		DeploymentID:             profile.Deployment.DeploymentID,
		TimeRange:                window,
		Model:                    profile.Deployment.Model,
		ModelVersion:             profile.Deployment.ModelVersion,
		Offer:                    profile.Deployment.Offer,
		BillingModel:             profile.Deployment.BillingModel,
		ServiceTier:              profile.Deployment.ServiceTier,
		DeploymentType:           profile.Deployment.DeploymentType,
		SKUName:                  profile.Deployment.SKUName,
		Region:                   profile.Deployment.Region,
		SloStatus:                slo.Status,
		ActualMS:                 slo.ActualMS,
		TargetMS:                 slo.TargetMS,
		BenchmarkStatus:          benchmark.Status,
		BenchmarkMetric:          benchmark.Metric,
		BenchmarkValueMS:         benchmark.BenchmarkValueMS,
		TrafficProfile:           traffic,
		TestedActions:            slices.Clone(testedActions),
		RecommendedTests:         recommendedTests,
		RepresentativeRequestIDs: slices.Clone(profile.RepresentativeRequestIDs),
		Notice:                   model.EvidenceNotice,
	}
}
