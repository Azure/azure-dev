// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package model

import "strings"

const (
	SchemaVersion = "1.0"

	ModeAssess = "assess"
	ModeDemo   = "demo"

	SloUnavailable  = "unavailable"
	SloInsufficient = "insufficient_data"
	SloWithinTarget = "within_current_offer_target"
	SloAboveTarget  = "above_current_offer_target"

	BenchmarkNotRequired = "not_required"
	BenchmarkNotRun      = "not_run"
	BenchmarkCoverageGap = "coverage_gap"
	BenchmarkMatched     = "matched"

	ComparisonAtOrBelow = "at_or_below_reference"
	ComparisonAbove     = "above_reference"

	AvailabilityAvailable    = "available"
	AvailabilityPartial      = "partial"
	AvailabilityUnavailable  = "unavailable"
	AvailabilityIllustrative = "illustrative"

	OfferPriorityProcessing = "Priority Processing"
	OfferPTUM               = "PTU-M"

	GoalLowerOrPredictable = "lower_or_predictable_latency"
)

const (
	DataNoticeDemo = "Illustrative sample data"

	ContractualNotice            = "This is an operating target, not a contractual SLA."
	RecommendationInterpretation = "Workload hypothesis for the next test; not a confirmed platform root cause."
	EvidenceNotice               = "Customer-safe workload evidence only. No raw prompts, secrets, internal endpoints, " +
		"or confirmed platform root cause are included."
)

// TimeWindow is the UTC interval analyzed by an assessment.
type TimeWindow struct {
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Label     string `json:"label"`
}

// DeploymentReference identifies an Azure OpenAI deployment.
type DeploymentReference struct {
	DeploymentID   string
	Subscription   string
	ResourceGroup  string
	AccountName    string
	DeploymentName string
	UserTenantID   string
}

// DeploymentContext describes the assessed Azure OpenAI deployment.
type DeploymentContext struct {
	DeploymentID   string   `json:"deployment_id"`
	SubscriptionID string   `json:"subscription_id"`
	ResourceGroup  string   `json:"resource_group"`
	AccountName    string   `json:"account_name"`
	DeploymentName string   `json:"deployment_name"`
	Model          string   `json:"model"`
	ModelVersion   string   `json:"model_version"`
	Offer          string   `json:"offer"`
	BillingModel   string   `json:"billing_model"`
	ServiceTier    string   `json:"service_tier"`
	DeploymentType string   `json:"deployment_type"`
	SKUName        string   `json:"sku_name"`
	Region         string   `json:"region"`
	TPMRateLimit   *float64 `json:"tpm_rate_limit"`
	RPMRateLimit   *float64 `json:"rpm_rate_limit"`
}

// Distribution describes an observed token distribution.
type Distribution struct {
	Average *float64 `json:"average"`
	P50     *float64 `json:"p50"`
	P95     *float64 `json:"p95"`
	Unit    string   `json:"unit"`
}

// LatencyDistribution describes latency percentiles in milliseconds.
type LatencyDistribution struct {
	AverageMS *float64 `json:"average_ms"`
	P50MS     *float64 `json:"p50_ms"`
	P90MS     *float64 `json:"p90_ms"`
	P95MS     *float64 `json:"p95_ms"`
}

// RequestRate describes average and peak requests per minute.
type RequestRate struct {
	AverageRPM  *float64 `json:"average_rpm"`
	PeakRPM     *float64 `json:"peak_rpm"`
	BurstFactor *float64 `json:"burst_factor"`
}

// TokenRate describes observed token throughput.
type TokenRate struct {
	InputAverageTPM  *float64 `json:"input_average_tpm"`
	InputPeakTPM     *float64 `json:"input_peak_tpm"`
	OutputAverageTPM *float64 `json:"output_average_tpm"`
	OutputPeakTPM    *float64 `json:"output_peak_tpm"`
	AverageTPM       *float64 `json:"average_tpm"`
	PeakTPM          *float64 `json:"peak_tpm"`
	LimitTPM         *float64 `json:"limit_tpm"`
	PeakLimitRatio   *float64 `json:"peak_limit_ratio"`
}

