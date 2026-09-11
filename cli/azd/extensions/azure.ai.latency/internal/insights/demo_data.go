// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package insights

import (
	"context"
	"fmt"
	"slices"

	"azure.ai.latency/internal/contracts"
	"azure.ai.latency/internal/model"
)

const (
	DemoGeneratedAt  = "2026-09-02T08:00:00Z"
	demoSubscription = "00000000-0000-0000-0000-000000000000"
)

// DemoWindow is the fixed time range used by all deterministic demo scenarios.
var DemoWindow = model.TimeWindow{
	StartTime: "2026-09-01T08:00:00Z",
	EndTime:   "2026-09-02T08:00:00Z",
	Label:     "Last 24 hours",
}

// DemoScenario contains one deterministic sample assessment.
type DemoScenario struct {
	Name          string
	Title         string
	Description   string
	UserGoal      string
	TestedActions []string
	Profile       model.TrafficProfile
}

// DemoScenarios returns all scenarios in their product-defined order.
func DemoScenarios() []DemoScenario {
	return []DemoScenario{
		{
			Name:        "within-target",
			Title:       "Within current offer target",
			Description: "Healthy interactive traffic; benchmark comparison is not required.",
			Profile: demoProfile(profileInput{
				Name:           "gpt-5.6-luna",
				Input:          [3]*float64{f(7900), f(8200), f(14500)},
				Output:         [3]*float64{f(240), f(220), f(420)},
				Streaming:      f(0.94),
				Cache:          f(0.68),
				RequestRate:    [2]*float64{f(504), f(888)},
				TTFT:           [3]*float64{f(1120), f(850), f(2500)},
				TBT:            [3]*float64{f(32), f(28), f(45)},
				TBTP90:         f(35),
				TTLT:           [3]*float64{f(5200), f(4400), f(8800)},
				ErrorRate:      f(0.003),
				ThrottlingRate: f(0.002),
				LogsEnabled:    true,
			}),
		},
		{
			Name:  "lower-latency-needed",
			Title: "Target met, more predictable latency needed",
			Description: "The current offer target is met, but the interactive workload needs a lower " +
				"and more predictable tail.",
			UserGoal: model.GoalLowerOrPredictable,
			Profile: demoProfile(profileInput{
				Name:           "gpt-4.1-mini",
				Model:          "gpt-4.1-mini",
				Version:        "2025-04-14",
				Input:          [3]*float64{f(6800), f(6400), f(13800)},
				Output:         [3]*float64{f(210), f(190), f(390)},
				Streaming:      f(0.97),
				Cache:          f(0.74),
				RequestRate:    [2]*float64{f(660), f(1020)},
				TTFT:           [3]*float64{f(1090), f(640), f(1900)},
				TBT:            [3]*float64{f(27), f(22), f(40)},
				TTLT:           [3]*float64{f(4900), f(3900), f(9100)},
				ErrorRate:      f(0.002),
				ThrottlingRate: f(0.001),
				LogsEnabled:    true,
			}),
		},
		{
			Name:        "workload-explained",
			Title:       "Above target, workload profile explains the difference",
			Description: "Long context, low cache reuse, large outputs, and traffic bursts match the expected cohort range.",
			Profile: demoProfile(profileInput{
				Name:           "gpt-5.6-luna",
				Input:          [3]*float64{f(9600), f(9200), f(14500)},
				Output:         [3]*float64{f(520), f(480), f(720)},
				Streaming:      f(0.92),
				Cache:          f(0.12),
				RequestRate:    [2]*float64{f(900), f(4320)},
				TTFT:           [3]*float64{f(3900), f(3200), f(6200)},
				TBT:            [3]*float64{f(52), f(44), f(82)},
				TTLT:           [3]*float64{f(18000), f(14500), f(31000)},
				ErrorRate:      f(0.007),
				ThrottlingRate: f(0.014),
				LogsEnabled:    true,
			}),
		},
		{
			Name:  "unexplained-gap",
			Title: "Above target, gap remains unexplained",
			Description: "The workload matches a low-latency cohort, but observed TTFT remains well " +
				"above its benchmark reference.",
			TestedActions: []string{
				"Repeated the test with stable prompt prefixes.",
				"Smoothed request concurrency for 30 minutes.",
			},
			Profile: demoProfile(profileInput{
				Name:           "gpt-5.6-luna",
				Input:          [3]*float64{f(7200), f(7000), f(12000)},
				Output:         [3]*float64{f(220), f(210), f(380)},
				Streaming:      f(0.95),
				Cache:          f(0.71),
				RequestRate:    [2]*float64{f(420), f(690)},
				TTFT:           [3]*float64{f(3200), f(2500), f(7800)},
				TBT:            [3]*float64{f(62), f(48), f(105)},
				TTLT:           [3]*float64{f(8900), f(6300), f(18200)},
				ErrorRate:      f(0.004),
				ThrottlingRate: f(0.002),
				RequestIDs:     []string{"demo-request-7f2a", "demo-request-9c41", "demo-request-b830"},
				LogsEnabled:    true,
			}),
		},
		{
			Name:  "no-benchmark-coverage",
			Title: "Above target, no matching benchmark coverage",
			Description: "The current-offer target is exceeded, but this non-streaming workload is " +
				"outside the current benchmark coverage.",
			Profile: demoProfile(profileInput{
				Name:           "gpt-4.1-mini",
				Model:          "gpt-4.1-mini",
				Version:        "2025-04-14",
				Input:          [3]*float64{f(18500), f(17800), f(29000)},
				Output:         [3]*float64{f(190), f(180), f(320)},
				Streaming:      f(0.10),
				Cache:          f(0.52),
				RequestRate:    [2]*float64{f(780), f(1320)},
				TTFT:           [3]*float64{f(2300), f(1900), f(4900)},
				TBT:            [3]*float64{f(36), f(28), f(60)},
				TTLT:           [3]*float64{f(6600), f(5400), f(12300)},
				ErrorRate:      f(0.004),
				ThrottlingRate: f(0.003),
				RequestIDs:     []string{"demo-request-c012"},
				LogsEnabled:    true,
			}),
		},
		{
			Name:  "logs-unavailable",
			Title: "Log Analytics is not enabled",
			Description: "ARM and platform metrics are available, but request-level distributions and " +
				"percentiles remain unavailable.",
			Profile: demoProfile(profileInput{
				Name:           "gpt-5.6-luna",
				Input:          [3]*float64{f(9200), nil, nil},
				Output:         [3]*float64{f(310), nil, nil},
				Streaming:      f(0.89),
				RequestRate:    [2]*float64{f(540), f(1080)},
				TTFT:           [3]*float64{f(1600), nil, nil},
				TBT:            [3]*float64{f(41), nil, nil},
				TTLT:           [3]*float64{f(7400), nil, nil},
				ErrorRate:      f(0.006),
				ThrottlingRate: f(0.005),
			}),
		},
	}
}

