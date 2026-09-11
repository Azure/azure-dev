// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"azure.ai.latency/internal/model"
)

var prohibitedOutputKeys = map[string]bool{
	"cold_pool":         true,
	"hot_pool":          true,
	"pool_status":       true,
	"pool_type":         true,
	"raw_prompt":        true,
	"internal_endpoint": true,
	"kusto_query":       true,
	"credentials":       true,
	"secrets":           true,
}

func TestCustomerResultSchemaExcludesInternalFields(t *testing.T) {
	for _, name := range []string{
		"within-target",
		"workload-explained",
		"unexplained-gap",
		"no-benchmark-coverage",
		"logs-unavailable",
	} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			if err := RenderJSON(&output, fixtureAssessment(t, name)); err != nil {
				t.Fatal(err)
			}
			var payload any
			if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			for key := range allJSONKeys(payload) {
				if prohibitedOutputKeys[key] {
					t.Errorf("customer result contains prohibited key %q", key)
				}
			}
		})
	}
}

func TestIllustrativeDataIsDisclosedInTerminalAndJSON(t *testing.T) {
	result := fixtureAssessment(t, "within-target")
	var terminal bytes.Buffer
	if err := RenderTerminal(&terminal, result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(terminal.String(), "MODEL LATENCY SELF-SERVICE TOOL") {
		t.Fatal("terminal report does not contain the product name")
	}
	if !strings.Contains(terminal.String(), "Note: illustrative sample data.") {
		t.Fatal("terminal report does not disclose illustrative data")
	}

	var renderedJSON bytes.Buffer
	if err := RenderJSON(&renderedJSON, result); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(renderedJSON.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["data_notice"] != "Illustrative sample data" {
		t.Fatalf("data notice = %v, want Illustrative sample data", payload["data_notice"])
	}
	components := payload["illustrative_components"].([]any)
	if !containsJSONText(components, "Deployment and telemetry") {
		t.Fatal("JSON does not identify the illustrative deployment and telemetry")
	}
	profile := payload["traffic_profile"].(map[string]any)
	tokenRate := profile["token_rate"].(map[string]any)
	if tokenRate["peak_limit_ratio"] != 0.8 {
		t.Fatalf("peak limit ratio = %v, want 0.8", tokenRate["peak_limit_ratio"])
	}
	for _, key := range []string{"input_average_tpm", "output_peak_tpm"} {
		if _, ok := tokenRate[key]; !ok {
			t.Errorf("token rate does not contain %q", key)
		}
	}
	deployment := profile["deployment"].(map[string]any)
	for _, key := range []string{"offer", "billing_model", "service_tier", "deployment_type", "sku_name"} {
		if _, ok := deployment[key]; !ok {
			t.Errorf("deployment does not contain %q", key)
		}
	}
	for _, key := range []string{"billing_mode", "deployment_sku"} {
		if _, ok := deployment[key]; ok {
			t.Errorf("deployment contains legacy key %q", key)
		}
	}
}

func TestTBTP90IsRenderedWhenRequestLogsProvideIt(t *testing.T) {
	result := fixtureAssessment(t, "within-target")
	latency := result.TrafficProfile.Latency["tbt"]
	latency.AverageMS = model.Float64(29.2)
	latency.P50MS = model.Float64(28.5)
	latency.P90MS = model.Float64(35.4)
	latency.P95MS = model.Float64(39.1)
	result.TrafficProfile.Latency["tbt"] = latency

	var terminal bytes.Buffer
	if err := RenderTerminal(&terminal, result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(terminal.String(), "P90 35ms") {
		t.Fatalf("terminal TBT P90 was not rounded like the reference:\n%s", terminal.String())
	}

	report := renderHTMLText(t, result)
	for _, phrase := range []string{
		"Time between tokens (TBT)",
		`class="metric-hero">39ms <small>P95</small>`,
		"Token rate",
		"peak total TPM",
	} {
		if !strings.Contains(report, phrase) {
			t.Errorf("HTML report does not contain %q", phrase)
		}
	}

	var renderedJSON bytes.Buffer
	if err := RenderJSON(&renderedJSON, result); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(renderedJSON.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	profile := payload["traffic_profile"].(map[string]any)
	latencyJSON := profile["latency"].(map[string]any)
	tbt := latencyJSON["tbt"].(map[string]any)
	if tbt["p90_ms"] != 35.4 {
		t.Fatalf("TBT P90 = %v, want 35.4", tbt["p90_ms"])
	}
}

func TestWithinTargetReportSuppressesBenchmarkAndSwitcher(t *testing.T) {
	report := renderHTMLText(t, fixtureAssessment(t, "within-target"))
	for _, phrase := range []string{
		"Within current offer target",
		"<title>Model Latency Self-Service Tool</title>",
		"<strong>Model Latency Self-Service Tool</strong>",
		"Observed TBT P95 versus current offer target",
		"This is an operating target, not a contractual SLA.",
		"P50 28ms",
		"Sep 1, 2026, 08:00 – Sep 2, 2026, 08:00 UTC · 1 day",
		".marker.p50 span{bottom:36px}",
		".marker.p95 span{top:38px}",
		".marker.target span{top:58px}",
	} {
		if !strings.Contains(report, phrase) {
			t.Errorf("HTML report does not contain %q", phrase)
		}
	}
	for _, phrase := range []string{
		"Model Latency Insights",
		"Comparison with a similar workload",
		`<section class="card scenario-picker">`,
		"Data-source details",
		"Metric definitions",
		"Methodology and limitations",
	} {
		if strings.Contains(report, phrase) {
			t.Errorf("HTML report unexpectedly contains %q", phrase)
		}
	}
	assertBefore(t, report, "<small>Assessment scope</small>", "<h3>Traffic profile</h3>")
	assertBefore(
		t,
		report,
		`<div class="profile-label">Input tokens</div>`,
		`<div class="profile-label">Output tokens</div>`,
	)
	assertBefore(
		t,
		report,
		`<div class="profile-label">Output tokens</div>`,
		`<span class="profile-label">Token rate</span>`,
	)
	assertBefore(
		t,
		report,
		`<span class="profile-label">Token rate</span>`,
		`<div class="profile-label">Request rate</div>`,
	)
	assertBefore(
		t,
		report,
		`<div class="profile-label">Request rate</div>`,
		`<span class="profile-label">Behavior and reliability</span>`,
	)
}

func TestSameDayTimeRangeIsCompact(t *testing.T) {
	result := fixtureAssessment(t, "within-target")
	result.TimeRange = model.TimeWindow{
		StartTime: "2026-09-09T07:58:59Z",
		EndTime:   "2026-09-09T08:11:48Z",
		Label:     "Custom time range",
	}
	report := renderHTMLText(t, result)
	if !strings.Contains(report, "Sep 9, 2026 · 07:58–08:11 UTC · 13 min") {
		t.Fatal("HTML report does not contain the compact same-day time range")
	}
	if strings.Contains(report, "Custom time range ·") {
		t.Fatal("HTML report includes the source time-range label")
	}
}

func TestFormattingMatchesConcreteReferenceReport(t *testing.T) {
	for name, test := range map[string]struct {
		actual string
		want   string
	}{
		"number with grouping": {
			actual: htmlNumberValue(3909.1),
			want:   "3,909.1",
		},
		"integer with grouping": {
			actual: htmlNumberValue(32104),
			want:   "32,104",
		},
		"token abbreviation": {
			actual: htmlToken(model.Float64(1000)),
			want:   "1.0K",
		},
		"percentage": {
			actual: htmlPercent(model.Float64(0.755)),
			want:   "75.5%",
		},
		"duration in milliseconds": {
			actual: htmlDuration(model.Float64(14.3)),
			want:   "14ms",
		},
		"duration in seconds": {
			actual: htmlDuration(model.Float64(16550)),
			want:   "16.55s",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if test.actual != test.want {
				t.Fatalf("formatted value = %q, want %q", test.actual, test.want)
			}
		})
	}

	result := fixtureAssessment(t, "within-target")
	result.GeneratedAt = "2026-09-11T16:35:36+08:00"
	result.TimeRange = model.TimeWindow{
		StartTime: "2026-09-09T07:58:59Z",
		EndTime:   "2026-09-09T08:11:48Z",
		Label:     "Custom time range",
	}
	if freshness := htmlDataFreshness(result); freshness != "Data through Sep 9, 2026 08:11 UTC" {
		t.Fatalf("freshness = %q, want concrete reference formatting", freshness)
	}
}

func TestWorkloadExplainedReportShowsComparison(t *testing.T) {
	result := fixtureAssessment(t, "workload-explained")
	report := renderHTMLText(t, result)
	for _, phrase := range []string{
		"Above target — workload explained",
		"Comparison with a similar workload",
		"Similar workload benchmark",
		"90ms",
		"High match confidence",
		"Compared with this workload",
		"Input 8–16K",
		"Output ≥500",
	} {
		if !strings.Contains(report, phrase) {
			t.Errorf("HTML report does not contain %q", phrase)
		}
	}
	for _, phrase := range []string{
		"DEMO / MOCK DATA",
		"Benchmark matching dimensions",
		"Evidence package for support",
	} {
		if strings.Contains(report, phrase) {
			t.Errorf("HTML report unexpectedly contains %q", phrase)
		}
	}
	if result.BenchmarkComparison.BenchmarkValueMS == nil ||
		*result.BenchmarkComparison.BenchmarkValueMS != 90 {
		t.Fatalf("benchmark value = %v, want 90", result.BenchmarkComparison.BenchmarkValueMS)
	}
	if result.BenchmarkComparison.Comparison == nil ||
		*result.BenchmarkComparison.Comparison != model.ComparisonAtOrBelow {
		t.Fatalf("benchmark comparison = %v, want %s", result.BenchmarkComparison.Comparison, model.ComparisonAtOrBelow)
	}
	if result.BenchmarkComparison.SelectedCohort.InputTokenBucket != "8-16K" {
		t.Fatalf(
			"input token bucket = %q, want 8-16K",
			result.BenchmarkComparison.SelectedCohort.InputTokenBucket,
		)
	}
	assertBefore(t, report, "Comparison with a similar workload", "Recommended next steps")
}

func TestRecommendationsAreSeparateAndActionable(t *testing.T) {
	report := renderHTMLText(t, fixtureAssessment(t, "workload-explained"))
	if count := strings.Count(report, `<article class="recommendation-item">`); count != 3 {
		t.Fatalf("recommendation card count = %d, want 3", count)
	}
	for _, phrase := range []string{
		`class="evidence-chain"`,
		"Assessment outcome",
		"recommendation-number",
	} {
		if strings.Contains(report, phrase) {
			t.Errorf("HTML report unexpectedly contains %q", phrase)
		}
	}
	for _, phrase := range []string{
		"Recommended next steps",
		"Use a stable prompt prefix, then rerun the same test.",
		"Ask for a shorter response, then compare total response time.",
		"Send requests at a steadier rate, then compare latency and throttling.",
	} {
		if !strings.Contains(report, phrase) {
			t.Errorf("HTML report does not contain %q", phrase)
		}
	}
}

func TestUnexplainedReportContainsCustomerSafeEvidence(t *testing.T) {
	report := renderHTMLText(t, fixtureAssessment(t, "unexplained-gap"))
	for _, phrase := range []string{
		"Unexplained target violation",
		"Support evidence",
		"Export JSON",
		"Preview exported evidence",
		"Representative request IDs",
		"Tests already tried",
		"The export excludes prompts, secrets, and internal platform data.",
	} {
		if !strings.Contains(report, phrase) {
			t.Errorf("HTML report does not contain %q", phrase)
		}
	}
	for _, phrase := range []string{"Assessment outcome", "Recommended next steps"} {
		if strings.Contains(report, phrase) {
			t.Errorf("HTML report unexpectedly contains %q", phrase)
		}
	}
}

func TestEmptySupportContextIsOmitted(t *testing.T) {
	result := fixtureAssessment(t, "unexplained-gap")
	result.EvidencePackage.RepresentativeRequestIDs = []string{}
	result.EvidencePackage.TestedActions = []string{}
	report := renderHTMLText(t, result)
	for _, phrase := range []string{
		"Representative request IDs",
		"Tests already tried",
		"No completed tests were recorded.",
		"<li>Unavailable</li>",
	} {
		if strings.Contains(report, phrase) {
			t.Errorf("HTML report unexpectedly contains %q", phrase)
		}
	}
}

func TestSupportExportContainsCustomerSafeEvidenceOnly(t *testing.T) {
	result := fixtureAssessment(t, "unexplained-gap")
	report := renderHTMLText(t, result)
	payload := parseEvidenceExport(t, report)

	if payload["package_type"] != "model_latency_support_evidence" {
		t.Fatalf("package type = %v, want model_latency_support_evidence", payload["package_type"])
	}
	if payload["schema_version"] != "1.0" {
		t.Fatalf("schema version = %v, want 1.0", payload["schema_version"])
	}
	evidence := payload["evidence"].(map[string]any)
	if evidence["deployment_id"] != result.TrafficProfile.Deployment.DeploymentID {
		t.Fatalf(
			"deployment ID = %v, want %s",
			evidence["deployment_id"],
			result.TrafficProfile.Deployment.DeploymentID,
		)
	}
	expectedTraffic := normalizeJSONValue(t, result.EvidencePackage.TrafficProfile)
	if !reflect.DeepEqual(evidence["traffic_profile"], expectedTraffic) {
		t.Fatalf(
			"traffic profile = %#v, want %#v",
			evidence["traffic_profile"],
			expectedTraffic,
		)
	}
	for key := range allJSONKeys(payload) {
		if prohibitedOutputKeys[key] {
			t.Errorf("evidence export contains prohibited key %q", key)
		}
	}
	for _, phrase := range []string{
		"data-export-evidence",
		"URL.createObjectURL",
		"model-latency-evidence-gpt-5.6-luna-20260902T080000Z.json",
	} {
		if !strings.Contains(report, phrase) {
			t.Errorf("HTML report does not contain %q", phrase)
		}
	}
}

func TestEvidenceExportEscapesScriptContentAndFiltersSensitiveKeys(t *testing.T) {
	result := fixtureAssessment(t, "unexplained-gap")
	result.EvidencePackage.RepresentativeRequestIDs = []string{`</script><script>alert("request")</script>`}
	result.EvidencePackage.TrafficProfile["safe_marker"] = `<script>alert("traffic")</script>`
	result.EvidencePackage.TrafficProfile["raw_prompt"] = "do-not-disclose"

	report := renderHTMLText(t, result)
	for _, prohibited := range []string{
		`</script><script>alert("request")</script>`,
		`<script>alert("traffic")</script>`,
		"do-not-disclose",
		"raw_prompt",
	} {
		if strings.Contains(report, prohibited) {
			t.Errorf("HTML report contains unsafe content %q", prohibited)
		}
	}
	for _, escaped := range []string{
		`&lt;/script&gt;&lt;script&gt;alert(&quot;request&quot;)&lt;/script&gt;`,
		`\u003cscript\u003ealert(\"traffic\")\u003c/script\u003e`,
	} {
		if !strings.Contains(report, escaped) {
			t.Errorf("HTML report does not contain safe escaped content %q", escaped)
		}
	}
}

func TestEvidenceExportUsesASCIISafeJSON(t *testing.T) {
	result := fixtureAssessment(t, "unexplained-gap")
	result.EvidencePackage.TrafficProfile["unicode_marker"] = "≥😀"
	report := renderHTMLText(t, result)
	export := evidenceExportText(t, report)
	if !strings.Contains(export, `\u2265\ud83d\ude00`) {
		t.Fatalf("evidence export does not ASCII-escape Unicode: %s", export)
	}
}

func TestCoverageGapDoesNotClaimBenchmarkRange(t *testing.T) {
	report := renderHTMLText(t, fixtureAssessment(t, "no-benchmark-coverage"))
	for _, phrase := range []string{
		"Latency exceeds the target; benchmark coverage is unavailable",
		"Target violation · coverage gap",
	} {
		if !strings.Contains(report, phrase) {
			t.Errorf("HTML report does not contain %q", phrase)
		}
	}
	if strings.Contains(report, "Latency exceeds the target and similar-workload range") {
		t.Fatal("coverage-gap report claims that a benchmark range was exceeded")
	}
}

func TestBasicProfileMarksRequestDistributionsUnavailable(t *testing.T) {
	report := renderHTMLText(t, fixtureAssessment(t, "logs-unavailable"))
	for _, phrase := range []string{
		"Basic profile",
		"Unavailable from aggregate-only telemetry",
		"Enable resource diagnostic logs.",
		"AzureOpenAIRequestUsage",
		"Explore offer options",
	} {
		if !strings.Contains(report, phrase) {
			t.Errorf("HTML report does not contain %q", phrase)
		}
	}
}

func TestOfferExplorerIsSimpleAndLatencyFocused(t *testing.T) {
	report := renderHTMLText(t, fixtureAssessment(t, "within-target"))
	for _, phrase := range []string{
		"Explore offer options",
		"PTU-M",
		"Best for",
		"Trade-off",
		`type="radio"`,
		"Lower latency for traffic spikes",
		"More predictable latency",
		`data-offer-result="traffic-spikes"`,
		`data-offer-result="predictable-latency"`,
		".offer-goal{display:grid;grid-template-columns:20px minmax(0,1fr);align-items:start",
	} {
		if !strings.Contains(report, phrase) {
			t.Errorf("HTML report does not contain %q", phrase)
		}
	}
	for _, phrase := range []string{"Why this option is available", "Lower cost", "<strong>Skip</strong>"} {
		if strings.Contains(report, phrase) {
			t.Errorf("HTML report unexpectedly contains %q", phrase)
		}
	}
}

func TestWorkloadActionAndOfferExplorerCanCoexist(t *testing.T) {
	result := fixtureAssessment(t, "within-target")
	result.Recommendations = []model.Recommendation{{
		ObservedPattern:    "Low prompt-cache reuse",
		SupportingEvidence: "Prompt-cache usage is below the configured threshold.",
		RecommendedAction:  "Use a stable prompt prefix, then rerun the same test.",
		Confidence:         "Medium",
	}}
	report := renderHTMLText(t, result)
	for _, phrase := range []string{"Low prompt-cache reuse", "Explore offer options"} {
		if !strings.Contains(report, phrase) {
			t.Errorf("HTML report does not contain %q", phrase)
		}
	}
}

func TestExplainedAndUnexplainedOfferExplorerVisibility(t *testing.T) {
	explained := renderHTMLText(t, fixtureAssessment(t, "workload-explained"))
	if !strings.Contains(explained, "Above target — workload explained") ||
		!strings.Contains(explained, "Explore offer options") {
		t.Fatal("explained target miss does not show the assessment and offer explorer")
	}

	unexplained := renderHTMLText(t, fixtureAssessment(t, "unexplained-gap"))
	if !strings.Contains(unexplained, "Unexplained target violation") {
		t.Fatal("unexplained target miss does not show the assessment")
	}
	if strings.Contains(unexplained, "Explore offer options") {
		t.Fatal("unexplained target miss shows the offer explorer")
	}
}

func TestScenarioSelectorUsesReferenceLabelsAndBehavior(t *testing.T) {
	results := []*model.AssessmentResult{
		fixtureAssessment(t, "within-target"),
		fixtureAssessment(t, "workload-explained"),
		fixtureAssessment(t, "unexplained-gap"),
		fixtureAssessment(t, "logs-unavailable"),
	}
	first := renderHTMLText(t, results...)
	second := renderHTMLText(t, results...)
	if first != second {
		t.Fatal("same scenario inputs produced different HTML")
	}
	for _, phrase := range []string{
		`<section class="card scenario-picker">`,
		`class="scenario-button active" data-target="within-target">Within target</button>`,
		`data-target="workload-explained">Above target · workload explained</button>`,
		`data-target="unexplained-gap">Above target · unexplained</button>`,
		`data-target="logs-unavailable">Request logs unavailable</button>`,
		`panel.dataset.scenario === button.dataset.target`,
	} {
		if !strings.Contains(first, phrase) {
			t.Errorf("multi-scenario report does not contain %q", phrase)
		}
	}
	if count := strings.Count(first, `<article class="scenario-panel`); count != len(results) {
		t.Fatalf("scenario panel count = %d, want %d", count, len(results))
	}
}

func TestRenderHTMLEscapesDynamicMarkup(t *testing.T) {
	result := fixtureAssessment(t, "within-target")
	result.TrafficProfile.Deployment.DeploymentName = `<script>deployment-marker</script>`
	result.Recommendations[0].ObservedPattern = `<img src=x onerror="recommendation-marker">`
	report := renderHTMLText(t, result)
	for _, raw := range []string{
		`<script>deployment-marker</script>`,
		`<img src=x onerror="recommendation-marker">`,
	} {
		if strings.Contains(report, raw) {
			t.Errorf("HTML report contains unescaped dynamic markup %q", raw)
		}
	}
	for _, escaped := range []string{
		`&lt;script&gt;deployment-marker&lt;/script&gt;`,
		`&lt;img src=x onerror=&quot;recommendation-marker&quot;&gt;`,
	} {
		if !strings.Contains(report, escaped) {
			t.Errorf("HTML report does not contain escaped value %q", escaped)
		}
	}
}

func TestWriteHTMLCreatesParentDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "reports", "latency.html")
	if err := WriteHTML(path, []*model.AssessmentResult{fixtureAssessment(t, "within-target")}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path) //nolint:gosec // The path is created by t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(content, []byte("<!doctype html>")) {
		t.Fatal("written report is not HTML")
	}
	if runtime.GOOS == "windows" && !bytes.Contains(content, []byte("\r\n")) {
		t.Fatal("Windows report does not use the reference renderer's CRLF line endings")
	}
	if err := WriteHTML(filepath.Dir(path), []*model.AssessmentResult{
		fixtureAssessment(t, "within-target"),
	}); err == nil {
		t.Fatal("WriteHTML succeeded when the report path was a directory")
	}
}