// DataAvailability records the quality and provenance of an input data source.
type DataAvailability struct {
	Source           string   `json:"source"`
	Status           string   `json:"status"`
	Fields           []string `json:"fields"`
	Note             string   `json:"note"`
	IllustrativeData bool     `json:"illustrative_data"`
}

// ProviderMetadata describes a contract or telemetry provider.
type ProviderMetadata struct {
	Name          string `json:"name"`
	SchemaVersion string `json:"schema_version"`
	DataVersion   string `json:"data_version"`
	DataStatus    string `json:"data_status"`
	SourceLabel   string `json:"source_label"`
	Description   string `json:"description"`
}

// TrafficProfile contains aggregate and request-level workload observations.
type TrafficProfile struct {
	Deployment               DeploymentContext              `json:"deployment"`
	InputTokens              Distribution                   `json:"input_tokens"`
	OutputTokens             Distribution                   `json:"output_tokens"`
	StreamingRatio           *float64                       `json:"streaming_ratio"`
	CacheHitRatio            *float64                       `json:"cache_hit_ratio"`
	RequestRate              RequestRate                    `json:"request_rate"`
	TokenRate                TokenRate                      `json:"token_rate"`
	RequestCount             *int64                         `json:"request_count"`
	TTFTSampleCount          *int64                         `json:"ttft_sample_count"`
	TBTSampleCount           *int64                         `json:"tbt_sample_count"`
	Latency                  map[string]LatencyDistribution `json:"latency"`
	ErrorRate                *float64                       `json:"error_rate"`
	ThrottlingRate           *float64                       `json:"throttling_rate"`
	APIPath                  string                         `json:"api_path"`
	RepresentativeRequestIDs []string                       `json:"representative_request_ids"`
	DataAvailability         []DataAvailability             `json:"data_availability"`
}

// HasFullRequestProfile reports whether request-level token and latency percentiles are available.
func (p TrafficProfile) HasFullRequestProfile(metric string) bool {
	latency, ok := p.Latency[strings.ToLower(metric)]
	return ok && latency.P95MS != nil && p.InputTokens.P95 != nil && p.OutputTokens.P95 != nil
}

// MetricSampleCount returns the request-level sample count for a latency metric.
func (p TrafficProfile) MetricSampleCount(metric string) *int64 {
	switch strings.ToUpper(metric) {
	case "TTFT":
		return p.TTFTSampleCount
	case "TBT":
		return p.TBTSampleCount
	default:
		return p.RequestCount
	}
}

// SloTarget is one current-offer target catalog row.
type SloTarget struct {
	Model         string  `json:"model"`
	ModelVersion  string  `json:"model_version"`
	OfferingType  string  `json:"offering_type"`
	PoolType      string  `json:"pool_type"`
	Target        string  `json:"target"`
	Value         float64 `json:"value"`
	IngestionTime string  `json:"ingestion_time"`
}

// SloAssessment is the current-offer target decision.
type SloAssessment struct {
	Status            string   `json:"status"`
	Message           string   `json:"message"`
	Metric            string   `json:"metric"`
	Percentile        string   `json:"percentile"`
	ActualMS          *float64 `json:"actual_ms"`
	TargetMS          *float64 `json:"target_ms"`
	SourceLabel       string   `json:"source_label"`
	ContractualNotice string   `json:"contractual_notice"`
}

// BenchmarkCohort is one similar-workload benchmark catalog row.
type BenchmarkCohort struct {
	Model             string   `json:"model"`
	ModelVersion      string   `json:"model_version"`
	OfferingType      string   `json:"offering_type"`
	Region            string   `json:"region"`
	APIPath           string   `json:"api_path"`
	StreamingMode     string   `json:"streaming_mode"`
	InputTokenBucket  string   `json:"input_token_bucket"`
	OutputTokenBucket string   `json:"output_token_bucket"`
	CacheHitRate      *float64 `json:"cache_hit_rate"`
	Metric            string   `json:"metric"`
	Value             float64  `json:"value"`
	SampleSize        int      `json:"sample_size"`
	IngestionTime     string   `json:"ingestion_time"`
}

// BenchmarkDimension records how one workload dimension was matched.
type BenchmarkDimension struct {
	Dimension      string `json:"dimension"`
	WorkloadValue  any    `json:"workload_value"`
	BenchmarkValue any    `json:"benchmark_value"`
	Rule           string `json:"rule"`
	Status         string `json:"status"`
}