// FindDemoScenario resolves a demo scenario by name.
func FindDemoScenario(name string) (DemoScenario, bool) {
	for _, scenario := range DemoScenarios() {
		if scenario.Name == name {
			return scenario, true
		}
	}
	return DemoScenario{}, false
}

// AssessDemo evaluates one deterministic demo scenario through the production decision engine.
func AssessDemo(
	ctx context.Context,
	catalog *contracts.Catalog,
	scenario DemoScenario,
) (*model.AssessmentResult, error) {
	service := NewService(catalog, StaticProfileProvider{Profile: scenario.Profile}, DemoOfferProvider{})
	return service.Assess(ctx, AssessmentRequest{
		TimeWindow:    DemoWindow,
		UserGoal:      scenario.UserGoal,
		Mode:          model.ModeDemo,
		Scenario:      model.String(scenario.Name),
		ScenarioTitle: model.String(scenario.Title),
		TestedActions: slices.Clone(scenario.TestedActions),
		IllustrativeComponents: []string{
			"Deployment and telemetry",
			"SLA targets",
			"LLM-Runner benchmark cohorts",
			"Offer eligibility",
		},
		GeneratedAt: DemoGeneratedAt,
	})
}

// StaticProfileProvider returns one prebuilt demo profile.
type StaticProfileProvider struct {
	Profile model.TrafficProfile
}

