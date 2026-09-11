// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//nolint:lll // Credential-bearing URL fixtures are intentionally explicit.
package azureprovider

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"azure.ai.latency/internal/model"
)

const (
	testSubscription  = "11111111-1111-1111-1111-111111111111"
	testResourceGroup = "example-rg"
	testAccount       = "example-account"
	testDeployment    = "example-deployment"
)

var testWindow = model.TimeWindow{
	StartTime: "2026-09-01T00:00:00Z",
	EndTime:   "2026-09-02T00:00:00Z",
	Label:     "Last 24 hours",
}

type fakeCognitiveClient struct {
	metadata       deploymentMetadata
	metadataErr    error
	models         []regionalModel
	modelsErr      error
	capacities     []modelCapacity
	capacitiesErr  error
	usages         []quotaUsage
	usagesErr      error
	capacityFormat string
	capacityName   string
	capacityVer    string
	usageRegion    string
}

func (f *fakeCognitiveClient) GetDeploymentMetadata(
	context.Context,
	string,
	string,
	string,
) (deploymentMetadata, error) {
	return f.metadata, f.metadataErr
}

func (f *fakeCognitiveClient) ListModels(context.Context, string) ([]regionalModel, error) {
	return f.models, f.modelsErr
}

func (f *fakeCognitiveClient) ListModelCapacities(
	_ context.Context,
	format string,
	name string,
	version string,
) ([]modelCapacity, error) {
	f.capacityFormat = format
	f.capacityName = name
	f.capacityVer = version
	return f.capacities, f.capacitiesErr
}

func (f *fakeCognitiveClient) ListUsages(_ context.Context, region string) ([]quotaUsage, error) {
	f.usageRegion = region
	return f.usages, f.usagesErr
}

type fakeMetricsClient struct {
	results map[metricAggregation]metricQueryResult
	errors  map[metricAggregation]error
	queries []metricQuery
}

func (f *fakeMetricsClient) Query(
	_ context.Context,
	query metricQuery,
) (metricQueryResult, error) {
	f.queries = append(f.queries, query)
	return f.results[query.Aggregation], f.errors[query.Aggregation]
}

type fakeLogsClient struct {
	query func(logQuery) (logQueryResult, error)
}

func (f *fakeLogsClient) Query(_ context.Context, query logQuery) (logQueryResult, error) {
	return f.query(query)
}

