// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"azure.ai.latency/internal/model"
)

const (
	valueAPIPath                  = "api_path"
	valueRepresentativeRequestIDs = "representative_request_ids"
)

var (
	safeColumnName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	safeRequestID  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

func (p *Provider) collectLogs(
	ctx context.Context,
	resourceID string,
	deploymentName string,
	start time.Time,
	end time.Time,
) (observedValues, model.DataAvailability, error) {
	values := newObservedValues()
	availability := model.DataAvailability{
		Source: "Log Analytics request logs",
		Fields: []string{},
	}
	if p.logs == nil {
		availability.Status = model.AvailabilityUnavailable
		availability.Note = "Request logs could not be queried. Confirm diagnostic settings and query permissions."
		return values, availability, nil
	}

	hadSuccessfulQuery := false
	partialResponse := false
	aggregationFailure := false
	for _, table := range []string{"AzureOpenAIRequestUsage", "RequestResponse"} {
		schema, err := p.logs.Query(ctx, logQuery{
			ResourceID: resourceID,
			Text:       table + " | getschema | project ColumnName, DataType",
			Start:      start,
			End:        end,
		})
		if ctxErr := contextError(ctx, err); ctxErr != nil {
			return values, availability, ctxErr
		}
		if err != nil {
			// A table-not-found response is expected when the workspace uses
			// AzureDiagnostics rather than resource-specific tables.
			continue
		}
		hadSuccessfulQuery = true
		partialResponse = partialResponse || schema.Partial
		columns := schemaColumns(schema.Rows)
		if len(columns) == 0 {
			continue
		}
		query, aliases := buildDedicatedLogQuery(
			table,
			columns,
			resourceID,
			deploymentName,
			start,
			end,
		)
		if query == "" || len(aliases) == 0 {
			continue
		}
		result, queryErr := p.logs.Query(ctx, logQuery{
			ResourceID: resourceID,
			Text:       query,
			Start:      start,
			End:        end,
		})
		if ctxErr := contextError(ctx, queryErr); ctxErr != nil {
			return values, availability, ctxErr
		}
		if queryErr != nil {
			aggregationFailure = true
			continue
		}
		hadSuccessfulQuery = true
		partialResponse = partialResponse || result.Partial
		if len(result.Rows) > 0 {
			setLogValues(&values, result.Rows[0], aliases)
		}
	}

	legacyFailed := false
	usageResult, usageErr := p.logs.Query(ctx, logQuery{
		ResourceID: resourceID,
		Text:       buildLegacyUsageQuery(resourceID, deploymentName, start, end),
		Start:      start,
		End:        end,
	})
	if ctxErr := contextError(ctx, usageErr); ctxErr != nil {
		return values, availability, ctxErr
	}
	if usageErr != nil {
		legacyFailed = true
	} else {
		hadSuccessfulQuery = true
		partialResponse = partialResponse || usageResult.Partial
		if len(usageResult.Rows) > 0 && positiveNumber(usageResult.Rows[0]["usage_rows"]) {
			usageResult.Rows[0][valueRequestCount] = usageResult.Rows[0]["usage_rows"]
			aliases := append([]string{valueRequestCount}, legacyUsageFields...)
			aliases = append(aliases, valueRepresentativeRequestIDs, valueAPIPath)
			setLogValues(&values, usageResult.Rows[0], aliases)
		}
	}

	outcomeResult, outcomeErr := p.logs.Query(ctx, logQuery{
		ResourceID: resourceID,
		Text:       buildLegacyOutcomeQuery(resourceID, deploymentName, start, end),
		Start:      start,
		End:        end,
	})
	if ctxErr := contextError(ctx, outcomeErr); ctxErr != nil {
		return values, availability, ctxErr
	}
	if outcomeErr != nil {
		legacyFailed = true
	} else {
		hadSuccessfulQuery = true
		partialResponse = partialResponse || outcomeResult.Partial
		if len(outcomeResult.Rows) > 0 && positiveNumber(outcomeResult.Rows[0]["outcome_rows"]) {
			setLogValues(&values, outcomeResult.Rows[0], []string{
				valueErrorRate,
				valueThrottlingRate,
				valueRepresentativeRequestIDs,
				valueAPIPath,
			})
		}
	}

	availability.Fields = logAvailabilityFields(values)
	incomplete := len(availability.Fields) < 12
	forcePartial := partialResponse || aggregationFailure || legacyFailed && incomplete
	availability.Status = availabilityStatus(availability.Fields, 12, forcePartial)
	switch {
	case !hadSuccessfulQuery:
		availability.Note = "Request logs could not be queried. Confirm diagnostic settings and query permissions."
	case len(availability.Fields) == 0 && (aggregationFailure || legacyFailed):
		availability.Note = "Request-log aggregate queries failed. Confirm diagnostic settings and query permissions."
	case len(availability.Fields) == 0:
		availability.Note = "No matching request-level records were found for this deployment and time range."
	case forcePartial:
		availability.Note = "Some request-log sources or fields were unavailable; available aggregates are included."
	case availability.Status == model.AvailabilityPartial:
		availability.Note = "Request logs were queried, but some request-level fields were not emitted."
	default:
		availability.Note = "Request-level aggregates were computed through the Azure Monitor Logs SDK."
	}
	return values, availability, nil
}

func schemaColumns(rows []map[string]any) map[string]string {
	columns := map[string]string{}
	for _, row := range rows {
		name := stringValue(rowValue(row, "ColumnName"))
		if !safeColumnName.MatchString(name) {
			continue
		}
		columns[name] = stringValue(rowValue(row, "DataType"))
	}
	return columns
}

func rowValue(row map[string]any, name string) any {
	for key, value := range row {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return nil
}

func setLogValues(destination *observedValues, row map[string]any, aliases []string) {
	for _, alias := range aliases {
		value := rowValue(row, alias)
		switch alias {
		case valueRepresentativeRequestIDs:
			if len(destination.requestIDs) == 0 {
				destination.requestIDs = normalizeRequestIDs(value)
			}
		case valueAPIPath:
			if destination.apiPath == "" {
				destination.apiPath = sanitizeAPIPath(stringValue(value))
			}
		default:
			if _, exists := destination.numeric[alias]; exists {
				continue
			}
			if numeric, ok := finiteNumber(value); ok {
				destination.numeric[alias] = numeric
			}
		}
	}
}

func finiteNumber(value any) (float64, bool) {
	var numeric float64
	switch item := value.(type) {
	case float64:
		numeric = item
	case float32:
		numeric = float64(item)
	case int:
		numeric = float64(item)
	case int32:
		numeric = float64(item)
	case int64:
		numeric = float64(item)
	case json.Number:
		parsed, err := item.Float64()
		if err != nil {
			return 0, false
		}
		numeric = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(item), 64)
		if err != nil {
			return 0, false
		}
		numeric = parsed
	default:
		return 0, false
	}
	if math.IsNaN(numeric) || math.IsInf(numeric, 0) {
		return 0, false
	}
	return numeric, true
}

func positiveNumber(value any) bool {
	number, ok := finiteNumber(value)
	return ok && number > 0
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func normalizeRequestIDs(value any) []string {
	candidates := []string{}
	switch item := value.(type) {
	case []any:
		for _, candidate := range item {
			candidates = append(candidates, stringValue(candidate))
		}
	case []string:
		candidates = append(candidates, item...)
	case string:
		candidates = append(candidates, item)
	}
	unique := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if !safeRequestID.MatchString(candidate) || strings.Contains(candidate, "://") {
			continue
		}
		unique[candidate] = struct{}{}
	}
	result := slices.Sorted(maps.Keys(unique))
	if len(result) > 3 {
		result = result[:3]
	}
	return result
}

func sanitizeAPIPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := url.Parse(value); err == nil && parsed.IsAbs() {
		value = parsed.EscapedPath()
	}
	if cut, _, found := strings.Cut(value, "?"); found {
		value = cut
	}
	if cut, _, found := strings.Cut(value, "#"); found {
		value = cut
	}
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 || strings.Contains(value, "@") {
		return ""
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return ""
		}
	}
	return value
}

