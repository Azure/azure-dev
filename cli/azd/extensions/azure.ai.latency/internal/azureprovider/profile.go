// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureprovider

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"azure.ai.latency/internal/model"
)

const (
	metricRequests = "AzureOpenAIRequests"
	metricInput    = "ProcessedPromptTokens"
	metricOutput   = "GeneratedTokens"
	metricTTFT     = "AzureOpenAINormalizedTTFTInMS"
	metricTBT      = "AzureOpenAINormalizedTBTInMS"
	metricTTLT     = "AzureOpenAITTLTInMS"
)

const (
	valueInputAverage     = "input_average"
	valueInputP50         = "input_p50"
	valueInputP95         = "input_p95"
	valueOutputAverage    = "output_average"
	valueOutputP50        = "output_p50"
	valueOutputP95        = "output_p95"
	valueStreamingRatio   = "streaming_ratio"
	valueCacheHitRatio    = "cache_hit_ratio"
	valueRequestCount     = "request_count"
	valueAverageRPM       = "average_rpm"
	valuePeakRPM          = "peak_rpm"
	valueInputAverageTPM  = "input_average_tpm"
	valueInputPeakTPM     = "input_peak_tpm"
	valueOutputAverageTPM = "output_average_tpm"
	valueOutputPeakTPM    = "output_peak_tpm"
	valueAverageTPM       = "average_tpm"
	valuePeakTPM          = "peak_tpm"
	valueTTFTAverageMS    = "ttft_average_ms"
	valueTTFTSampleCount  = "ttft_sample_count"
	valueTTFTP50MS        = "ttft_p50_ms"
	valueTTFTP95MS        = "ttft_p95_ms"
	valueTBTAverageMS     = "tbt_average_ms"
	valueTBTSampleCount   = "tbt_sample_count"
	valueTBTP50MS         = "tbt_p50_ms"
	valueTBTP90MS         = "tbt_p90_ms"
	valueTBTP95MS         = "tbt_p95_ms"
	valueTTLTAverageMS    = "ttlt_average_ms"
	valueTTLTP50MS        = "ttlt_p50_ms"
	valueTTLTP95MS        = "ttlt_p95_ms"
	valueErrorRate        = "error_rate"
	valueThrottlingRate   = "throttling_rate"
)

// GetProfile loads ARM metadata and the available monitoring data for a deployment.
func (p *Provider) GetProfile(
	ctx context.Context,
	reference model.DeploymentReference,
	window model.TimeWindow,
) (model.TrafficProfile, error) {
	if p == nil || p.cognitive == nil {
		return model.TrafficProfile{}, fmt.Errorf("Azure provider is not initialized")
	}
	if err := p.validateReference(reference); err != nil {
		return model.TrafficProfile{}, err
	}
	start, end, err := parseWindow(window)
	if err != nil {
		return model.TrafficProfile{}, err
	}

	metadata, err := p.cognitive.GetDeploymentMetadata(
		ctx,
		strings.TrimSpace(reference.ResourceGroup),
		strings.TrimSpace(reference.AccountName),
		strings.TrimSpace(reference.DeploymentName),
	)
	if err != nil {
		return model.TrafficProfile{}, safeServiceError("read Azure OpenAI deployment metadata", err)
	}

	deployment := p.deploymentContext(reference, metadata)
	accountID := accountResourceID(
		p.subscriptionID,
		reference.ResourceGroup,
		reference.AccountName,
	)
	metricValues, metricAvailability, err := p.collectMetrics(
		ctx,
		accountID,
		deployment,
		start,
		end,
	)
	if err != nil {
		return model.TrafficProfile{}, err
	}
	logValues, logAvailability, err := p.collectLogs(
		ctx,
		accountID,
		deployment.DeploymentName,
		start,
		end,
	)
	if err != nil {
		return model.TrafficProfile{}, err
	}

	values := metricValues
	values.overlay(logValues)
	return buildProfile(
		deployment,
		values,
		armAvailability(deployment),
		metricAvailability,
		logAvailability,
	), nil
}

func (p *Provider) validateReference(reference model.DeploymentReference) error {
	if reference.Subscription != "" && !strings.EqualFold(strings.TrimSpace(reference.Subscription), p.subscriptionID) {
		return fmt.Errorf("deployment subscription does not match the provider subscription")
	}
	missing := []string{}
	if strings.TrimSpace(reference.ResourceGroup) == "" {
		missing = append(missing, "resource group")
	}
	if strings.TrimSpace(reference.AccountName) == "" {
		missing = append(missing, "account name")
	}
	if strings.TrimSpace(reference.DeploymentName) == "" {
		missing = append(missing, "deployment name")
	}
	if len(missing) > 0 {
		return fmt.Errorf("deployment reference is missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func parseWindow(window model.TimeWindow) (time.Time, time.Time, error) {
	start, err := time.Parse(time.RFC3339, strings.TrimSpace(window.StartTime))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("time window start must be RFC3339")
	}
	end, err := time.Parse(time.RFC3339, strings.TrimSpace(window.EndTime))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("time window end must be RFC3339")
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("time window end must be after its start")
	}
	return start.UTC(), end.UTC(), nil
}