func TestGetProfileBuildsMetadataRatesAndDistributions(t *testing.T) {
	t.Parallel()
	firstMinute := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	secondMinute := firstMinute.Add(time.Minute)
	cognitive := &fakeCognitiveClient{metadata: completeMetadata()}
	metrics := &fakeMetricsClient{
		results: map[metricAggregation]metricQueryResult{
			metricAggregationTotal: {
				Series: map[string][]metricPoint{
					metricRequests: {
						{Time: firstMinute, Value: 10},
						{Time: secondMinute, Value: 20},
					},
					metricInput: {
						{Time: firstMinute, Value: 100},
						{Time: secondMinute, Value: 300},
					},
					metricOutput: {
						{Time: firstMinute, Value: 50},
						{Time: secondMinute, Value: 70},
					},
				},
			},
			metricAggregationAverage: {
				Series: map[string][]metricPoint{
					metricTTFT: {{Value: 0}, {Value: 100}, {Value: 200}},
					metricTBT:  {{Value: 20}, {Value: 30}},
					metricTTLT: {{Value: 900}, {Value: 1100}},
				},
			},
		},
		errors: map[metricAggregation]error{},
	}
	logs := &fakeLogsClient{query: completeLogQuery}
	provider := &Provider{
		subscriptionID: testSubscription,
		cognitive:      cognitive,
		logs:           logs,
		metricsFactory: func(endpoint string) (metricsClient, error) {
			if endpoint != "https://eastus2.metrics.monitor.azure.com" {
				t.Fatalf("unexpected metrics endpoint %q", endpoint)
			}
			return metrics, nil
		},
	}

	profile, err := provider.GetProfile(t.Context(), testReference(), testWindow)
	if err != nil {
		t.Fatalf("GetProfile returned an error: %v", err)
	}

	if profile.Deployment.Model != "gpt-4.1-mini" ||
		profile.Deployment.ModelVersion != "2025-04-14" {
		t.Fatalf("unexpected deployment model: %+v", profile.Deployment)
	}
	if profile.Deployment.Offer != "Standard PayGo" ||
		profile.Deployment.BillingModel != "PayGo" ||
		profile.Deployment.ServiceTier != "Default" ||
		profile.Deployment.DeploymentType != "global_standard" {
		t.Fatalf("unexpected deployment classification: %+v", profile.Deployment)
	}
	assertFloat(t, profile.Deployment.TPMRateLimit, 13000)
	assertFloat(t, profile.Deployment.RPMRateLimit, 13)
	assertFloat(t, profile.RequestRate.AverageRPM, 15)
	assertFloat(t, profile.RequestRate.PeakRPM, 20)
	assertFloat(t, profile.RequestRate.BurstFactor, 4.0/3.0)
	assertFloat(t, profile.TokenRate.InputAverageTPM, 200)
	assertFloat(t, profile.TokenRate.InputPeakTPM, 300)
	assertFloat(t, profile.TokenRate.OutputAverageTPM, 60)
	assertFloat(t, profile.TokenRate.OutputPeakTPM, 70)
	assertFloat(t, profile.TokenRate.AverageTPM, 260)
	assertFloat(t, profile.TokenRate.PeakTPM, 370)
	assertFloat(t, profile.TokenRate.PeakLimitRatio, 370.0/13000.0)
	assertFloat(t, profile.InputTokens.P50, 16)
	assertFloat(t, profile.InputTokens.P95, 32)
	assertFloat(t, profile.OutputTokens.P95, 12)
	assertFloat(t, profile.Latency["ttft"].P95MS, 2027)
	assertFloat(t, profile.Latency["tbt"].P90MS, 31)
	assertFloat(t, profile.ErrorRate, 0.125)
	assertFloat(t, profile.ThrottlingRate, 0.025)
	if profile.RequestCount == nil || *profile.RequestCount != 24 {
		t.Fatalf("unexpected request count: %v", profile.RequestCount)
	}
	if profile.TTFTSampleCount == nil || *profile.TTFTSampleCount != 23 {
		t.Fatalf("unexpected TTFT sample count: %v", profile.TTFTSampleCount)
	}
	if profile.TBTSampleCount == nil || *profile.TBTSampleCount != 20 {
		t.Fatalf("unexpected TBT sample count: %v", profile.TBTSampleCount)
	}
	if profile.APIPath != "/openai/deployments/example/chat/completions" {
		t.Fatalf("unexpected sanitized API path %q", profile.APIPath)
	}
	if strings.Join(profile.RepresentativeRequestIDs, ",") != "req-1,req-2" {
		t.Fatalf("unexpected representative request IDs: %v", profile.RepresentativeRequestIDs)
	}
	for index, availability := range profile.DataAvailability {
		if availability.Status != model.AvailabilityAvailable {
			t.Fatalf("availability row %d was not available: %+v", index, availability)
		}
	}
	if len(metrics.queries) != 2 {
		t.Fatalf("expected two metric group queries, got %d", len(metrics.queries))
	}
	for _, query := range metrics.queries {
		if query.DeploymentName != testDeployment || query.ResourceID != accountResourceID(
			testSubscription,
			testResourceGroup,
			testAccount,
		) {
			t.Fatalf("metrics were not scoped to the deployment: %+v", query)
		}
	}
}

