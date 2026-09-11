// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"azure.ai.latency/internal/model"
)

var htmlEvidenceTrafficOrder = []string{
	"input_tokens_p50",
	"input_tokens_p95",
	"output_tokens_p50",
	"output_tokens_p95",
	"streaming_ratio",
	"cache_hit_ratio",
	"average_requests_per_minute",
	"peak_requests_per_minute",
	"average_tokens_per_minute",
	"peak_tokens_per_minute",
	"average_input_tokens_per_minute",
	"peak_input_tokens_per_minute",
	"average_output_tokens_per_minute",
	"peak_output_tokens_per_minute",
	"deployment_tpm_limit",
	"ttft_p50_ms",
	"ttft_p95_ms",
	"tbt_p50_ms",
	"tbt_p95_ms",
	"ttlt_p50_ms",
	"ttlt_p95_ms",
	"error_rate",
	"throttling_rate",
}

var htmlProhibitedEvidenceKeys = map[string]bool{
	"cold_pool":         true,
	"hot_pool":          true,
	"pool_status":       true,
	"pool_type":         true,
	"raw_prompt":        true,
	"internal_endpoint": true,
	"kusto_query":       true,
	"credential":        true,
	"credentials":       true,
	"secret":            true,
	"secrets":           true,
}

var htmlEvidenceFilenamePattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

type htmlOrderedEvidenceTraffic map[string]any

type htmlDecimalFloat float64

type htmlEvidenceExportPayload struct {
	SchemaVersion string             `json:"schema_version"`
	PackageType   string             `json:"package_type"`
	GeneratedAt   string             `json:"generated_at"`
	Evidence      htmlEvidenceExport `json:"evidence"`
}

type htmlEvidenceExport struct {
	Summary                  string                     `json:"summary"`
	DeploymentID             string                     `json:"deployment_id"`
	TimeRange                model.TimeWindow           `json:"time_range"`
	Model                    *string                    `json:"model"`
	ModelVersion             *string                    `json:"model_version"`
	Offer                    *string                    `json:"offer"`
	BillingModel             *string                    `json:"billing_model"`
	ServiceTier              *string                    `json:"service_tier"`
	DeploymentType           *string                    `json:"deployment_type"`
	SKUName                  *string                    `json:"sku_name"`
	Region                   *string                    `json:"region"`
	SloStatus                string                     `json:"slo_status"`
	ActualMS                 *float64                   `json:"actual_ms"`
	TargetMS                 *htmlDecimalFloat          `json:"target_ms"`
	BenchmarkStatus          string                     `json:"benchmark_status"`
	BenchmarkMetric          string                     `json:"benchmark_metric"`
	BenchmarkValueMS         *htmlDecimalFloat          `json:"benchmark_value_ms"`
	TrafficProfile           htmlOrderedEvidenceTraffic `json:"traffic_profile"`
	TestedActions            []string                   `json:"tested_actions"`
	RecommendedTests         []string                   `json:"recommended_tests"`
	RepresentativeRequestIDs []string                   `json:"representative_request_ids"`
	Notice                   string                     `json:"notice"`
}

func htmlEvidencePackage(result *model.AssessmentResult) (string, error) {
	evidence := result.EvidencePackage
	if evidence == nil {
		return "", nil
	}
	reference := "Benchmark coverage unavailable"
	if evidence.BenchmarkValueMS != nil {
		reference = htmlDuration(evidence.BenchmarkValueMS)
	}

	traffic := htmlOrderedTraffic(evidence.TrafficProfile)
	var metrics strings.Builder
	for _, key := range traffic.keys() {
		metrics.WriteString("<tr><th>")
		metrics.WriteString(htmlEscape(strings.ReplaceAll(key, "_", " ")))
		metrics.WriteString("</th><td>")
		metrics.WriteString(htmlEvidenceValue(key, traffic[key]))
		metrics.WriteString("</td></tr>")
	}

	exportJSON, err := htmlEvidenceExportJSON(result, traffic)
	if err != nil {
		return "", fmt.Errorf("encode HTML evidence export: %w", err)
	}
	exportFilename := htmlEvidenceExportFilename(result)

	var output strings.Builder
	output.WriteString(`
<section class="card section-card escalation">
  <div class="section-heading"><div><h3>Support evidence</h3>
    <p>The latency gap remains unexplained. Export this JSON and attach it to a support ticket.</p></div>
    <div class="section-actions"><span class="pill danger">Support needed</span>
      <button type="button" class="export-button" data-export-evidence
        data-export-filename="`)
	output.WriteString(htmlEscape(exportFilename))
	output.WriteString(`">Export JSON</button></div></div>
  <details class="evidence-preview"><summary>Preview exported evidence</summary>
    <div class="escalation-grid">
      <span><small>Observed / target</small><strong>`)
	output.WriteString(htmlDuration(evidence.ActualMS))
	output.WriteString(` / `)
	output.WriteString(htmlDuration(evidence.TargetMS))
	output.WriteString(`</strong></span>
      <span><small>Benchmark reference (`)
	output.WriteString(htmlEscape(evidence.BenchmarkMetric))
	output.WriteString(`)</small><strong>`)
	output.WriteString(htmlEscape(reference))
	output.WriteString(`</strong></span>
      <span><small>Time range</small><strong>`)
	output.WriteString(htmlEscape(htmlDisplayTimeRange(result)))
	output.WriteString(`</strong></span>
    </div>
    <div class="table-wrap"><table>`)
	output.WriteString(metrics.String())
	output.WriteString(`</table></div>
    `)
	output.WriteString(htmlEvidenceContext(evidence))
	output.WriteString(`
  </details>
  <p class="export-note">The export excludes prompts, secrets, and internal platform data.</p>
  <script type="application/json" class="evidence-package-data">`)
	output.WriteString(exportJSON)
	output.WriteString(`</script>
</section>`)
	return output.String(), nil
}