// SelectedBenchmarkCohort is the customer-safe benchmark cohort projection.
type SelectedBenchmarkCohort struct {
	Model             string   `json:"model"`
	ModelVersion      string   `json:"model_version"`
	OfferingType      string   `json:"offering_type"`
	Region            string   `json:"region"`
	APIPath           string   `json:"api_path"`
	StreamingMode     string   `json:"streaming_mode"`
	InputTokenBucket  string   `json:"input_token_bucket"`
	OutputTokenBucket string   `json:"output_token_bucket"`
	CacheHitRate      *float64 `json:"cache_hit_rate"`
	Metric            string   `json:"metric"`
	ValueMS           float64  `json:"value_ms"`
	SampleSize        int      `json:"sample_size"`
	IngestionTime     string   `json:"ingestion_time"`
}

// BenchmarkComparison is the similar-workload benchmark decision.
type BenchmarkComparison struct {
	Status                     string                   `json:"status"`
	Message                    string                   `json:"message"`
	Metric                     string                   `json:"metric"`
	ActualMS                   *float64                 `json:"actual_ms"`
	BenchmarkValueMS           *float64                 `json:"benchmark_value_ms"`
	SampleSize                 *int                     `json:"sample_size"`
	Confidence                 *string                  `json:"confidence"`
	Freshness                  *string                  `json:"freshness"`
	Comparison                 *string                  `json:"comparison"`
	WorkloadProfileExplainsGap *bool                    `json:"workload_profile_explains_gap"`
	SelectedCohort             *SelectedBenchmarkCohort `json:"selected_cohort"`
	Dimensions                 []BenchmarkDimension     `json:"dimensions"`
	SourceLabel                string                   `json:"source_label"`
}

// Recommendation is one evidence-backed next step.
type Recommendation struct {
	ObservedPattern    string  `json:"observed_pattern"`
	SupportingEvidence string  `json:"supporting_evidence"`
	RecommendedAction  string  `json:"recommended_action"`
	Confidence         string  `json:"confidence"`
	Category           string  `json:"category"`
	Interpretation     string  `json:"interpretation"`
	RuleID             *string `json:"rule_id"`
}

// RecommendationRule is a configurable workload rule.
type RecommendationRule struct {
	ID              string  `json:"id"`
	Enabled         bool    `json:"enabled"`
	Priority        int     `json:"priority"`
	Metric          string  `json:"metric"`
	Operator        string  `json:"operator"`
	Threshold       float64 `json:"threshold"`
	ObservedPattern string  `json:"observed_pattern"`
	Evidence        string  `json:"evidence"`
	Action          string  `json:"action"`
	Confidence      string  `json:"confidence"`
}

// OfferRecommendationRule is a configurable offer-fit rule.
type OfferRecommendationRule struct {
	ID        string  `json:"id"`
	Enabled   bool    `json:"enabled"`
	Priority  int     `json:"priority"`
	Offer     string  `json:"offer"`
	Metric    string  `json:"metric"`
	Operator  string  `json:"operator"`
	Threshold float64 `json:"threshold"`
	Fit       string  `json:"fit"`
	Action    string  `json:"action"`
	Tradeoff  string  `json:"tradeoff"`
}

// OutcomeRule is a configurable terminal assessment outcome.
type OutcomeRule struct {
	ObservedPattern string `json:"observed_pattern"`
	Evidence        string `json:"evidence"`
	Action          string `json:"action"`
	Confidence      string `json:"confidence"`
	Category        string `json:"category"`
}

// NumericBucket maps a numeric value to a product-contract label.
type NumericBucket struct {
	Label     string   `json:"label"`
	Operator  string   `json:"operator"`
	Threshold *float64 `json:"threshold"`
}

// ProductRules contains the decision and recommendation contract.
type ProductRules struct {
	SchemaVersion            string
	AssessmentMetric         string
	AssessmentPercentile     string
	TargetPoolType           string
	MinimumRequestSamples    int64
	MinimumMetricSamples     int64
	BenchmarkTriggerStatuses []string
	OfferTriggerStatuses     []string
	OfferRequiredGoal        string
	InputTokenBuckets        []NumericBucket
	OutputTokenBuckets       []NumericBucket
	StreamingRatioBuckets    []NumericBucket
	CacheRatioBuckets        []NumericBucket
	WorkloadRules            []RecommendationRule
	OfferRules               []OfferRecommendationRule
	Outcomes                 map[string]OutcomeRule
}