func (p *Provider) deploymentContext(
	reference model.DeploymentReference,
	metadata deploymentMetadata,
) model.DeploymentContext {
	terms := classifyDeployment(metadata.SKUName, metadata.ServiceTier)
	deploymentID := strings.TrimSpace(metadata.ID)
	if deploymentID == "" {
		deploymentID = strings.TrimSpace(reference.DeploymentID)
	}
	if deploymentID == "" {
		deploymentID = fmt.Sprintf(
			"%s/deployments/%s",
			accountResourceID(p.subscriptionID, reference.ResourceGroup, reference.AccountName),
			strings.TrimSpace(reference.DeploymentName),
		)
	}
	return model.DeploymentContext{
		DeploymentID:   deploymentID,
		SubscriptionID: p.subscriptionID,
		ResourceGroup:  strings.TrimSpace(reference.ResourceGroup),
		AccountName:    strings.TrimSpace(reference.AccountName),
		DeploymentName: strings.TrimSpace(reference.DeploymentName),
		Model:          strings.TrimSpace(metadata.ModelName),
		ModelVersion:   strings.TrimSpace(metadata.ModelVersion),
		Offer:          terms.offer,
		BillingModel:   terms.billingModel,
		ServiceTier:    terms.serviceTier,
		DeploymentType: terms.deploymentType,
		SKUName:        strings.TrimSpace(metadata.SKUName),
		Region:         strings.TrimSpace(metadata.Region),
		TPMRateLimit:   rateLimitPerMinute(metadata.RateLimits, "token"),
		RPMRateLimit:   rateLimitPerMinute(metadata.RateLimits, "request"),
	}
}

func accountResourceID(subscriptionID, resourceGroup, accountName string) string {
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s",
		strings.TrimSpace(subscriptionID),
		strings.TrimSpace(resourceGroup),
		strings.TrimSpace(accountName),
	)
}

type deploymentTerms struct {
	offer          string
	billingModel   string
	serviceTier    string
	deploymentType string
}

func classifyDeployment(skuName, serviceTier string) deploymentTerms {
	terms, ok := map[string]deploymentTerms{
		"globalstandard": {
			offer: "Standard PayGo", billingModel: "PayGo", serviceTier: "Default",
			deploymentType: "global_standard",
		},
		"datazonestandard": {
			offer: "Standard PayGo", billingModel: "PayGo", serviceTier: "Default",
			deploymentType: "data_zone_standard",
		},
		"standard": {
			offer: "Standard PayGo", billingModel: "PayGo", serviceTier: "Default",
			deploymentType: "regional_standard",
		},
		"globalprovisionedmanaged": {
			offer: model.OfferPTUM, billingModel: "PTU", serviceTier: "Provisioned",
			deploymentType: "global_provisioned",
		},
		"datazoneprovisionedmanaged": {
			offer: model.OfferPTUM, billingModel: "PTU", serviceTier: "Provisioned",
			deploymentType: "data_zone_provisioned",
		},
		"provisionedmanaged": {
			offer: model.OfferPTUM, billingModel: "PTU", serviceTier: "Provisioned",
			deploymentType: "regional_provisioned",
		},
		"globalbatch": {
			offer: "Batch", billingModel: "Batch", serviceTier: "Batch",
			deploymentType: "global_batch",
		},
		"datazonebatch": {
			offer: "Batch", billingModel: "Batch", serviceTier: "Batch",
			deploymentType: "data_zone_batch",
		},
		"developertier": {
			offer: "Developer PayGo", billingModel: "PayGo", serviceTier: "Default",
			deploymentType: "developer",
		},
	}[strings.ToLower(strings.TrimSpace(skuName))]
	if !ok {
		return deploymentTerms{offer: strings.TrimSpace(skuName)}
	}

	if terms.billingModel == "PayGo" {
		switch strings.ToLower(strings.TrimSpace(serviceTier)) {
		case "priority":
			terms.offer = model.OfferPriorityProcessing
			terms.serviceTier = "Priority"
		}
	}
	return terms
}

