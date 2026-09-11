// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package report

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"azure.ai.latency/internal/model"
)

// RenderHTML writes a self-contained HTML report for one or more assessments to writer.
func RenderHTML(writer io.Writer, results []*model.AssessmentResult) error {
	if writer == nil {
		return fmt.Errorf("HTML report writer is required")
	}
	content, err := renderHTMLReport(results)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(writer, content); err != nil {
		return fmt.Errorf("write HTML report: %w", err)
	}
	return nil
}

// WriteHTML creates parent directories and writes a self-contained HTML report to path.
func WriteHTML(path string, results []*model.AssessmentResult) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("HTML report path is required")
	}

	var content bytes.Buffer
	if err := RenderHTML(&content, results); err != nil {
		return err
	}

	cleanPath := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o750); err != nil {
		return fmt.Errorf("create HTML report directory: %w", err)
	}
	file, err := os.OpenFile(cleanPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create HTML report: %w", err)
	}
	reportBytes := content.Bytes()
	if runtime.GOOS == "windows" {
		reportBytes = bytes.ReplaceAll(reportBytes, []byte("\n"), []byte("\r\n"))
	}
	_, writeErr := file.Write(reportBytes)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("persist HTML report: %w", err)
	}
	return nil
}

func renderHTMLReport(results []*model.AssessmentResult) (string, error) {
	if len(results) == 0 {
		return "", fmt.Errorf("at least one assessment result is required")
	}
	for index, result := range results {
		if result == nil {
			return "", fmt.Errorf("assessment result %d is nil", index+1)
		}
	}

	selected := results[0].Scenario
	selector := htmlScenarioSelector(results, selected)
	var panels strings.Builder
	for _, result := range results {
		active := len(results) == 1 || equalOptionalString(result.Scenario, selected)
		className := "scenario-panel"
		if active {
			className += " active"
		}
		panels.WriteString(`<article class="`)
		panels.WriteString(className)
		panels.WriteString(`" data-scenario="`)
		panels.WriteString(htmlEscape(optionalString(result.Scenario)))
		panels.WriteString(`">`)
		panel, err := htmlResultPanel(result)
		if err != nil {
			return "", err
		}
		panels.WriteString(panel)
		panels.WriteString(`</article>`)
	}

	disclosure := ""
	if len(results[0].IllustrativeComponents) > 0 {
		disclosure = " Illustrative sample values are used in this scenario."
	}

	var output strings.Builder
	output.WriteString(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Model Latency Self-Service Tool</title>
<style>`)
	output.WriteString(htmlCSS)
	output.WriteString(`</style>
</head>
<body>
<header class="topbar"><div class="shell">
<strong>Model Latency Self-Service Tool</strong>
<h1>Model latency assessment</h1>
<p>Understand the outcome, review the evidence, and choose the next safe action.</p>
</div></header>
<main class="shell">`)
	output.WriteString(selector)
	output.WriteString(panels.String())
	output.WriteString(`</main>
<footer>Feedback-oriented proof of concept. Workload hypotheses are not confirmed root-cause conclusions.`)
	output.WriteString(htmlEscape(disclosure))
	output.WriteString(`</footer>`)
	output.WriteString(htmlInteractionScript(len(results) > 1))
	output.WriteString(`
</body>
</html>`)
	return output.String(), nil
}

func htmlResultPanel(result *model.AssessmentResult) (string, error) {
	var output strings.Builder
	output.WriteString(htmlAssessmentCard(result))
	output.WriteString(htmlTrafficProfile(result))
	output.WriteString(htmlBenchmarkSection(result))
	output.WriteString(htmlRecommendationChain(result))
	evidence, err := htmlEvidencePackage(result)
	if err != nil {
		return "", err
	}
	output.WriteString(evidence)
	output.WriteString(htmlDiagnosticSetup(result))
	output.WriteString(htmlOfferExplorer(result))
	return output.String(), nil
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func equalOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