func htmlEvidenceContext(evidence *model.EvidencePackage) string {
	var sections strings.Builder
	if len(evidence.RepresentativeRequestIDs) > 0 {
		var items strings.Builder
		for _, item := range evidence.RepresentativeRequestIDs {
			items.WriteString("<li><code>")
			items.WriteString(htmlEscape(item))
			items.WriteString("</code></li>")
		}
		sections.WriteString("<div><h4>Representative request IDs</h4><ul>")
		sections.WriteString(items.String())
		sections.WriteString("</ul></div>")
	}
	if len(evidence.TestedActions) > 0 {
		var items strings.Builder
		for _, item := range evidence.TestedActions {
			items.WriteString("<li>")
			items.WriteString(htmlEscape(item))
			items.WriteString("</li>")
		}
		sections.WriteString("<div><h4>Tests already tried</h4><ul>")
		sections.WriteString(items.String())
		sections.WriteString("</ul></div>")
	}
	if sections.Len() == 0 {
		return ""
	}
	return `<div class="evidence-context">` + sections.String() + `</div>`
}

func htmlEvidenceExportJSON(
	result *model.AssessmentResult,
	traffic htmlOrderedEvidenceTraffic,
) (string, error) {
	evidence := result.EvidencePackage
	payload := htmlEvidenceExportPayload{
		SchemaVersion: "1.0",
		PackageType:   "model_latency_support_evidence",
		GeneratedAt:   result.GeneratedAt,
		Evidence: htmlEvidenceExport{
			Summary:                  evidence.Summary,
			DeploymentID:             evidence.DeploymentID,
			TimeRange:                evidence.TimeRange,
			Model:                    htmlOptionalJSONText(evidence.Model),
			ModelVersion:             htmlOptionalJSONText(evidence.ModelVersion),
			Offer:                    htmlOptionalJSONText(evidence.Offer),
			BillingModel:             htmlOptionalJSONText(evidence.BillingModel),
			ServiceTier:              htmlOptionalJSONText(evidence.ServiceTier),
			DeploymentType:           htmlOptionalJSONText(evidence.DeploymentType),
			SKUName:                  htmlOptionalJSONText(evidence.SKUName),
			Region:                   htmlOptionalJSONText(evidence.Region),
			SloStatus:                evidence.SloStatus,
			ActualMS:                 evidence.ActualMS,
			TargetMS:                 htmlDecimalFloatPointer(evidence.TargetMS),
			BenchmarkStatus:          evidence.BenchmarkStatus,
			BenchmarkMetric:          evidence.BenchmarkMetric,
			BenchmarkValueMS:         htmlDecimalFloatPointer(evidence.BenchmarkValueMS),
			TrafficProfile:           traffic,
			TestedActions:            htmlJSONStrings(evidence.TestedActions),
			RecommendedTests:         htmlJSONStrings(evidence.RecommendedTests),
			RepresentativeRequestIDs: htmlJSONStrings(evidence.RepresentativeRequestIDs),
			Notice:                   evidence.Notice,
		},
	}
	content, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return htmlASCIIJSON(content), nil
}

func htmlEvidenceExportFilename(result *model.AssessmentResult) string {
	deployment := htmlEvidenceFilenamePattern.ReplaceAllString(
		result.TrafficProfile.Deployment.DeploymentName,
		"-",
	)
	deployment = strings.Trim(deployment, "-")
	if deployment == "" {
		deployment = "deployment"
	}
	timestamp := "report"
	if generated, err := htmlParseTimestamp(result.GeneratedAt); err == nil {
		timestamp = generated.Format("20060102T150405Z")
	}
	return "model-latency-evidence-" + deployment + "-" + timestamp + ".json"
}

