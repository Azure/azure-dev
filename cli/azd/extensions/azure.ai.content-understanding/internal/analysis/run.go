// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"
	"azure.ai.content-understanding/internal/localio"
)

type RunOptions struct {
	Analyzer    string
	Mode        cuclient.AnalysisMode
	Concurrency int
	Usage       bool
}

type Record struct {
	Input    string          `json:"input"`
	Status   string          `json:"status"`
	Analyzer string          `json:"analyzerId,omitempty"`
	Output   *string         `json:"output"`
	Usage    json.RawMessage `json:"usage,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type Execution struct {
	Records []Record
	Stream  any
}

type outcome struct {
	index   int
	payload any
	usage   json.RawMessage
	err     error
}

type reportRecord struct {
	Input    string          `json:"input"`
	Status   string          `json:"status"`
	Analyzer string          `json:"analyzer,omitempty"`
	Output   *string         `json:"output"`
	Usage    json.RawMessage `json:"usage,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type reportCounts struct {
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
	Total     int `json:"total"`
}

type report struct {
	Schema   string         `json:"schema"`
	Analyzer string         `json:"analyzer"`
	View     string         `json:"result_view"`
	Counts   reportCounts   `json:"counts"`
	Results  []reportRecord `json:"results"`
}

func Execute(ctx context.Context, client cuclient.Client, plan Plan, options RunOptions) (Execution, error) {
	if err := ctx.Err(); err != nil {
		return Execution{}, err
	}
	if len(plan.Outputs) == 0 {
		return Execution{}, invalid("empty_inputs", "Select at least one analysis input.")
	}
	if err := cuclient.ValidateAnalyzerID(options.Analyzer); err != nil {
		return Execution{}, err
	}
	if options.Concurrency < 1 || options.Concurrency > 32 {
		return Execution{}, invalid("invalid_concurrency", "--concurrency must be between 1 and 32.")
	}
	if options.Mode != cuclient.AnalysisModeAsync && options.Mode != cuclient.AnalysisModeInline {
		return Execution{}, invalid("invalid_analysis_mode", "Select async or inline analysis.")
	}
	result := Execution{Records: make([]Record, len(plan.Outputs))}
	inputs := make([]Input, len(plan.Outputs))
	var jobs []int
	for index, output := range plan.Outputs {
		inputs[index] = output.Input
		record := Record{
			Input: output.Input.Reference, Output: output.Path,
			Status: "failed", Error: "Analysis was stopped before completion.",
		}
		if output.Skipped {
			record.Status, record.Error, record.Reason = "skipped", "", "result file already exists"
		} else {
			jobs = append(jobs, index)
		}
		result.Records[index] = record
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	completed := analyzeInputs(workCtx, client, inputs, jobs, plan.LLMInput, options)
	var publishErr error
	for item := range completed {
		record := &result.Records[item.index]
		if item.err != nil {
			record.Error = safeDiagnostic(item.err)
			continue
		}
		if err := workCtx.Err(); err != nil {
			record.Error = safeDiagnostic(err)
			continue
		}
		output := plan.Outputs[item.index]
		if output.Path != nil {
			if err := savePayload(*output.Path, item.payload, plan.Existing == "reanalyze"); err != nil {
				record.Error = "The analysis result could not be saved."
				publishErr = errors.Join(publishErr, err)
				cancel()
				continue
			}
		} else {
			result.Stream = item.payload
			if options.Usage {
				result.Stream = struct {
					Result any             `json:"result"`
					Usage  json.RawMessage `json:"usage"`
				}{item.payload, item.usage}
			}
		}
		record.Status, record.Error, record.Analyzer = "succeeded", "", options.Analyzer
		record.Usage = json.RawMessage("null")
		if options.Usage {
			record.Usage = item.usage
		}
	}
	summary := makeReport(options.Analyzer, plan.LLMInput, result.Records)
	if plan.Report != "" {
		if err := savePayload(plan.Report, summary, false); err != nil {
			return result, &cuclient.Error{
				Kind: cuclient.ErrorService, Code: "report_write_failed",
				Message: "The analysis report could not be saved; completed result files were retained.", Cause: err,
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if publishErr != nil {
		return result, &cuclient.Error{
			Kind: cuclient.ErrorService, Code: "result_write_failed",
			Message: "An analysis result could not be saved; pending analyses were stopped.", Cause: publishErr,
		}
	}
	if summary.Counts.Failed != 0 {
		return result, &cuclient.Error{
			Kind: cuclient.ErrorService, Code: "analysis_failed",
			Message: fmt.Sprintf(
				"%d analysis input(s) failed; successful result files were retained.", summary.Counts.Failed),
			Suggestion: "Use --report-file to inspect per-input outcomes.",
		}
	}
	return result, nil
}

func analyzeInputs(
	ctx context.Context, client cuclient.Client, inputs []Input, jobs []int, llm bool, options RunOptions,
) <-chan outcome {
	completed := make(chan outcome)
	var next atomic.Int64
	var workers sync.WaitGroup
	for range min(options.Concurrency, len(jobs)) {
		workers.Go(func() {
			for ctx.Err() == nil {
				position := int(next.Add(1) - 1)
				if position >= len(jobs) {
					return
				}
				index := jobs[position]
				payload, usage, err := analyzeOne(ctx, client, inputs[index], llm, options)
				// Drain all started jobs before closing the service session, including on cancellation.
				completed <- outcome{index: index, payload: payload, usage: usage, err: err}
			}
		})
	}
	go func() {
		workers.Wait()
		close(completed)
	}()
	return completed
}

func analyzeOne(
	ctx context.Context, client cuclient.Client, input Input, llm bool, options RunOptions,
) (any, json.RawMessage, error) {
	request := cuclient.AnalyzeRequest{
		AnalyzerID: options.Analyzer, Mode: options.Mode, Input: cuclient.Input{URL: input.URL},
	}
	var file *os.File
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if input.Path != "" {
		var err error
		file, err = input.Open()
		if err != nil {
			return nil, nil, err
		}
		request.Input.Body, request.Input.ContentType = file, "application/octet-stream"
	}
	var response cuclient.AnalyzeResponse
	err := ctx.Err()
	if err == nil {
		response, err = client.Analyze(ctx, request)
	}
	if file != nil {
		err = errors.Join(err, file.Close())
	}
	if err != nil {
		return nil, nil, err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response.JSON, &envelope); err != nil || envelope == nil {
		return nil, nil, invalidAnalysisResponse()
	}
	if nested := bytes.TrimSpace(envelope["result"]); len(nested) == 0 || nested[0] != '{' {
		return nil, nil, invalidAnalysisResponse()
	}
	usage := envelope["usage"]
	if len(usage) == 0 {
		var err error
		usage, err = json.Marshal(response.Usage)
		if err != nil {
			return nil, nil, err
		}
	}
	if llm {
		text, err := RenderLLM(envelope["result"])
		return text, usage, err
	}
	return response.JSON, usage, nil
}

func invalidAnalysisResponse() error {
	return &cuclient.Error{
		Kind: cuclient.ErrorService, Code: "invalid_analysis_response",
		Message: "The CU client returned an invalid complete analysis response.",
	}
}

func savePayload(path string, payload any, replace bool) error {
	var body []byte
	if text, ok := payload.(string); ok {
		body = []byte(text)
	} else {
		var err error
		body, err = json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return err
		}
		body = append(body, '\n')
	}
	return localio.Write(path, body, replace)
}

func makeReport(analyzer string, llm bool, records []Record) report {
	summary := report{
		Schema: "cu-cli/analyze-report/v1", Analyzer: analyzer, View: "full",
		Counts: reportCounts{Total: len(records)}, Results: make([]reportRecord, len(records)),
	}
	if llm {
		summary.View = "llm-input"
	}
	for index, record := range records {
		switch record.Status {
		case "succeeded":
			summary.Counts.Succeeded++
		case "skipped":
			summary.Counts.Skipped++
		case "failed":
			summary.Counts.Failed++
		}
		summary.Results[index] = reportRecord(record)
	}
	return summary
}

func safeDiagnostic(err error) string {
	if errors.Is(err, context.Canceled) {
		return "Analysis was cancelled."
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Analysis timed out."
	}
	if domain, ok := errors.AsType[*cuclient.Error](err); ok {
		return exterrors.Redact(domain.Message)
	}
	return "Analysis failed; check the input, authentication, and service availability."
}