// GetProfile implements TrafficProfileProvider.
func (p StaticProfileProvider) GetProfile(
	context.Context,
	model.DeploymentReference,
	model.TimeWindow,
) (model.TrafficProfile, error) {
	return p.Profile, nil
}

// DemoOfferProvider returns deterministic illustrative offer eligibility.
type DemoOfferProvider struct{}

// GetOptions implements OfferEligibilityProvider.
func (DemoOfferProvider) GetOptions(
	_ context.Context,
	deployment model.DeploymentContext,
) ([]model.OfferOption, error) {
	priority := model.OfferOption{
		Offer:        model.OfferPriorityProcessing,
		Available:    model.Bool(true),
		Availability: "Available",
		Note:         "Illustrative regional capability result.",
	}
	ptum := model.OfferOption{
		Offer:                     model.OfferPTUM,
		Available:                 model.Bool(true),
		Availability:              "Available",
		Note:                      "Illustrative capacity and quota result.",
		RequiresLiveCapacityCheck: true,
		MinimumPTUs:               model.Int(15),
	}
	if deployment.Model == "gpt-4.1-mini" && deployment.ModelVersion == "2025-04-14" {
		priority.Available = nil
		priority.Availability = "Not documented"
		priority.Note = "Priority Processing capability is not documented for this illustrative model version."
	}
	return []model.OfferOption{priority, ptum}, nil
}

type profileInput struct {
	Name           string
	Model          string
	Version        string
	Input          [3]*float64
	Output         [3]*float64
	Streaming      *float64
	Cache          *float64
	RequestRate    [2]*float64
	TTFT           [3]*float64
	TBT            [3]*float64
	TBTP90         *float64
	TTLT           [3]*float64
	ErrorRate      *float64
	ThrottlingRate *float64
	RequestIDs     []string
	LogsEnabled    bool
}

func demoProfile(input profileInput) model.TrafficProfile {
	deployment := demoDeployment(input.Name, input.Model, input.Version)
	requestRate := model.RequestRate{
		AverageRPM: input.RequestRate[0],
		PeakRPM:    input.RequestRate[1],
	}
	if input.RequestRate[0] != nil && input.RequestRate[1] != nil && *input.RequestRate[0] > 0 {
		requestRate.BurstFactor = f(*input.RequestRate[1] / *input.RequestRate[0])
	}

	var requestCount, ttftSamples, tbtSamples *int64
	apiPath := ""
	if input.LogsEnabled {
		requestCount = model.Int64(100000)
		ttftSamples = model.Int64(100000)
		tbtSamples = model.Int64(100000)
		apiPath = "CAPI"
	}

	return model.TrafficProfile{
		Deployment:      deployment,
		InputTokens:     distribution(input.Input),
		OutputTokens:    distribution(input.Output),
		StreamingRatio:  input.Streaming,
		CacheHitRatio:   input.Cache,
		RequestRate:     requestRate,
		TokenRate:       demoTokenRate(input.Input, input.Output, input.RequestRate),
		RequestCount:    requestCount,
		TTFTSampleCount: ttftSamples,
		TBTSampleCount:  tbtSamples,
		Latency: map[string]model.LatencyDistribution{
			"ttft": latencyDistribution(input.TTFT, nil),
			"tbt":  latencyDistribution(input.TBT, input.TBTP90),
			"ttlt": latencyDistribution(input.TTLT, nil),
		},
		ErrorRate:                input.ErrorRate,
		ThrottlingRate:           input.ThrottlingRate,
		APIPath:                  apiPath,
		RepresentativeRequestIDs: slices.Clone(input.RequestIDs),
		DataAvailability:         demoAvailability(input.LogsEnabled),
	}
}