// OfferOption describes one offer and its live eligibility state.
type OfferOption struct {
	Offer                     string  `json:"offer"`
	Available                 *bool   `json:"available"`
	Availability              string  `json:"availability"`
	Note                      string  `json:"note"`
	RequiresLiveCapacityCheck bool    `json:"requires_live_capacity_check"`
	MinimumPTUs               *int    `json:"minimum_ptus"`
	TargetSummary             *string `json:"target_summary"`
	FitReason                 *string `json:"fit_reason"`
	Tradeoff                  *string `json:"tradeoff"`
	NextTest                  *string `json:"next_test"`
	Recommended               bool    `json:"recommended"`
	EligibilitySummary        *string `json:"eligibility_summary"`
	RuleID                    *string `json:"rule_id"`
}

// OfferGuidance is the user-facing offer explorer decision.
type OfferGuidance struct {
	Shown    bool          `json:"shown"`
	Headline string        `json:"headline"`
	Reason   string        `json:"reason"`
	Options  []OfferOption `json:"options"`
	Notice   string        `json:"notice"`
}

// EvidencePackage is the customer-safe support payload for an unexplained target miss.
type EvidencePackage struct {
	Summary                  string         `json:"summary"`
	DeploymentID             string         `json:"deployment_id"`
	TimeRange                TimeWindow     `json:"time_range"`
	Model                    string         `json:"model"`
	ModelVersion             string         `json:"model_version"`
	Offer                    string         `json:"offer"`
	BillingModel             string         `json:"billing_model"`
	ServiceTier              string         `json:"service_tier"`
	DeploymentType           string         `json:"deployment_type"`
	SKUName                  string         `json:"sku_name"`
	Region                   string         `json:"region"`
	SloStatus                string         `json:"slo_status"`
	ActualMS                 *float64       `json:"actual_ms"`
	TargetMS                 *float64       `json:"target_ms"`
	BenchmarkStatus          string         `json:"benchmark_status"`
	BenchmarkMetric          string         `json:"benchmark_metric"`
	BenchmarkValueMS         *float64       `json:"benchmark_value_ms"`
	TrafficProfile           map[string]any `json:"traffic_profile"`
	TestedActions            []string       `json:"tested_actions"`
	RecommendedTests         []string       `json:"recommended_tests"`
	RepresentativeRequestIDs []string       `json:"representative_request_ids"`
	Notice                   string         `json:"notice"`
}

// AssessmentResult is the versioned public result contract.
type AssessmentResult struct {
	SchemaVersion          string              `json:"schema_version"`
	GeneratedAt            string              `json:"generated_at"`
	Mode                   string              `json:"mode"`
	Scenario               *string             `json:"scenario"`
	ScenarioTitle          *string             `json:"scenario_title"`
	DataNotice             string              `json:"data_notice"`
	IllustrativeComponents []string            `json:"illustrative_components"`
	TimeRange              TimeWindow          `json:"time_range"`
	TrafficProfile         TrafficProfile      `json:"traffic_profile"`
	SloAssessment          SloAssessment       `json:"slo_assessment"`
	BenchmarkComparison    BenchmarkComparison `json:"benchmark_comparison"`
	Recommendations        []Recommendation    `json:"recommendations"`
	OfferGuidance          OfferGuidance       `json:"offer_guidance"`
	EligibleOfferOptions   []OfferOption       `json:"eligible_offer_options"`
	EvidencePackage        *EvidencePackage    `json:"evidence_package"`
	DataAvailability       []DataAvailability  `json:"data_availability"`
}

// Float64 returns a pointer to value.
func Float64(value float64) *float64 {
	return new(value)
}

// Int returns a pointer to value.
func Int(value int) *int {
	return new(value)
}

// Int64 returns a pointer to value.
func Int64(value int64) *int64 {
	return new(value)
}

// Bool returns a pointer to value.
func Bool(value bool) *bool {
	return new(value)
}

// String returns a pointer to value.
func String(value string) *string {
	return new(value)
}