func rateLimitPerMinute(limits []rateLimit, key string) *float64 {
	for _, limit := range limits {
		normalized := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(limit.Key)), "s")
		if normalized != strings.ToLower(key) || limit.Count < 0 || limit.PeriodSeconds <= 0 {
			continue
		}
		value := limit.Count * 60 / limit.PeriodSeconds
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil
		}
		return model.Float64(value)
	}
	return nil
}

func (p *Provider) collectMetrics(
	ctx context.Context,
	resourceID string,
	deployment model.DeploymentContext,
	start time.Time,
	end time.Time,
) (observedValues, model.DataAvailability, error) {
	values := newObservedValues()
	availability := model.DataAvailability{
		Source: "Azure Monitor platform metrics",
		Fields: []string{},
	}
	endpoint, err := metricsEndpoint(deployment.Region)
	if err != nil || p.metricsFactory == nil {
		availability.Status = model.AvailabilityUnavailable
		availability.Note = "Azure Monitor metrics are unavailable because the regional endpoint could not be resolved."
		return values, availability, nil
	}
	client, err := p.metricsFactory(endpoint)
	if err != nil {
		availability.Status = model.AvailabilityUnavailable
		availability.Note = "Azure Monitor metrics could not be queried. Affected values remain unavailable."
		return values, availability, nil
	}

	totals, totalErr := client.Query(ctx, metricQuery{
		SubscriptionID: p.subscriptionID,
		ResourceID:     resourceID,
		DeploymentName: deployment.DeploymentName,
		Names:          []string{metricRequests, metricInput, metricOutput},
		Aggregation:    metricAggregationTotal,
		Start:          start,
		End:            end,
	})
	if ctxErr := contextError(ctx, totalErr); ctxErr != nil {
		return values, availability, ctxErr
	}
	averages, averageErr := client.Query(ctx, metricQuery{
		SubscriptionID: p.subscriptionID,
		ResourceID:     resourceID,
		DeploymentName: deployment.DeploymentName,
		Names:          []string{metricTTFT, metricTBT, metricTTLT},
		Aggregation:    metricAggregationAverage,
		Start:          start,
		End:            end,
	})
	if ctxErr := contextError(ctx, averageErr); ctxErr != nil {
		return values, availability, ctxErr
	}

	if totalErr == nil {
		addMetricTotals(&values, totals)
	}
	if averageErr == nil {
		addMetricAverages(&values, averages)
	}
	availability.Fields = metricAvailabilityFields(values)
	partialQuery := totalErr != nil || averageErr != nil || totals.Partial || averages.Partial
	availability.Status = availabilityStatus(availability.Fields, 13, partialQuery)
	switch {
	case totalErr != nil && averageErr != nil:
		availability.Note = "Azure Monitor metrics could not be queried. Affected values remain unavailable."
	case partialQuery:
		availability.Note = "Some Azure Monitor metric groups could not be queried; available aggregates are included."
	case len(availability.Fields) == 0:
		availability.Note = "No matching Azure Monitor metric points were found for this deployment and time range."
	case availability.Status == model.AvailabilityPartial:
		availability.Note = "Azure Monitor metrics were queried, but some aggregates had no data points."
	default:
		availability.Note = "One-minute resource aggregates were queried through the Azure Monitor Metrics SDK."
	}
	return values, availability, nil
}

func metricsEndpoint(region string) (string, error) {
	var normalized strings.Builder
	for _, character := range strings.ToLower(region) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			normalized.WriteRune(character)
		}
	}
	if normalized.Len() == 0 {
		return "", fmt.Errorf("account region is unavailable")
	}
	return "https://" + normalized.String() + ".metrics.monitor.azure.com", nil
}

func addMetricTotals(values *observedValues, result metricQueryResult) {
	requests := metricSeries(result, metricRequests)
	input := metricSeries(result, metricInput)
	output := metricSeries(result, metricOutput)
	if len(requests) > 0 {
		total := seriesSum(requests)
		values.numeric[valueRequestCount] = total
		values.numeric[valueAverageRPM] = seriesMean(requests)
		values.numeric[valuePeakRPM] = seriesMaximum(requests)
		if total > 0 && len(input) > 0 {
			values.numeric[valueInputAverage] = seriesSum(input) / total
		}
		if total > 0 && len(output) > 0 {
			values.numeric[valueOutputAverage] = seriesSum(output) / total
		}
	}
	if len(input) > 0 {
		values.numeric[valueInputAverageTPM] = seriesMean(input)
		values.numeric[valueInputPeakTPM] = seriesMaximum(input)
	}
	if len(output) > 0 {
		values.numeric[valueOutputAverageTPM] = seriesMean(output)
		values.numeric[valueOutputPeakTPM] = seriesMaximum(output)
	}
	combined := combineMetricSeries(input, output)
	if len(combined) > 0 {
		values.numeric[valueAverageTPM] = seriesMean(combined)
		values.numeric[valuePeakTPM] = seriesMaximum(combined)
	}
}