func TestGetProfileDegradesTelemetryFailures(t *testing.T) {
	t.Parallel()
	secretError := errors.New(
		`request failed: https://user:password@example.test/query?sig=secret&query=AzureDiagnostics`,
	)
	metrics := &fakeMetricsClient{
		results: map[metricAggregation]metricQueryResult{
			metricAggregationTotal: {
				Series: map[string][]metricPoint{
					metricRequests: {{Value: 4}, {Value: 8}},
				},
			},
		},
		errors: map[metricAggregation]error{metricAggregationAverage: secretError},
	}
	provider := &Provider{
		subscriptionID: testSubscription,
		cognitive:      &fakeCognitiveClient{metadata: completeMetadata()},
		logs: &fakeLogsClient{query: func(logQuery) (logQueryResult, error) {
			return logQueryResult{}, secretError
		}},
		metricsFactory: func(string) (metricsClient, error) {
			return metrics, nil
		},
	}

	profile, err := provider.GetProfile(t.Context(), testReference(), testWindow)
	if err != nil {
		t.Fatalf("telemetry failures should not fail the profile: %v", err)
	}
	if profile.DataAvailability[1].Status != model.AvailabilityPartial {
		t.Fatalf("expected partial metrics, got %+v", profile.DataAvailability[1])
	}
	if profile.DataAvailability[2].Status != model.AvailabilityUnavailable {
		t.Fatalf("expected unavailable logs, got %+v", profile.DataAvailability[2])
	}
	assertFloat(t, profile.RequestRate.AverageRPM, 6)
	for _, availability := range profile.DataAvailability {
		if strings.Contains(availability.Note, "password") ||
			strings.Contains(availability.Note, "sig=") ||
			strings.Contains(availability.Note, "AzureDiagnostics") {
			t.Fatalf("availability note disclosed service request details: %q", availability.Note)
		}
	}
}

func TestGetProfileReturnsRedactedMetadataError(t *testing.T) {
	t.Parallel()
	raw := errors.New(`GET https://user:password@example.test/resource?sig=secret`)
	provider := &Provider{
		subscriptionID: testSubscription,
		cognitive:      &fakeCognitiveClient{metadataErr: raw},
	}

	_, err := provider.GetProfile(t.Context(), testReference(), testWindow)
	if err == nil {
		t.Fatal("expected metadata failure")
	}
	if !errors.Is(err, raw) {
		t.Fatal("metadata failure should preserve its error chain")
	}
	if strings.Contains(err.Error(), "password") ||
		strings.Contains(err.Error(), "sig=") ||
		strings.Contains(err.Error(), "https://") {
		t.Fatalf("metadata error disclosed a URL or credential: %q", err)
	}
}

func TestGetProfileUsesAzureDiagnosticsFallback(t *testing.T) {
	t.Parallel()
	logs := &fakeLogsClient{query: func(query logQuery) (logQueryResult, error) {
		switch {
		case strings.Contains(query.Text, "getschema"):
			return logQueryResult{}, errors.New("resource-specific table not found")
		case strings.Contains(query.Text, "Category == 'AzureOpenAIRequestUsage'"):
			return logQueryResult{Rows: []map[string]any{{
				"usage_rows":                  9.0,
				valueInputAverage:             12.0,
				valueInputP50:                 10.0,
				valueInputP95:                 20.0,
				valueOutputAverage:            4.0,
				valueOutputP50:                3.0,
				valueOutputP95:                8.0,
				valueStreamingRatio:           1.0,
				valueCacheHitRatio:            0.0,
				valueTTFTSampleCount:          9.0,
				valueTTFTAverageMS:            100.0,
				valueTTFTP50MS:                90.0,
				valueTTFTP95MS:                150.0,
				valueTBTSampleCount:           8.0,
				valueTBTAverageMS:             15.0,
				valueTBTP50MS:                 14.0,
				valueTBTP90MS:                 19.0,
				valueTBTP95MS:                 20.0,
				valueTTLTAverageMS:            500.0,
				valueTTLTP50MS:                450.0,
				valueTTLTP95MS:                800.0,
				valueRepresentativeRequestIDs: []any{"legacy-request-1"},
				valueAPIPath:                  "/openai/chat/completions?api-version=secret",
			}}}, nil
		case strings.Contains(query.Text, "Category == 'RequestResponse'"):
			return logQueryResult{Rows: []map[string]any{{
				"outcome_rows":      9.0,
				valueErrorRate:      1.0 / 9.0,
				valueThrottlingRate: 0.0,
			}}}, nil
		default:
			return logQueryResult{}, errors.New("unexpected log query")
		}
	}}
	metrics := &fakeMetricsClient{
		results: map[metricAggregation]metricQueryResult{},
		errors: map[metricAggregation]error{
			metricAggregationTotal:   errors.New("metrics unavailable"),
			metricAggregationAverage: errors.New("metrics unavailable"),
		},
	}
	provider := &Provider{
		subscriptionID: testSubscription,
		cognitive:      &fakeCognitiveClient{metadata: completeMetadata()},
		logs:           logs,
		metricsFactory: func(string) (metricsClient, error) {
			return metrics, nil
		},
	}

	profile, err := provider.GetProfile(t.Context(), testReference(), testWindow)
	if err != nil {
		t.Fatalf("legacy fallback should build a profile: %v", err)
	}
	if profile.RequestCount == nil || *profile.RequestCount != 9 {
		t.Fatalf("legacy usage_rows was not mapped to request count: %v", profile.RequestCount)
	}
	if profile.DataAvailability[2].Status != model.AvailabilityAvailable {
		t.Fatalf("legacy logs should provide a complete log profile: %+v", profile.DataAvailability[2])
	}
	if profile.APIPath != "/openai/chat/completions" {
		t.Fatalf("legacy API path was not sanitized: %q", profile.APIPath)
	}
}