func logAvailabilityFields(values observedValues) []string {
	fields := availableFields(values, []struct {
		label string
		keys  []string
	}{
		{"Input token distribution", []string{valueInputP50, valueInputP95}},
		{"Output token distribution", []string{valueOutputP50, valueOutputP95}},
		{"Streaming ratio", []string{valueStreamingRatio}},
		{"Cache usage", []string{valueCacheHitRatio}},
		{"Request count", []string{valueRequestCount}},
		{"TTFT P50/P95", []string{valueTTFTP50MS, valueTTFTP95MS}},
		{"TBT P50/P90/P95", []string{valueTBTP50MS, valueTBTP90MS, valueTBTP95MS}},
		{"TTLT P50/P95", []string{valueTTLTP50MS, valueTTLTP95MS}},
		{"Error rate", []string{valueErrorRate}},
		{"Throttling rate", []string{valueThrottlingRate}},
	})
	if values.apiPath != "" {
		fields = append(fields, "API path")
	}
	if len(values.requestIDs) > 0 {
		fields = append(fields, "Representative request IDs")
	}
	return fields
}

var legacyUsageFields = []string{
	valueInputAverage,
	valueInputP50,
	valueInputP95,
	valueOutputAverage,
	valueOutputP50,
	valueOutputP95,
	valueStreamingRatio,
	valueCacheHitRatio,
	valueTTFTAverageMS,
	valueTTFTSampleCount,
	valueTTFTP50MS,
	valueTTFTP95MS,
	valueTBTAverageMS,
	valueTBTSampleCount,
	valueTBTP50MS,
	valueTBTP90MS,
	valueTBTP95MS,
	valueTTLTAverageMS,
	valueTTLTP50MS,
	valueTTLTP95MS,
}