func demoDeployment(name, modelName, version string) model.DeploymentContext {
	if modelName == "" {
		modelName = "gpt-5.6-luna"
	}
	if version == "" {
		version = "2026-07-09"
	}
	return model.DeploymentContext{
		DeploymentID: fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/model-latency-demo-rg/providers/"+
				"Microsoft.CognitiveServices/accounts/latency-insights-demo/deployments/%s",
			demoSubscription,
			name,
		),
		SubscriptionID: demoSubscription,
		ResourceGroup:  "model-latency-demo-rg",
		AccountName:    "latency-insights-demo",
		DeploymentName: name,
		Model:          modelName,
		ModelVersion:   version,
		Offer:          "Standard PayGo",
		BillingModel:   "PayGo",
		ServiceTier:    "Default",
		DeploymentType: "global_standard",
		SKUName:        "GlobalStandard",
		Region:         "eastus2",
	}
}

func distribution(values [3]*float64) model.Distribution {
	return model.Distribution{
		Average: values[0],
		P50:     values[1],
		P95:     values[2],
		Unit:    "tokens",
	}
}

func latencyDistribution(values [3]*float64, p90 *float64) model.LatencyDistribution {
	return model.LatencyDistribution{
		AverageMS: values[0],
		P50MS:     values[1],
		P90MS:     p90,
		P95MS:     values[2],
	}
}

func demoTokenRate(
	input [3]*float64,
	output [3]*float64,
	requestRate [2]*float64,
) model.TokenRate {
	if input[0] == nil || input[1] == nil || output[0] == nil || output[1] == nil ||
		requestRate[0] == nil || requestRate[1] == nil {
		return model.TokenRate{}
	}
	inputAverage := *requestRate[0] * *input[0]
	inputPeak := *requestRate[1] * *input[1]
	outputAverage := *requestRate[0] * *output[0]
	outputPeak := *requestRate[1] * *output[1]
	average := inputAverage + outputAverage
	peak := inputPeak + outputPeak
	limit := peak * 1.25
	return model.TokenRate{
		InputAverageTPM:  f(inputAverage),
		InputPeakTPM:     f(inputPeak),
		OutputAverageTPM: f(outputAverage),
		OutputPeakTPM:    f(outputPeak),
		AverageTPM:       f(average),
		PeakTPM:          f(peak),
		LimitTPM:         f(limit),
		PeakLimitRatio:   f(0.8),
	}
}

func demoAvailability(logsEnabled bool) []model.DataAvailability {
	logStatus := model.AvailabilityIllustrative
	logNote := "Deterministic sample request-level telemetry."
	logIllustrative := true
	if !logsEnabled {
		logStatus = model.AvailabilityUnavailable
		logNote = "Diagnostic settings are not enabled. Configure Azure OpenAI resource logs to a " +
			"Log Analytics workspace, then rerun."
		logIllustrative = false
	}
	return []model.DataAvailability{
		{
			Source:           "ARM deployment metadata",
			Status:           model.AvailabilityIllustrative,
			Fields:           []string{"Model", "Model version", "Offer", "Deployment SKU", "Region"},
			Note:             "Deterministic sample deployment metadata.",
			IllustrativeData: true,
		},
		{
			Source:           "Azure Monitor platform metrics",
			Status:           model.AvailabilityIllustrative,
			Fields:           []string{"Request rate", "Streaming ratio", "Error rate", "Throttling rate"},
			Note:             "Deterministic sample aggregate metrics.",
			IllustrativeData: true,
		},
		{
			Source: "Log Analytics request logs",
			Status: logStatus,
			Fields: []string{
				"Token distributions",
				"Cache usage",
				"TTFT P50/P95",
				"TBT P50/P95",
				"TTLT P50/P95",
			},
			Note:             logNote,
			IllustrativeData: logIllustrative,
		},
	}
}

func f(value float64) *float64 {
	return model.Float64(value)
}
