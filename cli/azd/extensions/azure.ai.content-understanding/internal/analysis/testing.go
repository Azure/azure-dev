// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/localio"
)

const testDisclaimer = "This is not a real accuracy benchmark. It only checks whether a value was " +
	"extracted or generated, and reports confidence when the service returns it."

type TestField struct {
	Value      any      `json:"value"`
	Confidence *float64 `json:"confidence"`
}

type TestSample struct {
	Input  string               `json:"input"`
	Status string               `json:"status"`
	Error  string               `json:"error,omitempty"`
	Fields map[string]TestField `json:"fields"`
}

type TestFieldSummary struct {
	Populated          int      `json:"populated"`
	PopulatedPct       float64  `json:"populatedPct"`
	MeanConfidence     *float64 `json:"meanConfidence"`
	LowConfidenceCount int      `json:"lowConfidenceCount"`
}

type TestSummary struct {
	Total                  int                         `json:"samplesTotal"`
	OK                     int                         `json:"samplesOk"`
	Failed                 int                         `json:"samplesFailed"`
	Disclaimer             string                      `json:"disclaimer"`
	Fields                 map[string]TestFieldSummary `json:"fields"`
	LowConfidenceThreshold float64                     `json:"lowConfidenceThreshold"`
}

type TestReport struct {
	Analyzer string       `json:"analyzerId"`
	Summary  TestSummary  `json:"summary"`
	Samples  []TestSample `json:"samples"`
}

func CheckTestReport(path string, inputs []Input, force bool) error {
	if path == "" {
		return nil
	}
	working, err := os.Getwd()
	if err != nil {
		return invalid("invalid_working_directory", "The working directory could not be resolved.")
	}
	exists, err := localio.Check(path)
	if err != nil || AliasesInput(path, inputs, working) {
		return invalid("invalid_test_report", "The test report must be a regular file path distinct from all inputs.")
	}
	if exists && !force {
		return &cuclient.Error{Kind: cuclient.ErrorConflict, Code: "test_report_exists",
			Message: "The test report already exists.", Suggestion: "Pass --force to replace the report explicitly."}
	}
	return nil
}

func RunTest(
	ctx context.Context, client cuclient.Client, inputs []Input, analyzer string, concurrency int,
) (TestReport, error) {
	if err := ctx.Err(); err != nil {
		return TestReport{}, err
	}
	if err := cuclient.ValidateAnalyzerID(analyzer); err != nil {
		return TestReport{}, err
	}
	if concurrency < 1 || concurrency > 16 || len(inputs) == 0 {
		return TestReport{}, invalid("invalid_test_inputs", "Select local samples and concurrency between 1 and 16.")
	}
	report := TestReport{Analyzer: analyzer, Samples: make([]TestSample, len(inputs))}
	jobs := make([]int, len(inputs))
	for i, input := range inputs {
		if input.URL != "" || input.Path == "" {
			return TestReport{}, invalid("local_samples_required",
				"Analyzer test accepts local files and directories, not URLs.")
		}
		jobs[i] = i
		report.Samples[i] = TestSample{Input: input.Reference, Status: "error",
			Error: "Analysis was stopped before completion.", Fields: map[string]TestField{}}
	}
	for item := range analyzeInputs(ctx, client, inputs, jobs, false, RunOptions{
		Analyzer: analyzer, Mode: cuclient.AnalysisModeAsync, Concurrency: concurrency,
	}) {
		sample := &report.Samples[item.index]
		err := item.err
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			raw, ok := item.payload.(json.RawMessage)
			if !ok {
				err = invalidAnalysisResponse()
			} else {
				sample.Fields, err = testFields(raw)
			}
		}
		if err != nil {
			sample.Fields = map[string]TestField{}
			message := []rune(safeDiagnostic(err))
			sample.Error = string(message[:min(len(message), 500)])
			continue
		}
		sample.Status, sample.Error = "ok", ""
	}
	report.Summary = summarizeTest(report.Samples)
	return report, ctx.Err()
}

func SaveTestReport(path string, report TestReport, force bool) error {
	if err := savePayload(path, report, force); err != nil {
		return &cuclient.Error{Kind: cuclient.ErrorService, Code: "test_report_write_failed",
			Message: "The analyzer test report could not be saved.", Cause: err}
	}
	return nil
}

