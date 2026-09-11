// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//nolint:lll // Static markup must remain byte-identical to the authoritative renderer.
package report

import (
	"fmt"
	"strings"

	"azure.ai.latency/internal/model"
)

func htmlAssessmentCard(result *model.AssessmentResult) string {
	profile := result.TrafficProfile
	assessment := result.SloAssessment
	outcome := htmlSemanticOutcome(result)
	action := htmlNextAction(result)
	full := htmlHasFullRequestProfile(profile)
	completeness := "Basic traffic profile"
	if full {
		completeness = "Full traffic profile"
	}
	sample := "Request sample unavailable"
	if profile.RequestCount != nil {
		sample = htmlInteger(profile.RequestCount) + " requests analyzed"
	}
	freshness := htmlDataFreshness(result)
	deployment := profile.Deployment
	metricLatency := htmlLatency(profile, assessment.Metric)
	chart := htmlBulletChart(
		metricLatency.P50MS,
		assessment.ActualMS,
		assessment.TargetMS,
		assessment.Metric,
	)

	var output strings.Builder
	output.WriteString(`
<section class="card assessment-card state-`)
	output.WriteString(outcome.State)
	output.WriteString(`">
  <div class="assessment-copy">
    <div class="status-line"><span class="status-icon" aria-hidden="true">`)
	output.WriteString(outcome.Icon)
	output.WriteString(`</span>
      <span class="status-badge">`)
	output.WriteString(htmlEscape(outcome.Label))
	output.WriteString(`</span></div>
    <h2>`)
	output.WriteString(htmlEscape(outcome.Title))
	output.WriteString(`</h2>
    <p class="assessment-lead">`)
	output.WriteString(outcome.Meaning)
	output.WriteString(`</p>
    <div class="assessment-scope" aria-label="Assessment scope">
      <small>Assessment scope</small>
      <dl class="scope-grid">
        <div><dt>Deployment</dt><dd>`)
	output.WriteString(htmlEscape(deployment.DeploymentName))
	output.WriteString(`</dd></div>
        <div><dt>Model / version</dt><dd>`)
	output.WriteString(htmlEscape(htmlText(deployment.Model)))
	output.WriteString(` / `)
	output.WriteString(htmlEscape(htmlText(deployment.ModelVersion)))
	output.WriteString(`</dd></div>
        <div><dt>Current offer</dt><dd>`)
	output.WriteString(htmlEscape(htmlOfferLabel(deployment.Offer)))
	output.WriteString(` · `)
	output.WriteString(htmlEscape(htmlDeploymentTypeLabel(deployment.DeploymentType)))
	output.WriteString(`</dd></div>
        <div><dt>Region</dt><dd>`)
	output.WriteString(htmlEscape(htmlRegionLabel(deployment.Region)))
	output.WriteString(`</dd></div>
        <div class="wide"><dt>Time range</dt><dd>`)
	output.WriteString(htmlEscape(htmlDisplayTimeRange(result)))
	output.WriteString(`</dd></div>
      </dl>
      <details class="resource-id"><summary>Resource ID</summary><code>`)
	output.WriteString(htmlEscape(deployment.DeploymentID))
	output.WriteString(`</code></details>
    </div>
    <div class="next-action"><small>What to do now</small><strong>`)
	output.WriteString(htmlEscape(action))
	output.WriteString(`</strong></div>
    <div class="trust-row" aria-label="Assessment quality">
      <span>`)
	output.WriteString(htmlEscape(sample))
	output.WriteString(`</span><span>`)
	output.WriteString(htmlEscape(completeness))
	output.WriteString(`</span>
      <span>`)
	output.WriteString(htmlEscape(freshness))
	output.WriteString(`</span>
    </div>
  </div>
  <div class="assessment-chart">
    <small>Observed `)
	output.WriteString(htmlEscape(assessment.Metric))
	output.WriteString(` `)
	output.WriteString(htmlEscape(assessment.Percentile))
	output.WriteString(` versus current offer target</small>
    <div class="chart-value">`)
	output.WriteString(htmlDuration(assessment.ActualMS))
	output.WriteString(` <span>target `)
	output.WriteString(htmlDuration(assessment.TargetMS))
	output.WriteString(`</span></div>
    `)
	output.WriteString(chart)
	output.WriteString(`
    <p>This is an operating target, not a contractual SLA.</p>
  </div>
</section>`)
	return output.String()
}