func addMetricAverages(values *observedValues, result metricQueryResult) {
	for key, metricName := range map[string]string{
		valueTTFTAverageMS: metricTTFT,
		valueTBTAverageMS:  metricTBT,
		valueTTLTAverageMS: metricTTLT,
	} {
		if average, ok := meanPositive(metricSeries(result, metricName)); ok {
			values.numeric[key] = average
		}
	}
}

func metricSeries(result metricQueryResult, name string) []metricPoint {
	for metricName, series := range result.Series {
		if strings.EqualFold(metricName, name) {
			return series
		}
	}
	return nil
}

func combineMetricSeries(left, right []metricPoint) []metricPoint {
	if hasTimestamps(left) && hasTimestamps(right) {
		byTime := map[time.Time]float64{}
		for _, point := range left {
			byTime[point.Time] += point.Value
		}
		for _, point := range right {
			byTime[point.Time] += point.Value
		}
		timestamps := make([]time.Time, 0, len(byTime))
		for timestamp := range byTime {
			timestamps = append(timestamps, timestamp)
		}
		slices.SortFunc(timestamps, func(a, b time.Time) int { return a.Compare(b) })
		result := make([]metricPoint, 0, len(timestamps))
		for _, timestamp := range timestamps {
			result = append(result, metricPoint{Time: timestamp, Value: byTime[timestamp]})
		}
		return result
	}

	result := make([]metricPoint, max(len(left), len(right)))
	for index := range result {
		if index < len(left) {
			result[index].Value += left[index].Value
		}
		if index < len(right) {
			result[index].Value += right[index].Value
		}
	}
	return result
}

func hasTimestamps(values []metricPoint) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if value.Time.IsZero() {
			return false
		}
	}
	return true
}

func seriesSum(values []metricPoint) float64 {
	total := 0.0
	for _, value := range values {
		total += value.Value
	}
	return total
}

func seriesMean(values []metricPoint) float64 {
	return seriesSum(values) / float64(len(values))
}

func seriesMaximum(values []metricPoint) float64 {
	maximum := values[0].Value
	for _, value := range values[1:] {
		maximum = max(maximum, value.Value)
	}
	return maximum
}

func meanPositive(values []metricPoint) (float64, bool) {
	total := 0.0
	count := 0
	for _, value := range values {
		if value.Value > 0 && !math.IsNaN(value.Value) && !math.IsInf(value.Value, 0) {
			total += value.Value
			count++
		}
	}
	if count == 0 {
		return 0, false
	}
	return total / float64(count), true
}

func metricAvailabilityFields(values observedValues) []string {
	definitions := []struct {
		label string
		keys  []string
	}{
		{"Average input tokens", []string{valueInputAverage}},
		{"Average output tokens", []string{valueOutputAverage}},
		{"Request count", []string{valueRequestCount}},
		{"Request rate", []string{valueAverageRPM, valuePeakRPM}},
		{"Average TPM", []string{valueAverageTPM}},
		{"Peak TPM", []string{valuePeakTPM}},
		{"Average input TPM", []string{valueInputAverageTPM}},
		{"Peak input TPM", []string{valueInputPeakTPM}},
		{"Average output TPM", []string{valueOutputAverageTPM}},
		{"Peak output TPM", []string{valueOutputPeakTPM}},
		{"Average TTFT", []string{valueTTFTAverageMS}},
		{"Average TBT", []string{valueTBTAverageMS}},
		{"Average TTLT", []string{valueTTLTAverageMS}},
	}
	return availableFields(values, definitions)
}

func availableFields(
	values observedValues,
	definitions []struct {
		label string
		keys  []string
	},
) []string {
	fields := []string{}
	for _, definition := range definitions {
		available := true
		for _, key := range definition.keys {
			if _, ok := values.numeric[key]; !ok {
				available = false
				break
			}
		}
		if available {
			fields = append(fields, definition.label)
		}
	}
	return fields
}