func testFields(raw json.RawMessage) (map[string]TestField, error) {
	var envelope struct {
		Result struct {
			Contents []struct {
				Fields json.RawMessage `json:"fields"`
			} `json:"contents"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, invalidAnalysisResponse()
	}
	result := map[string]TestField{}
	for _, content := range envelope.Result.Contents {
		if len(content.Fields) == 0 || string(content.Fields) == "null" {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(content.Fields))
		token, err := decoder.Token()
		if err != nil || token != json.Delim('{') {
			return nil, invalidAnalysisResponse()
		}
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return nil, invalidAnalysisResponse()
			}
			name, ok := token.(string)
			if !ok {
				return nil, invalidAnalysisResponse()
			}
			var raw json.RawMessage
			if decoder.Decode(&raw) != nil {
				return nil, invalidAnalysisResponse()
			}
			if previous, ok := result[name]; ok && nonemptyTestValue(previous.Value) {
				continue
			}
			value, err := decodeValue(raw)
			if err != nil {
				return nil, invalidAnalysisResponse()
			}
			field, err := coerceTestField(value)
			if err != nil {
				return nil, err
			}
			result[name] = field
			mapped, _ := value.(map[string]any)
			if children, ok := mapped["valueObject"].(map[string]any); ok {
				for key, child := range children {
					entry, err := coerceTestField(child)
					if err != nil {
						return nil, err
					}
					result[name+"."+key] = entry
				}
			} else if array, ok := mapped["valueArray"].([]any); ok {
				values := map[string][]any{}
				confidences := map[string][]float64{}
				for _, item := range array {
					itemMap, _ := item.(map[string]any)
					children, _ := itemMap["valueObject"].(map[string]any)
					for key, child := range children {
						entry, err := coerceTestField(child)
						if err != nil {
							return nil, err
						}
						values[key] = append(values[key], entry.Value)
						if entry.Confidence != nil {
							confidences[key] = append(confidences[key], *entry.Confidence)
						}
					}
				}
				for key, values := range values {
					result[name+"[]."+key] = TestField{Value: values, Confidence: meanConfidence(confidences[key])}
				}
			}
		}
	}
	return result, nil
}

func coerceTestField(value any) (TestField, error) {
	coerced, err := coerceTestValue(value)
	if err != nil {
		return TestField{}, err
	}
	field := TestField{Value: coerced}
	mapped, _ := value.(map[string]any)
	if confidence := mapped["confidence"]; confidence != nil {
		text := fmt.Sprint(confidence)
		if boolean, ok := confidence.(bool); ok {
			text = "0"
			if boolean {
				text = "1"
			}
		}
		if number, err := strconv.ParseFloat(text, 64); err == nil && !math.IsNaN(number) && !math.IsInf(number, 0) {
			field.Confidence = &number
		}
	}
	return field, nil
}

func coerceTestValue(value any) (any, error) {
	mapped, ok := value.(map[string]any)
	if !ok {
		return value, nil
	}
	for _, key := range []string{
		"valueString", "valueNumber", "valueInteger", "valueBoolean", "valueDate", "valueTime", "valueJson",
	} {
		if mapped[key] != nil {
			return mapped[key], nil
		}
	}
	if raw := mapped["valueArray"]; raw != nil {
		array, ok := raw.([]any)
		if !ok {
			return nil, invalidAnalysisResponse()
		}
		result := make([]any, len(array))
		for i, child := range array {
			var err error
			result[i], err = coerceTestValue(child)
			if err != nil {
				return nil, err
			}
		}
		return result, nil
	}
	if object, ok := mapped["valueObject"].(map[string]any); ok {
		result := map[string]any{}
		for name, child := range object {
			var err error
			result[name], err = coerceTestValue(child)
			if err != nil {
				return nil, err
			}
		}
		return result, nil
	}
	for _, key := range []string{"value", "content"} {
		if mapped[key] != nil {
			return mapped[key], nil
		}
	}
	return nil, nil
}

func nonemptyTestValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case []any:
		return len(v) != 0
	case map[string]any:
		return len(v) != 0
	default:
		return true
	}
}

func populatedTestValue(value any) bool {
	switch v := value.(type) {
	case []any:
		return slices.ContainsFunc(v, populatedTestValue)
	case map[string]any:
		for _, child := range v {
			if populatedTestValue(child) {
				return true
			}
		}
		return false
	default:
		return nonemptyTestValue(value)
	}
}

func roundTestStatistic(value float64, digits int) float64 {
	// Formatting a finite float guarantees parsing succeeds and matches Python's binary-float rounding.
	rounded, _ := strconv.ParseFloat(strconv.FormatFloat(value, 'f', digits, 64), 64)
	return rounded
}

func meanConfidence(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	value := roundTestStatistic(sum/float64(len(values)), 3)
	return &value
}

func summarizeTest(samples []TestSample) TestSummary {
	summary := TestSummary{Total: len(samples), Disclaimer: testDisclaimer,
		Fields: map[string]TestFieldSummary{}, LowConfidenceThreshold: 0.6}
	confidences := map[string][]float64{}
	for _, sample := range samples {
		if sample.Status == "ok" {
			summary.OK++
		}
		for name, field := range sample.Fields {
			stat := summary.Fields[name]
			if field.Value != nil {
				if populatedTestValue(field.Value) {
					stat.Populated++
				}
				if field.Confidence != nil {
					confidences[name] = append(confidences[name], *field.Confidence)
					if *field.Confidence < 0.6 {
						stat.LowConfidenceCount++
					}
				}
			}
			summary.Fields[name] = stat
		}
	}
	summary.Failed = summary.Total - summary.OK
	for name, stat := range summary.Fields {
		stat.PopulatedPct = roundTestStatistic(float64(stat.Populated)/float64(summary.Total)*100, 1)
		stat.MeanConfidence = meanConfidence(confidences[name])
		summary.Fields[name] = stat
	}
	return summary
}