func htmlEvidenceValue(key string, value any) string {
	if htmlNilValue(value) {
		return unavailable
	}
	number, numeric := htmlNumericValue(value)
	if strings.HasSuffix(key, "_ratio") || strings.HasSuffix(key, "_rate") {
		if numeric {
			return htmlPercent(new(number))
		}
	}
	if strings.HasSuffix(key, "_ms") {
		if numeric {
			return htmlDuration(new(number))
		}
	}
	if numeric {
		return htmlEscape(htmlNumberValue(number))
	}
	return htmlEscape(htmlPythonValue(value))
}

func htmlNumericValue(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int8:
		return float64(value), true
	case int16:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint8:
		return float64(value), true
	case uint16:
		return float64(value), true
	case uint32:
		return float64(value), true
	case uint64:
		return float64(value), true
	case json.Number:
		number, err := value.Float64()
		return number, err == nil
	case bool:
		if value {
			return 1, true
		}
		return 0, true
	case *float64:
		if value != nil {
			return *value, true
		}
	case *float32:
		if value != nil {
			return float64(*value), true
		}
	case *int:
		if value != nil {
			return float64(*value), true
		}
	case *int32:
		if value != nil {
			return float64(*value), true
		}
	case *int64:
		if value != nil {
			return float64(*value), true
		}
	}
	return 0, false
}

func htmlPythonValue(value any) string {
	switch value := value.(type) {
	case bool:
		if value {
			return "True"
		}
		return "False"
	case string:
		return value
	default:
		return fmt.Sprint(value)
	}
}

func htmlNilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func htmlOrderedTraffic(profile map[string]any) htmlOrderedEvidenceTraffic {
	traffic := make(htmlOrderedEvidenceTraffic)
	for key, value := range profile {
		if htmlProhibitedEvidenceKeys[strings.ToLower(key)] {
			continue
		}
		traffic[key] = htmlSanitizeJSONValue(value)
	}
	return traffic
}

func (traffic htmlOrderedEvidenceTraffic) keys() []string {
	keys := make([]string, 0, len(traffic))
	seen := make(map[string]bool, len(traffic))
	for _, key := range htmlEvidenceTrafficOrder {
		if _, ok := traffic[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	extras := make([]string, 0, len(traffic)-len(keys))
	for key := range traffic {
		if !seen[key] {
			extras = append(extras, key)
		}
	}
	slices.Sort(extras)
	return append(keys, extras...)
}

func (traffic htmlOrderedEvidenceTraffic) MarshalJSON() ([]byte, error) {
	var output bytes.Buffer
	output.WriteByte('{')
	for index, key := range traffic.keys() {
		if index > 0 {
			output.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		encodedValue, err := htmlMarshalEvidenceTrafficValue(key, traffic[key])
		if err != nil {
			return nil, err
		}
		output.Write(encodedKey)
		output.WriteByte(':')
		output.Write(encodedValue)
	}
	output.WriteByte('}')
	return output.Bytes(), nil
}

func htmlMarshalEvidenceTrafficValue(key string, value any) ([]byte, error) {
	if key == "deployment_tpm_limit" {
		if number, ok := htmlNumericValue(value); ok {
			return htmlDecimalFloat(number).MarshalJSON()
		}
	}
	return json.Marshal(value)
}

func htmlSanitizeJSONValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			if !htmlProhibitedEvidenceKeys[strings.ToLower(key)] {
				result[key] = htmlSanitizeJSONValue(item)
			}
		}
		return result
	case []any:
		result := make([]any, len(value))
		for index, item := range value {
			result[index] = htmlSanitizeJSONValue(item)
		}
		return result
	default:
		return value
	}
}

func htmlOptionalJSONText(value string) *string {
	if value == "" {
		return nil
	}
	return new(value)
}

func htmlJSONStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return slices.Clone(values)
}

func htmlDecimalFloatPointer(value *float64) *htmlDecimalFloat {
	if value == nil {
		return nil
	}
	return new(htmlDecimalFloat(*value))
}

func (value htmlDecimalFloat) MarshalJSON() ([]byte, error) {
	number := float64(value)
	if number == math.Trunc(number) {
		return []byte(strconv.FormatFloat(number, 'f', 0, 64) + ".0"), nil
	}
	return []byte(strconv.FormatFloat(number, 'g', -1, 64)), nil
}

func htmlASCIIJSON(content []byte) string {
	var output strings.Builder
	for len(content) > 0 {
		character, size := utf8.DecodeRune(content)
		content = content[size:]
		if character <= unicodeMaxASCII {
			output.WriteRune(character)
			continue
		}
		if character <= 0xffff {
			fmt.Fprintf(&output, `\u%04x`, character)
			continue
		}
		value := character - 0x10000
		high := 0xD800 + (value >> 10)
		low := 0xDC00 + (value & 0x3FF)
		fmt.Fprintf(&output, `\u%04x\u%04x`, high, low)
	}
	return output.String()
}

const unicodeMaxASCII = '\x7f'

var _ json.Marshaler = htmlOrderedEvidenceTraffic{}