func buildLegacyUsageQuery(resourceID, deploymentName string, start, end time.Time) string {
	return fmt.Sprintf(`AzureDiagnostics
| where TimeGenerated between(datetime(%s) .. datetime(%s))
| where tolower(_ResourceId) == tolower('%s')
| where Category == 'AzureOpenAIRequestUsage'
| extend P=todynamic(properties_s)
| where tostring(P.modelDeploymentName) == '%s'
| extend InputTokens=iff(gettype(P.promptTokens) == 'array',
    todouble(array_sum(P.promptTokens)), todouble(P.promptTokens)),
    CachedTokens=iff(gettype(P.cachedTokens) == 'array',
    todouble(array_sum(P.cachedTokens)), todouble(P.cachedTokens)),
    OutputTokens=iff(gettype(P.generatedTokens) == 'array',
    todouble(array_sum(P.generatedTokens)), todouble(P.generatedTokens)),
    StreamType=tostring(P.streamType),
    TTFT=todouble(P.timeToFirstTokenMs),
    TTLT=todouble(P.timeToLastTokenMs),
    RequestID=coalesce(tostring(P.requestId), tostring(P.request_id), tostring(P.correlationId)),
    APIPath=coalesce(tostring(P.apiPath), tostring(P.api_path), tostring(P.operationName))
| extend RequestMeanTBT=iff(
    tolower(StreamType) in ('streaming', 'true', '1')
    and isfinite(OutputTokens) and OutputTokens > 1
    and isfinite(TTFT) and isfinite(TTLT) and TTLT >= TTFT,
    (TTLT - TTFT) / (OutputTokens - 1), real(null))
| summarize usage_rows=count(),
    ttft_sample_count=countif(isfinite(TTFT)),
    tbt_sample_count=countif(isfinite(RequestMeanTBT)),
    input_average=avg(InputTokens),
    input_p50=percentile(InputTokens, 50),
    input_p95=percentile(InputTokens, 95),
    output_average=avg(OutputTokens),
    output_p50=percentile(OutputTokens, 50),
    output_p95=percentile(OutputTokens, 95),
    streaming_ratio=todouble(countif(tolower(StreamType) in ('streaming', 'true', '1'))) / count(),
    CachedTotal=sum(CachedTokens),
    InputTotal=sum(InputTokens),
    ttft_average_ms=avg(TTFT),
    ttft_p50_ms=percentile(TTFT, 50),
    ttft_p95_ms=percentile(TTFT, 95),
    tbt_average_ms=avg(RequestMeanTBT),
    tbt_p50_ms=percentile(RequestMeanTBT, 50),
    tbt_p90_ms=percentile(RequestMeanTBT, 90),
    tbt_p95_ms=percentile(RequestMeanTBT, 95),
    ttlt_average_ms=avg(TTLT),
    ttlt_p50_ms=percentile(TTLT, 50),
    ttlt_p95_ms=percentile(TTLT, 95),
    representative_request_ids=make_set_if(RequestID, isnotempty(RequestID), 3),
    api_path=take_anyif(APIPath, isnotempty(APIPath))
| extend cache_hit_ratio=iff(InputTotal > 0, CachedTotal / InputTotal, real(null))
| project-away CachedTotal, InputTotal`,
		start.UTC().Format(time.RFC3339),
		end.UTC().Format(time.RFC3339),
		escapeKQLString(resourceID),
		escapeKQLString(deploymentName),
	)
}