func htmlTrafficProfile(result *model.AssessmentResult) string {
	profile := result.TrafficProfile
	full := htmlHasFullRequestProfile(profile)
	profileNote := "Platform metrics are available; request-level distributions are marked unavailable."
	pillClass := "warning"
	pillLabel := "Basic"
	if full {
		profileNote = "Request-level logs provide token distributions and latency percentiles."
		pillClass = "complete"
		pillLabel = "Full"
	}

	var output strings.Builder
	output.WriteString(`
<section class="card section-card" id="traffic-profile">
  <div class="section-heading"><div><h3>Traffic profile</h3><p>`)
	output.WriteString(htmlEscape(profileNote))
	output.WriteString(`</p></div>
    <span class="pill `)
	output.WriteString(pillClass)
	output.WriteString(`">`)
	output.WriteString(pillLabel)
	output.WriteString(` profile</span></div>
  <div class="group-heading"><h4>Latency experience</h4><span>Customer-visible model latency</span></div>
  <div class="latency-grid">
    `)
	output.WriteString(htmlLatencyCard(
		"TTFT",
		"Time to first token",
		htmlLatency(profile, "ttft"),
		"Time from request submission until the first response token is available.",
	))
	output.WriteString(`
    `)
	output.WriteString(htmlLatencyCard(
		"TBT",
		"Time between tokens",
		htmlLatency(profile, "tbt"),
		"Per-request mean generation interval between tokens. "+
			"Percentiles are calculated across eligible streaming requests.",
	))
	output.WriteString(`
    `)
	output.WriteString(htmlLatencyCard(
		"TTLT",
		"Time to last token",
		htmlLatency(profile, "ttlt"),
		"Total response time from request submission through the last generated token.",
	))
	output.WriteString(`
  </div>
  <div class="group-heading"><h4>Workload shape</h4><span>Signals that may contribute to latency</span></div>
  <div class="workload-grid">
    `)
	output.WriteString(htmlTokenCard(
		"Input tokens",
		profile.InputTokens,
		[]string{"<2K", "2-8K", "8-16K", ">=16K"},
		true,
	))
	output.WriteString(`
    `)
	output.WriteString(htmlTokenCard(
		"Output tokens",
		profile.OutputTokens,
		[]string{"<500", "≥500"},
		false,
	))
	output.WriteString(`
    `)
	output.WriteString(htmlTPMCard(result))
	output.WriteString(`
    `)
	output.WriteString(htmlRateCard(result))
	output.WriteString(`
    `)
	output.WriteString(htmlBehaviorCard(result))
	output.WriteString(`
  </div>
  `)
	output.WriteString(htmlAggregateNote(result))
	output.WriteString(`
</section>`)
	return output.String()
}

func htmlLatencyCard(
	abbreviation string,
	title string,
	values model.LatencyDistribution,
	helpText string,
) string {
	available := values.P50MS != nil && values.P95MS != nil
	useSeconds := false
	for _, value := range []*float64{values.AverageMS, values.P50MS, values.P95MS} {
		if value != nil && *value >= 1000 {
			useSeconds = true
		}
	}
	source := "Unavailable from aggregate-only telemetry"
	if available {
		source = "Request-level distribution"
	}

	var output strings.Builder
	output.WriteString(`
<article class="latency-card">
  <div class="metric-head"><strong>`)
	output.WriteString(htmlEscape(title))
	output.WriteString(` (`)
	output.WriteString(htmlEscape(abbreviation))
	output.WriteString(`)</strong>
    <button class="info" type="button" aria-label="`)
	output.WriteString(htmlEscape(abbreviation))
	output.WriteString(` definition" data-help="`)
	output.WriteString(htmlEscape(helpText))
	output.WriteString(`">i</button></div>
  <div class="metric-hero">`)
	output.WriteString(htmlMetricDuration(values.P95MS, useSeconds))
	output.WriteString(` <small>P95</small></div>
  <div class="metric-pairs"><span><small>Average</small><strong>`)
	output.WriteString(htmlMetricDuration(values.AverageMS, useSeconds))
	output.WriteString(`</strong></span>
    <span><small>P50</small><strong>`)
	output.WriteString(htmlMetricDuration(values.P50MS, useSeconds))
	output.WriteString(`</strong></span></div>
  <div class="metric-source">`)
	output.WriteString(htmlEscape(source))
	output.WriteString(`</div>
</article>`)
	return output.String()
}