func TestRenderersValidateInputsAndReturnWriterErrors(t *testing.T) {
	result := fixtureAssessment(t, "within-target")
	writer := errorWriter{}
	for name, render := range map[string]func() error{
		"terminal": func() error { return RenderTerminal(writer, result) },
		"JSON":     func() error { return RenderJSON(writer, result) },
		"HTML":     func() error { return RenderHTML(writer, []*model.AssessmentResult{result}) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := render(); !errors.Is(err, errInjectedWriter) {
				t.Fatalf("error = %v, want injected writer error", err)
			}
		})
	}
	if err := RenderHTML(io.Discard, nil); err == nil {
		t.Fatal("RenderHTML accepted no assessment results")
	}
	if err := RenderHTML(io.Discard, []*model.AssessmentResult{nil}); err == nil {
		t.Fatal("RenderHTML accepted a nil assessment result")
	}
	if err := WriteHTML("", []*model.AssessmentResult{result}); err == nil {
		t.Fatal("WriteHTML accepted an empty path")
	}
}

func fixtureAssessment(t *testing.T, name string) *model.AssessmentResult {
	t.Helper()
	path := filepath.Join("testdata", "python_"+strings.ReplaceAll(name, "-", "_")+".json")
	content, err := os.ReadFile(path) //nolint:gosec // Test fixtures are checked into the package.
	if err != nil {
		t.Fatal(err)
	}
	var result model.AssessmentResult
	if err := json.Unmarshal(content, &result); err != nil {
		t.Fatal(err)
	}
	return &result
}