func buildLegacyOutcomeQuery(resourceID, deploymentName string, start, end time.Time) string {
	return fmt.Sprintf(`AzureDiagnostics
| where TimeGenerated between(datetime(%s) .. datetime(%s))
| where tolower(_ResourceId) == tolower('%s')
| where Category == 'RequestResponse'
| extend P=todynamic(properties_s)
| extend Deployment=coalesce(tostring(P.modelDeploymentName), tostring(P.model_deployment_name)),
    RequestID=coalesce(tostring(P.requestId), tostring(P.request_id), tostring(P.correlationId)),
    APIPath=coalesce(tostring(P.apiPath), tostring(P.api_path), tostring(P.operationName))
| where Deployment == '%s'
| summarize outcome_rows=count(),
    error_rate=iff(count() > 0, todouble(countif(toint(httpStatusCode_d) >= 400)) / count(), real(null)),
    throttling_rate=iff(count() > 0, todouble(countif(toint(httpStatusCode_d) == 429)) / count(), real(null)),
    representative_request_ids=make_set_if(RequestID, isnotempty(RequestID), 3),
    api_path=take_anyif(APIPath, isnotempty(APIPath))`,
		start.UTC().Format(time.RFC3339),
		end.UTC().Format(time.RFC3339),
		escapeKQLString(resourceID),
		escapeKQLString(deploymentName),
	)
}