func TestClassifyDeployment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		sku            string
		tier           string
		offer          string
		billing        string
		serviceTier    string
		deploymentType string
	}{
		{
			name: "global standard", sku: "GlobalStandard", offer: "Standard PayGo",
			billing: "PayGo", serviceTier: "Default", deploymentType: "global_standard",
		},
		{
			name: "priority", sku: "DataZoneStandard", tier: "priority",
			offer: model.OfferPriorityProcessing, billing: "PayGo",
			serviceTier: "Priority", deploymentType: "data_zone_standard",
		},
		{
			name: "regional provisioned", sku: "ProvisionedManaged", offer: model.OfferPTUM,
			billing: "PTU", serviceTier: "Provisioned", deploymentType: "regional_provisioned",
		},
		{
			name: "batch", sku: "GlobalBatch", offer: "Batch",
			billing: "Batch", serviceTier: "Batch", deploymentType: "global_batch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			actual := classifyDeployment(test.sku, test.tier)
			if actual.offer != test.offer ||
				actual.billingModel != test.billing ||
				actual.serviceTier != test.serviceTier ||
				actual.deploymentType != test.deploymentType {
				t.Fatalf("unexpected classification: %+v", actual)
			}
		})
	}
}

func TestDedicatedLogQueryDerivesTBTWithoutRawContent(t *testing.T) {
	t.Parallel()
	start, end, err := parseWindow(testWindow)
	if err != nil {
		t.Fatal(err)
	}
	query, aliases := buildDedicatedLogQuery(
		"AzureOpenAIRequestUsage",
		map[string]string{
			"TimeGenerated":       "datetime",
			"ModelDeploymentName": "string",
			"GeneratedTokens":     "long",
			"IsStreaming":         "bool",
			"TimeToFirstTokenMs":  "real",
			"TimeToLastTokenMs":   "real",
		},
		accountResourceID(testSubscription, testResourceGroup, testAccount),
		testDeployment,
		start,
		end,
	)
	for _, alias := range []string{
		valueTBTSampleCount,
		valueTBTP50MS,
		valueTBTP90MS,
		valueTBTP95MS,
	} {
		if !slicesContains(aliases, alias) {
			t.Fatalf("derived TBT query omitted %q: %v", alias, aliases)
		}
	}
	if !strings.Contains(query, "TimeToLastTokenMs") ||
		!strings.Contains(query, "TimeToFirstTokenMs") ||
		!strings.Contains(query, "GeneratedTokens") {
		t.Fatal("derived TBT expression is incomplete")
	}
	for _, forbidden := range []string{"PromptText", "RequestBody", "ResponseBody"} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("query included raw content field %q", forbidden)
		}
	}
}