func buildProfile(
	deployment model.DeploymentContext,
	values observedValues,
	arm model.DataAvailability,
	metrics model.DataAvailability,
	logs model.DataAvailability,
) model.TrafficProfile {
	requestRate := model.RequestRate{
		AverageRPM: values.number(valueAverageRPM),
		PeakRPM:    values.number(valuePeakRPM),
	}
	if requestRate.AverageRPM != nil && requestRate.PeakRPM != nil && *requestRate.AverageRPM > 0 {
		requestRate.BurstFactor = model.Float64(*requestRate.PeakRPM / *requestRate.AverageRPM)
	}
	peakTPM := values.number(valuePeakTPM)
	tokenRate := model.TokenRate{
		InputAverageTPM:  values.number(valueInputAverageTPM),
		InputPeakTPM:     values.number(valueInputPeakTPM),
		OutputAverageTPM: values.number(valueOutputAverageTPM),
		OutputPeakTPM:    values.number(valueOutputPeakTPM),
		AverageTPM:       values.number(valueAverageTPM),
		PeakTPM:          peakTPM,
		LimitTPM:         deployment.TPMRateLimit,
	}
	if peakTPM != nil && deployment.TPMRateLimit != nil && *deployment.TPMRateLimit > 0 {
		tokenRate.PeakLimitRatio = model.Float64(*peakTPM / *deployment.TPMRateLimit)
	}

	return model.TrafficProfile{
		Deployment: deployment,
		InputTokens: model.Distribution{
			Average: values.number(valueInputAverage),
			P50:     values.number(valueInputP50),
			P95:     values.number(valueInputP95),
			Unit:    "tokens",
		},
		OutputTokens: model.Distribution{
			Average: values.number(valueOutputAverage),
			P50:     values.number(valueOutputP50),
			P95:     values.number(valueOutputP95),
			Unit:    "tokens",
		},
		StreamingRatio: values.number(valueStreamingRatio),
		CacheHitRatio:  values.number(valueCacheHitRatio),
		RequestRate:    requestRate,
		TokenRate:      tokenRate,
		RequestCount:   integerValue(values, valueRequestCount),
		TTFTSampleCount: integerValue(
			values,
			valueTTFTSampleCount,
		),
		TBTSampleCount: integerValue(values, valueTBTSampleCount),
		Latency: map[string]model.LatencyDistribution{
			"ttft": {
				AverageMS: values.number(valueTTFTAverageMS),
				P50MS:     values.number(valueTTFTP50MS),
				P95MS:     values.number(valueTTFTP95MS),
			},
			"tbt": {
				AverageMS: values.number(valueTBTAverageMS),
				P50MS:     values.number(valueTBTP50MS),
				P90MS:     values.number(valueTBTP90MS),
				P95MS:     values.number(valueTBTP95MS),
			},
			"ttlt": {
				AverageMS: values.number(valueTTLTAverageMS),
				P50MS:     values.number(valueTTLTP50MS),
				P95MS:     values.number(valueTTLTP95MS),
			},
		},
		ErrorRate:                values.number(valueErrorRate),
		ThrottlingRate:           values.number(valueThrottlingRate),
		APIPath:                  values.apiPath,
		RepresentativeRequestIDs: slices.Clone(values.requestIDs),
		DataAvailability:         []model.DataAvailability{arm, metrics, logs},
	}
}

func integerValue(values observedValues, key string) *int64 {
	value, ok := values.numeric[key]
	if !ok || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return model.Int64(int64(math.Round(value)))
}

func armAvailability(deployment model.DeploymentContext) model.DataAvailability {
	fields := []string{}
	for _, field := range []struct {
		name      string
		available bool
	}{
		{"Model", deployment.Model != ""},
		{"Model version", deployment.ModelVersion != ""},
		{"Offer", deployment.Offer != ""},
		{"Billing model", deployment.BillingModel != ""},
		{"Service tier", deployment.ServiceTier != ""},
		{"Deployment type", deployment.DeploymentType != ""},
		{"ARM SKU", deployment.SKUName != ""},
		{"Region", deployment.Region != ""},
		{"TPM rate limit", deployment.TPMRateLimit != nil},
		{"RPM rate limit", deployment.RPMRateLimit != nil},
	} {
		if field.available {
			fields = append(fields, field.name)
		}
	}
	status := availabilityStatus(fields, 10, false)
	note := "Deployment metadata was read through the Cognitive Services management SDK."
	if status != model.AvailabilityAvailable {
		note = "Deployment metadata was read, but some optional ARM fields are unavailable."
	}
	return model.DataAvailability{
		Source: "ARM deployment metadata",
		Status: status,
		Fields: fields,
		Note:   note,
	}
}

func availabilityStatus(fields []string, expected int, forcePartial bool) string {
	if len(fields) == 0 {
		return model.AvailabilityUnavailable
	}
	if forcePartial || len(fields) < expected {
		return model.AvailabilityPartial
	}
	return model.AvailabilityAvailable
}

func contextError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}