func htmlTokenCard(
	title string,
	values model.Distribution,
	bands []string,
	input bool,
) string {
	band := htmlTokenBand(values.P95, input)
	var renderedBands strings.Builder
	for _, item := range bands {
		className := ""
		if item == band {
			className = "active"
		}
		renderedBands.WriteString(`<span class="`)
		renderedBands.WriteString(className)
		renderedBands.WriteString(`">`)
		renderedBands.WriteString(htmlEscape(htmlDisplayTokenBucket(item)))
		renderedBands.WriteString(`</span>`)
	}
	displayBand := "Unavailable"
	if band != "" {
		displayBand = htmlDisplayTokenBucket(band)
	}

	var output strings.Builder
	output.WriteString(`
<article class="profile-card">
  <div class="profile-label">`)
	output.WriteString(htmlEscape(title))
	output.WriteString(`</div>
  <div class="profile-main">`)
	output.WriteString(htmlToken(values.P95))
	output.WriteString(` <small>P95</small></div>
  <div class="profile-meta"><span>P50 `)
	output.WriteString(htmlToken(values.P50))
	output.WriteString(`</span><span>Average `)
	output.WriteString(htmlToken(values.Average))
	output.WriteString(`</span></div>
  <div class="band-row" aria-label="`)
	output.WriteString(htmlEscape(title))
	output.WriteString(` benchmark bands">
    `)
	output.WriteString(renderedBands.String())
	output.WriteString(`
  </div>
  <p class="card-note">P95 band: `)
	output.WriteString(htmlEscape(displayBand))
	output.WriteString(`. Band percentages require request-level histogram data.</p>
</article>`)
	return output.String()
}

func htmlRateCard(result *model.AssessmentResult) string {
	rate := result.TrafficProfile.RequestRate
	chart := `<div class="trend-unavailable">Trend unavailable</div>`
	if rate.AverageRPM != nil && rate.PeakRPM != nil {
		maximum := max(*rate.PeakRPM, *rate.AverageRPM, 1)
		averageWidth := 100 * *rate.AverageRPM / maximum
		peakWidth := 100 * *rate.PeakRPM / maximum
		chart = fmt.Sprintf(`
<div class="rate-chart" role="img" aria-label="Average %s and peak %s requests per minute">
  <span>Avg</span><i><b style="width:%.1f%%"></b></i>
  <span>Peak</span><i><b style="width:%.1f%%"></b></i>
</div>`, htmlNumber(rate.AverageRPM), htmlNumber(rate.PeakRPM), averageWidth, peakWidth)
	}

	var output strings.Builder
	output.WriteString(`
<article class="profile-card">
  <div class="profile-label">Request rate</div>
  <div class="profile-main">`)
	output.WriteString(htmlNumber(rate.PeakRPM))
	output.WriteString(` <small>peak/min</small></div>
  <div class="profile-meta"><span>Average `)
	output.WriteString(htmlNumber(rate.AverageRPM))
	output.WriteString(`/min</span><span>`)
	output.WriteString(htmlBurstLabel(htmlBurstFactor(rate)))
	output.WriteString(`</span></div>
  `)
	output.WriteString(chart)
	output.WriteString(`
  <p class="card-note">A time-series trend is not inferred when only aggregate average and peak values are available.</p>
</article>`)
	return output.String()
}

func htmlBehaviorCard(result *model.AssessmentResult) string {
	profile := result.TrafficProfile
	var output strings.Builder
	output.WriteString(`
<article class="profile-card">
  <div class="metric-head"><span class="profile-label">Behavior and reliability</span>
    <button class="info" type="button" aria-label="Cache reuse definition" data-help="Share of prompt tokens served from prompt cache telemetry. Unavailable is distinct from a measured zero.">i</button></div>
  <div class="profile-main">`)
	output.WriteString(htmlPercent(profile.StreamingRatio))
	output.WriteString(` <small>streaming</small></div>
  <div class="profile-meta"><span>Prompt-token cache reuse</span><strong>`)
	output.WriteString(htmlPercent(profile.CacheHitRatio))
	output.WriteString(`</strong></div>
  <div class="reliability"><span><small>Errors</small><strong>`)
	output.WriteString(htmlPercent(profile.ErrorRate))
	output.WriteString(`</strong></span>
    <span><small>Throttled</small><strong>`)
	output.WriteString(htmlPercent(profile.ThrottlingRate))
	output.WriteString(`</strong></span></div>
</article>`)
	return output.String()
}