func renderHTMLText(t *testing.T, results ...*model.AssessmentResult) string {
	t.Helper()
	var output bytes.Buffer
	if err := RenderHTML(&output, results); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func parseEvidenceExport(t *testing.T, report string) map[string]any {
	t.Helper()
	export := evidenceExportText(t, report)
	var payload map[string]any
	if err := json.Unmarshal([]byte(export), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func evidenceExportText(t *testing.T, report string) string {
	t.Helper()
	const prefix = `<script type="application/json" class="evidence-package-data">`
	start := strings.Index(report, prefix)
	if start < 0 {
		t.Fatal("HTML report does not contain evidence export data")
	}
	start += len(prefix)
	end := strings.Index(report[start:], "</script>")
	if end < 0 {
		t.Fatal("HTML report does not close the evidence export script")
	}
	return report[start : start+end]
}

func normalizeJSONValue(t *testing.T, value any) any {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var normalized any
	if err := json.Unmarshal(content, &normalized); err != nil {
		t.Fatal(err)
	}
	return normalized
}

func allJSONKeys(value any) map[string]bool {
	keys := map[string]bool{}
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			keys[strings.ToLower(key)] = true
			for nested := range allJSONKeys(item) {
				keys[nested] = true
			}
		}
	case []any:
		for _, item := range value {
			for nested := range allJSONKeys(item) {
				keys[nested] = true
			}
		}
	}
	return keys
}

func containsJSONText(values []any, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func assertBefore(t *testing.T, value, first, second string) {
	t.Helper()
	firstIndex := strings.Index(value, first)
	secondIndex := strings.Index(value, second)
	if firstIndex < 0 || secondIndex < 0 || firstIndex >= secondIndex {
		t.Fatalf("%q index = %d, %q index = %d", first, firstIndex, second, secondIndex)
	}
}

var errInjectedWriter = errors.New("injected writer failure")

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errInjectedWriter
}

var _ io.Writer = errorWriter{}