func buildDedicatedLogQuery(
	table string,
	columns map[string]string,
	resourceID string,
	deploymentName string,
	start time.Time,
	end time.Time,
) (string, []string) {
	if table != "AzureOpenAIRequestUsage" && table != "RequestResponse" {
		return "", nil
	}
	column := func(names ...string) string {
		for _, candidate := range names {
			for actual := range columns {
				if strings.EqualFold(actual, candidate) && safeColumnName.MatchString(actual) {
					return actual
				}
			}
		}
		return ""
	}
	expression := func(name string) string {
		if name == "" {
			return ""
		}
		return "['" + name + "']"
	}

	input := column("PromptTokens", "InputTokens", "InputTokenCount", "TotalPromptTokens")
	output := column("CompletionTokens", "OutputTokens", "GeneratedTokens", "OutputTokenCount")
	streaming := column("IsStreaming", "Streaming", "Stream")
	cachedTokens := column("CachedPromptTokens", "PromptCacheHitTokens", "CachedTokens")
	cacheHit := column("CacheHit", "IsCacheHit")
	ttft := column("TimeToFirstTokenMs", "TimeToFirstTokenInMilliseconds", "TTFTMs")
	tbt := column("TimeBetweenTokensMs", "TimeBetweenTokensInMilliseconds", "TBTMs")
	ttlt := column("TimeToLastTokenMs", "TotalLatencyMs", "DurationMs", "ResponseDurationMs", "TTLTMs")
	status := column("StatusCode", "HttpStatusCode", "ResponseCode", "ResultCode")
	deployment := column("ModelDeploymentName", "DeploymentName", "ModelDeployment")
	resource := column("_ResourceId", "ResourceId")
	requestID := column("RequestId", "CorrelationId", "OperationId")
	apiPath := column("ApiPath", "APIPath")

	summaries := []string{}
	aliases := []string{}
	add := func(alias, value string) {
		aliases = append(aliases, alias)
		summaries = append(summaries, alias+"="+value)
	}
	if table == "AzureOpenAIRequestUsage" {
		add(valueRequestCount, "count()")
		if ttft != "" {
			add(valueTTFTSampleCount, "countif(isfinite(todouble("+expression(ttft)+")))")
		}
	}
	if input != "" {
		add(valueInputAverage, "avg(todouble("+expression(input)+"))")
		add(valueInputP50, "percentile(todouble("+expression(input)+"), 50)")
		add(valueInputP95, "percentile(todouble("+expression(input)+"), 95)")
	}
	if output != "" {
		add(valueOutputAverage, "avg(todouble("+expression(output)+"))")
		add(valueOutputP50, "percentile(todouble("+expression(output)+"), 50)")
		add(valueOutputP95, "percentile(todouble("+expression(output)+"), 95)")
	}
	if streaming != "" {
		check := "tolower(tostring(" + expression(streaming) + ")) in ('true', '1', 'streaming')"
		add(valueStreamingRatio, "todouble(countif("+check+")) / count()")
	}
	if cachedTokens != "" && input != "" {
		inputTotal := "sum(todouble(" + expression(input) + "))"
		cachedTotal := "sum(todouble(" + expression(cachedTokens) + "))"
		add(valueCacheHitRatio, "iff("+inputTotal+" > 0, "+cachedTotal+" / "+inputTotal+", real(null))")
	} else if cacheHit != "" {
		check := "tolower(tostring(" + expression(cacheHit) + ")) in ('true', '1')"
		add(valueCacheHitRatio, "todouble(countif("+check+")) / count()")
	}
	for _, latency := range []struct {
		prefix string
		column string
	}{
		{"ttft", ttft},
		{"ttlt", ttlt},
	} {
		if latency.column == "" {
			continue
		}
		value := "todouble(" + expression(latency.column) + ")"
		add(latency.prefix+"_average_ms", "avg("+value+")")
		add(latency.prefix+"_p50_ms", "percentile("+value+", 50)")
		add(latency.prefix+"_p95_ms", "percentile("+value+", 95)")
	}

	tbtExpression := ""
	if tbt != "" {
		tbtExpression = "todouble(" + expression(tbt) + ")"
	} else if output != "" && ttft != "" && ttlt != "" && streaming != "" {
		outputValue := "todouble(" + expression(output) + ")"
		ttftValue := "todouble(" + expression(ttft) + ")"
		ttltValue := "todouble(" + expression(ttlt) + ")"
		streamingValue := "tolower(tostring(" + expression(streaming) + ")) in ('true', '1', 'streaming')"
		tbtExpression = "iff(" + streamingValue +
			" and isfinite(" + outputValue + ") and " + outputValue + " > 1" +
			" and isfinite(" + ttftValue + ") and isfinite(" + ttltValue + ")" +
			" and " + ttltValue + " >= " + ttftValue +
			", (" + ttltValue + " - " + ttftValue + ") / (" + outputValue + " - 1), real(null))"
	}
	if tbtExpression != "" {
		add(valueTBTSampleCount, "countif(isfinite("+tbtExpression+"))")
		add(valueTBTAverageMS, "avg("+tbtExpression+")")
		add(valueTBTP50MS, "percentile("+tbtExpression+", 50)")
		add(valueTBTP90MS, "percentile("+tbtExpression+", 90)")
		add(valueTBTP95MS, "percentile("+tbtExpression+", 95)")
	}
	if status != "" {
		add(valueErrorRate, "todouble(countif(toint("+expression(status)+") >= 400)) / count()")
		add(valueThrottlingRate, "todouble(countif(toint("+expression(status)+") == 429)) / count()")
	}
	if requestID != "" {
		add(valueRepresentativeRequestIDs, "make_set(tostring("+expression(requestID)+"), 3)")
	}
	if apiPath != "" {
		add(valueAPIPath, "take_any(tostring("+expression(apiPath)+"))")
	}
	if len(summaries) == 0 {
		return "", nil
	}

	filters := []string{
		fmt.Sprintf(
			"TimeGenerated between(datetime(%s) .. datetime(%s))",
			start.UTC().Format(time.RFC3339),
			end.UTC().Format(time.RFC3339),
		),
	}
	if resource != "" {
		filters = append(
			filters,
			"tolower(tostring("+expression(resource)+")) == tolower('"+escapeKQLString(resourceID)+"')",
		)
	}
	if deployment != "" {
		filters = append(
			filters,
			"tostring("+expression(deployment)+") == '"+escapeKQLString(deploymentName)+"'",
		)
	}
	query := table + "\n| where " + strings.Join(filters, "\n| where ") +
		"\n| summarize " + strings.Join(summaries, ", ")
	return query, aliases
}

func escapeKQLString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