func completeMetadata() deploymentMetadata {
	return deploymentMetadata{
		ID: "/subscriptions/" + testSubscription +
			"/resourceGroups/" + testResourceGroup +
			"/providers/Microsoft.CognitiveServices/accounts/" + testAccount +
			"/deployments/" + testDeployment,
		ModelName:    "gpt-4.1-mini",
		ModelVersion: "2025-04-14",
		SKUName:      "GlobalStandard",
		Region:       "East US 2",
		RateLimits: []rateLimit{
			{Key: "request", Count: 13, PeriodSeconds: 60},
			{Key: "token", Count: 13000, PeriodSeconds: 60},
		},
	}
}

func testReference() model.DeploymentReference {
	return model.DeploymentReference{
		Subscription:   testSubscription,
		ResourceGroup:  testResourceGroup,
		AccountName:    testAccount,
		DeploymentName: testDeployment,
	}
}

func completeLogQuery(query logQuery) (logQueryResult, error) {
	if strings.Contains(query.Text, "AzureDiagnostics") {
		return logQueryResult{}, errors.New("legacy table is not configured")
	}
	if strings.Contains(query.Text, "getschema") {
		if strings.HasPrefix(query.Text, "AzureOpenAIRequestUsage") {
			return schemaResult([]string{
				"TimeGenerated",
				"_ResourceId",
				"ModelDeploymentName",
				"PromptTokens",
				"CompletionTokens",
				"IsStreaming",
				"CachedPromptTokens",
				"TimeToFirstTokenMs",
				"TimeToLastTokenMs",
				"StatusCode",
				"RequestId",
				"ApiPath",
			}), nil
		}
		return schemaResult([]string{
			"TimeGenerated",
			"_ResourceId",
			"ModelDeploymentName",
			"StatusCode",
			"RequestId",
			"ApiPath",
		}), nil
	}
	if strings.HasPrefix(query.Text, "AzureOpenAIRequestUsage") {
		return logQueryResult{Rows: []map[string]any{{ //nolint:gosec,lll // Credential fixture verifies redaction.
			valueRequestCount:             24.0,
			valueInputAverage:             18.0,
			valueInputP50:                 16.0,
			valueInputP95:                 32.0,
			valueOutputAverage:            6.0,
			valueOutputP50:                5.0,
			valueOutputP95:                12.0,
			valueStreamingRatio:           0.75,
			valueCacheHitRatio:            0.2,
			valueTTFTSampleCount:          23.0,
			valueTTFTAverageMS:            1181.0,
			valueTTFTP50MS:                1060.0,
			valueTTFTP95MS:                2027.0,
			valueTBTSampleCount:           20.0,
			valueTBTAverageMS:             24.0,
			valueTBTP50MS:                 21.0,
			valueTBTP90MS:                 31.0,
			valueTBTP95MS:                 35.0,
			valueTTLTAverageMS:            1244.0,
			valueTTLTP50MS:                1087.0,
			valueTTLTP95MS:                2064.0,
			valueErrorRate:                0.125,
			valueThrottlingRate:           0.025,
			valueRepresentativeRequestIDs: []any{"req-2", "req-1", "has space", "https://user:secret@example"},
			valueAPIPath:                  "https://user:secret@example.test/openai/deployments/example/chat/completions?sig=secret",
		}}}, nil
	}
	return logQueryResult{Rows: []map[string]any{{
		valueErrorRate:                0.125,
		valueThrottlingRate:           0.025,
		valueRepresentativeRequestIDs: []any{"req-1"},
		valueAPIPath:                  "/openai/deployments/example/chat/completions",
	}}}, nil
}

func schemaResult(columns []string) logQueryResult {
	rows := make([]map[string]any, 0, len(columns))
	for _, column := range columns {
		rows = append(rows, map[string]any{
			"ColumnName": column,
			"DataType":   "string",
		})
	}
	return logQueryResult{Rows: rows}
}

func assertFloat(t *testing.T, actual *float64, expected float64) {
	t.Helper()
	if actual == nil || math.Abs(*actual-expected) > 1e-9 {
		t.Fatalf("expected %g, got %v", expected, actual)
	}
}

func slicesContains(values []string, expected string) bool {
	return slices.Contains(values, expected)
}