func htmlTPMCard(result *model.AssessmentResult) string {
	rate := result.TrafficProfile.TokenRate
	utilization := "Limit comparison unavailable"
	chart := `<div class="trend-unavailable">Deployment limit unavailable</div>`
	if rate.PeakLimitRatio != nil {
		utilization = "Peak " + htmlPercent(rate.PeakLimitRatio) + " of deployment limit"
		width := min(*rate.PeakLimitRatio*100, 100)
		chart = fmt.Sprintf(`
<div class="rate-chart" role="img" aria-label="%s">
  <span>Peak</span><i><b style="width:%.1f%%"></b></i>
</div>`, htmlEscape(utilization), width)
	}

	var output strings.Builder
	output.WriteString(`
<article class="profile-card">
  <div class="metric-head"><span class="profile-label">Token rate</span>
    <button class="info" type="button" aria-label="TPM definition" data-help="Observed input plus output tokens in one-minute Azure Monitor bins. The service rate-limit counter uses an estimated token count and can differ.">i</button></div>
  <div class="profile-main">`)
	output.WriteString(htmlNumber(rate.PeakTPM))
	output.WriteString(` <small>peak total TPM</small></div>
  <div class="tpm-breakdown">
    `)
	output.WriteString(htmlTPMBreakdownRow("Input", rate.InputAverageTPM, rate.InputPeakTPM))
	output.WriteString(`
    `)
	output.WriteString(htmlTPMBreakdownRow("Output", rate.OutputAverageTPM, rate.OutputPeakTPM))
	output.WriteString(`
  </div>
  <div class="profile-meta"><span>Total average `)
	output.WriteString(htmlNumber(rate.AverageTPM))
	output.WriteString(` TPM</span><span>Limit `)
	output.WriteString(htmlNumber(rate.LimitTPM))
	output.WriteString(` TPM</span></div>
  `)
	output.WriteString(chart)
	output.WriteString(`
  <p class="card-note">`)
	output.WriteString(htmlEscape(utilization))
	output.WriteString(`. Observed tokens; the service rate-limit estimate may differ.</p>
</article>`)
	return output.String()
}

func htmlTPMBreakdownRow(label string, average, peak *float64) string {
	return "<span><small>" + htmlEscape(label) + " TPM</small><strong>" +
		htmlNumber(average) + " avg · " + htmlNumber(peak) + " peak</strong></span>"
}

func htmlAggregateNote(result *model.AssessmentResult) string {
	if htmlHasFullRequestProfile(result.TrafficProfile) {
		return `<p class="data-note">Percentiles use request-level logs. ` +
			`Missing values remain unavailable rather than being inferred.</p>`
	}
	return `<p class="data-note warning">Aggregate-only data cannot provide request-level token ` +
		`distributions or latency percentiles.</p>`
}

func htmlRecommendationChain(result *model.AssessmentResult) string {
	if result.EvidencePackage != nil {
		return ""
	}
	var items strings.Builder
	if len(result.Recommendations) > 0 {
		for _, recommendation := range result.Recommendations {
			items.WriteString(htmlRecommendationItem(
				recommendation.ObservedPattern,
				recommendation.SupportingEvidence,
				recommendation.RecommendedAction,
				recommendation.Confidence,
			))
		}
	} else {
		items.WriteString(htmlRecommendationItem(
			htmlFallbackPattern(result),
			htmlFallbackEvidence(result),
			htmlNextAction(result),
			"High",
		))
	}
	return `
<section class="card section-card">
  <div class="section-heading"><div><h3>Recommended next steps</h3>
    <p>Each observed pattern is paired with evidence and a test to try.</p></div></div>
  <div class="recommendation-list">` + items.String() + `</div>
</section>`
}

func htmlRecommendationItem(pattern, evidence, action, confidence string) string {
	return `
<article class="recommendation-item">
  <div class="recommendation-body">
    <div class="recommendation-pattern">
      <div><small>Observed pattern</small><strong>` + htmlEscape(pattern) + `</strong></div>
      <span class="recommendation-confidence">` + htmlEscape(confidence) + ` confidence</span>
    </div>
    <div class="recommendation-evidence"><small>Evidence</small><p>` + htmlEscape(evidence) + `</p></div>
    <div class="recommendation-action"><small>Try this</small><p>` + htmlEscape(action) + `</p></div>
  </div>
</article>`
}

