// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"path/filepath"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Generation source building
// ---------------------------------------------------------------------------

// TraceOptions holds optional trace inclusion parameters for generation sources.
type TraceOptions struct {
	Days int
}

// BuildGenerationSources constructs the sources array for generation jobs.
// For prompt agents (agentKind == "prompt"), only the agent source is included.
// For other agent kinds, a prompt source is included when instruction is
// non-empty, along with the agent source. When traces is non-nil and Days > 0,
// a traces source is appended with start_time computed from the current time.
func BuildGenerationSources(agentKind, agentName, version, instruction string, traces *TraceOptions) []GenerationSource {
	var sources []GenerationSource

	if agentKind != "prompt" && instruction != "" {
		sources = append(sources, GenerationSource{
			Type:   "prompt",
			Prompt: instruction,
		})
	}

	agentSource := GenerationSource{
		Type:      "agent",
		AgentName: agentName,
	}
	if version != "" {
		agentSource.AgentVersion = version
	}
	sources = append(sources, agentSource)

	if traces != nil && traces.Days > 0 {
		startTime := time.Now().AddDate(0, 0, -traces.Days).Unix()
		sources = append(sources, GenerationSource{
			Type:      "traces",
			AgentName: agentName,
			StartTime: startTime,
		})
	}

	return sources
}

// ---------------------------------------------------------------------------
// Request builders
// ---------------------------------------------------------------------------

// NewDataGenerationJobRequest builds a DataGenerationJobRequest from the
// provided parameters. When sources contain a "traces" entry, the generation
// type is set to "traces"; otherwise it defaults to "simple_qna".
func NewDataGenerationJobRequest(
	name, evalModel string,
	maxSamples int,
	sources []GenerationSource,
) *DataGenerationJobRequest {
	genType := "simple_qna"
	for _, s := range sources {
		if s.Type == "traces" {
			genType = "traces"
			break
		}
	}
	return &DataGenerationJobRequest{
		Inputs: DataGenerationInputs{
			Name:     name,
			Scenario: "evaluation",
			Options: DataGenerationOptions{
				Type:       genType,
				MaxSamples: maxSamples,
				ModelOptions: ModelOptions{
					Model: evalModel,
				},
			},
			Sources: sources,
		},
	}
}

// NewEvaluatorGenerationJobRequest builds an EvaluatorGenerationJobRequest
// from the provided parameters.
func NewEvaluatorGenerationJobRequest(
	name, evalModel string,
	sources []GenerationSource,
) *EvaluatorGenerationJobRequest {
	return &EvaluatorGenerationJobRequest{
		Name:          name,
		EvaluatorName: name,
		Category:      "quality",
		Model:         evalModel,
		Sources:       sources,
	}
}

// ---------------------------------------------------------------------------
// Evaluator classification
// ---------------------------------------------------------------------------

// IsBuiltinEvaluator returns true when the evaluator name has the "builtin."
// prefix.
func IsBuiltinEvaluator(name string) bool {
	return strings.HasPrefix(name, "builtin.")
}

// SplitEvaluators partitions evaluators into generated (non-builtin) and
// built-in lists.
func SplitEvaluators(evaluators []string) (generated, builtin []string) {
	for _, e := range evaluators {
		if IsBuiltinEvaluator(e) {
			builtin = append(builtin, e)
		} else {
			generated = append(generated, e)
		}
	}
	return generated, builtin
}

// ---------------------------------------------------------------------------
// Dataset name detection
// ---------------------------------------------------------------------------

// IsDatasetName returns true when the value looks like a registered dataset
// name rather than a local file path. A name has no path separators and no
// common data-file extension (.jsonl, .json, .csv).
func IsDatasetName(value string) bool {
	if value == "" {
		return false
	}
	if strings.ContainsAny(value, "/\\") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(value))
	return ext != ".jsonl" && ext != ".json" && ext != ".csv"
}