func htmlBenchmarkSection(result *model.AssessmentResult) string {
	benchmark := result.BenchmarkComparison
	if result.SloAssessment.Status != model.SloAboveTarget {
		return ""
	}

	confidence := "Coverage unavailable"
	pillClass := "warning"
	var body string
	if benchmark.Status == model.BenchmarkMatched {
		confidenceValue := "Unknown"
		if benchmark.Confidence != nil && *benchmark.Confidence != "" {
			confidenceValue = *benchmark.Confidence
		}
		confidence = confidenceValue + " match confidence"
		pillClass = "complete"
		body = `
<div class="benchmark-card"><div><h4>` + htmlEscape(htmlBenchmarkTitle(benchmark)) + `</h4><p>` +
			htmlEscape(benchmark.Message) + `</p></div>
<div class="benchmark-stats">
  <span><small>Similar workload benchmark</small><strong>` + htmlEscape(benchmark.Metric) + ` · ` +
			htmlDuration(benchmark.BenchmarkValueMS) + `</strong></span>
  <span><small>Observed ` + htmlEscape(benchmark.Metric) + `</small><strong>` +
			htmlDuration(benchmark.ActualMS) + `</strong></span>
  <span><small>Sample / freshness</small><strong>` + htmlInt(benchmark.SampleSize) + ` / ` +
			htmlEscape(htmlDisplayDate(benchmark.Freshness)) + `</strong></span>
</div></div>
` + htmlSelectedCohort(benchmark.SelectedCohort)
	} else {
		body = `<div class="coverage-gap"><strong>Coverage gap</strong><p>` +
			htmlEscape(benchmark.Message) + `</p><p>No unrelated global average was substituted.</p></div>`
	}
	return `
<section class="card section-card">
  <div class="section-heading"><div><h3>Comparison with a similar workload</h3>
    <p>Shown because the current offer target was exceeded.</p></div>
    <span class="pill ` + pillClass + `">` + htmlEscape(confidence) + `</span></div>
  ` + body + `
</section>`
}

func htmlSelectedCohort(cohort *model.SelectedBenchmarkCohort) string {
	if cohort == nil {
		return ""
	}
	chips := []string{
		"Input " + htmlDisplayTokenBucket(cohort.InputTokenBucket),
		"Output " + htmlDisplayTokenBucket(cohort.OutputTokenBucket),
		cohort.APIPath,
		htmlRegionLabel(cohort.Region),
	}
	if cohort.CacheHitRate != nil {
		chips = append(chips, "Cache "+htmlPercent(cohort.CacheHitRate))
	}
	var renderedChips strings.Builder
	for _, chip := range chips {
		if chip != "" {
			renderedChips.WriteString("<span>")
			renderedChips.WriteString(htmlEscape(chip))
			renderedChips.WriteString("</span>")
		}
	}
	return `
<div class="selected-cohort">
  <small>Compared with this workload</small>
  <strong>` + htmlEscape(cohort.Model) + ` / ` + htmlEscape(cohort.ModelVersion) + ` · ` +
		htmlEscape(htmlOfferingTypeLabel(cohort.OfferingType)) + ` · ` +
		htmlEscape(htmlPythonTitle(cohort.StreamingMode)) + `</strong>
  <div class="cohort-chips">` + renderedChips.String() + `</div>
</div>`
}

func htmlBenchmarkTitle(benchmark model.BenchmarkComparison) string {
	if benchmark.WorkloadProfileExplainsGap != nil && *benchmark.WorkloadProfileExplainsGap {
		return "Observed latency is at or below the benchmark reference"
	}
	return "Observed latency is above the benchmark reference"
}

func htmlDiagnosticSetup(result *model.AssessmentResult) string {
	if htmlHasFullRequestProfile(result.TrafficProfile) {
		return ""
	}
	return `
<section class="card section-card setup">
  <div class="section-heading"><div><h3>Enable a full traffic profile</h3>
    <p>Request-level logs are required for token distributions and TTFT/TBT/TTLT percentiles.</p></div>
    <span class="pill warning">Data required</span></div>
  <ol class="setup-steps">
    <li><strong>Enable resource diagnostic logs.</strong></li>
    <li><strong>Route <code>RequestResponse</code> and <code>AzureOpenAIRequestUsage</code> to Log Analytics.</strong></li>
    <li><strong>Collect representative traffic.</strong></li>
    <li><strong>Rerun the assessment.</strong></li>
  </ol>
</section>`
}

func htmlOfferExplorer(result *model.AssessmentResult) string {
	options := result.EligibleOfferOptions
	if !htmlIsPayGo(result.TrafficProfile.Deployment.Offer) ||
		result.EvidencePackage != nil ||
		len(options) == 0 {
		return ""
	}

	scenario := optionalString(result.Scenario)
	if scenario == "" {
		scenario = "assessment"
	}
	group := "offer-goal-" + htmlEscape(scenario)
	var goals strings.Builder
	var panels strings.Builder
	for _, option := range options {
		goals.WriteString(htmlOfferGoal(option, group))
		panels.WriteString(`<div class="offer-result" data-offer-result="`)
		panels.WriteString(htmlEscape(htmlOfferGoalID(option)))
		panels.WriteString(`">`)
		panels.WriteString(htmlOfferCard(option))
		panels.WriteString(`</div>`)
	}
	return `
<details class="card offer-section">
  <summary><span><strong>Explore offer options <em>(optional)</em></strong>
    <small>Choose the outcome that matters most. This guidance does not explain the assessment result.</small></span></summary>
  <div class="offer-content">
    <fieldset class="offer-goals">
      <legend>What do you want to improve?</legend>
      <div class="offer-goal-grid">` + goals.String() + `</div>
    </fieldset>
    <p class="offer-note">Predictable and consistent latency both mean less variation between typical and slow-tail latency—for example, P95 closer to P50. Neither is a guarantee.</p>
    <div class="offer-results" aria-live="polite">` + panels.String() + `</div>
  </div>
</details>`
}

func htmlOfferCard(option model.OfferOption) string {
	return `
<article class="offer-card">
  <div class="offer-card-heading"><h4>` + htmlEscape(option.Offer) +
		`</h4><span class="pill complete">` + htmlEscape(option.Availability) + `</span></div>
  <div class="offer-card-copy">
    <small>Best for</small><p>` + htmlEscape(optionalString(option.FitReason)) + `</p>
    <small>Trade-off</small><p>` + htmlEscape(optionalString(option.Tradeoff)) + `</p>
  </div>
  <p class="safe-note">Availability is confirmed, but latency improvement is not guaranteed.</p>
</article>`
}

func htmlOfferGoal(option model.OfferOption, group string) string {
	title := "More predictable latency"
	description := "Steady, sustained traffic where reserved capacity is acceptable."
	if option.Offer == model.OfferPriorityProcessing {
		title = "Lower latency for traffic spikes"
		description = "Bursty or variable interactive traffic without reserved capacity."
	}
	return `
<label class="offer-goal">
  <input type="radio" name="` + group + `" value="` + htmlEscape(htmlOfferGoalID(option)) + `">
  <span><strong>` + htmlEscape(title) + `</strong><small>` + htmlEscape(description) + `</small></span>
</label>`
}

func htmlOfferGoalID(option model.OfferOption) string {
	if option.Offer == model.OfferPriorityProcessing {
		return "traffic-spikes"
	}
	return "predictable-latency"
}

func htmlScenarioSelector(results []*model.AssessmentResult, selected *string) string {
	if len(results) <= 1 {
		return ""
	}
	primary := map[string]bool{
		"within-target":      true,
		"workload-explained": true,
		"unexplained-gap":    true,
		"logs-unavailable":   true,
	}
	var buttons strings.Builder
	for _, result := range results {
		scenario := optionalString(result.Scenario)
		if !primary[scenario] {
			continue
		}
		className := "scenario-button"
		if equalOptionalString(result.Scenario, selected) {
			className += " active"
		}
		buttons.WriteString(`<button type="button" class="`)
		buttons.WriteString(className)
		buttons.WriteString(`" data-target="`)
		buttons.WriteString(htmlEscape(scenario))
		buttons.WriteString(`">`)
		buttons.WriteString(htmlEscape(htmlShortScenario(scenario)))
		buttons.WriteString(`</button>`)
	}
	return `<section class="card scenario-picker"><strong>Review a typical decision path</strong>
<small>Demo-only control; not part of the production experience.</small><div>` +
		buttons.String() + `</div></section>`
}
