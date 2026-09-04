// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"path/filepath"
	"strings"
	"time"

	"azureaieval/internal/pkg/evalcore"
)

// ---------------------------------------------------------------------------
// Generation source building
// ---------------------------------------------------------------------------

// TraceOptions holds optional trace inclusion parameters for generation sources.
type TraceOptions struct {
	Days int
}

// WithoutAgentSource returns the sources with the agent entry removed.
//
// Agent-seeded data generation currently fails server-side for every agent,
// while the same request carrying only the prompt succeeds, so this is what a
// retry falls back to.
func WithoutAgentSource(sources []GenerationSource) []GenerationSource {
	kept := make([]GenerationSource, 0, len(sources))
	for _, s := range sources {
		if s.Type == "agent" {
			continue
		}
		kept = append(kept, s)
	}
	return kept
}

// HasPromptSource reports whether anything remains to generate from.
func HasPromptSource(sources []GenerationSource) bool {
	for _, s := range sources {
		if s.Type == "prompt" && s.Prompt != "" {
			return true
		}
	}
	return false
}

// BuildGenerationSources emits the sources the caller selected, in a stable
// order, along with the ones it asked for and nothing could be built from.
//
// kinds is what --from named. An empty kinds means "whatever this plan has to
// offer" and reports nothing missing: the caller expressed no preference, so
// there is nothing to disappoint. Naming a kind explicitly is a request, and a
// request that cannot be built is worth saying out loud rather than quietly
// submitting a job seeded from less than was asked for.
func BuildGenerationSources(
	kinds []string,
	agentName, version, instruction string,
	traces *TraceOptions,
) (sources []GenerationSource, unbuildable []string) {
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	// Empty kinds selects everything available; a populated one selects only
	// what it names.
	selected := func(kind string) bool {
		return len(want) == 0 || want[kind]
	}
	// asked distinguishes "the default swept this up" from "the user typed it",
	// which is what decides whether an empty-handed source is an error.
	asked := func(kind string) bool { return want[kind] }

	// A traces source names the agent whose traces to read, but the service
	// still requires a prompt or an agent beside it, so traces on their own are
	// refused for every agent. The agent travels with them for the same reason
	// it travels with a prompt below. Verified against the service: traces alone
	// is a 400, traces plus agent is accepted.
	tracesNeedTheAgent := asked("traces") && agentName != ""

	// The agent is settled first because whether it was built decides whether
	// its instructions have anything to be the instructions of.
	var agentSource *GenerationSource
	if selected("agent") || tracesNeedTheAgent {
		switch {
		case agentName != "":
			agentSource = &GenerationSource{Type: "agent", AgentName: agentName}
			if version != "" {
				agentSource.AgentVersion = version
			}
		case asked("agent"):
			unbuildable = append(unbuildable, "agent")
		}
	}

	// Generating from an agent means generating from its instructions, so they
	// travel with it as a prompt. That is also the only shape the service
	// currently honours: the agent source alone fails for every agent, and the
	// prompt is what the retry in generateDataset falls back to. Without this,
	// `--from agent` would be a request that always fails, and `--from traces`
	// would have nothing to fall back to when agent seeding fails.
	promptCarriesTheAgent := agentSource != nil && (asked("agent") || tracesNeedTheAgent)
	if selected("prompt") || promptCarriesTheAgent {
		switch {
		case instruction != "":
			sources = append(sources, GenerationSource{
				Type:   "prompt",
				Prompt: instruction,
			})
		case asked("prompt"):
			unbuildable = append(unbuildable, "prompt")
		}
	}

	if agentSource != nil {
		sources = append(sources, *agentSource)
	}

	if selected("traces") {
		// A window narrows the request; it does not authorize it. Asking for
		// traces without one means every trace the agent has.
		switch {
		case agentName == "" && asked("traces"):
			// Without an agent the source names nothing to read and carries
			// nothing the service accepts beside it.
			unbuildable = append(unbuildable, "traces")
		case traces != nil && traces.Days > 0:
			sources = append(sources, GenerationSource{
				Type:      "traces",
				AgentName: agentName,
				StartTime: time.Now().AddDate(0, 0, -traces.Days).Unix(),
			})
		case asked("traces"):
			sources = append(sources, GenerationSource{
				Type:      "traces",
				AgentName: agentName,
			})
		}
	}

	// The service takes a file's rows through the dataset upload path, not
	// through a generation source, so there is nothing here to build one from.
	if asked("file") {
		unbuildable = append(unbuildable, "file")
	}

	return sources, unbuildable
}

// ---------------------------------------------------------------------------
// Request builders
// ---------------------------------------------------------------------------

// NewDataGenerationJobRequest builds a DataGenerationJobRequest from the
// provided parameters. Currently, it's always "simple_qna" type with multiple sources
func NewDataGenerationJobRequest(
	name, evalModel string,
	maxSamples int,
	sources []GenerationSource,
) *DataGenerationJobRequest {
	return &DataGenerationJobRequest{
		Inputs: DataGenerationInputs{
			Name:     name,
			Scenario: "evaluation",
			Options: DataGenerationOptions{
				Type:       "simple_qna",
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
		Inputs: EvaluatorGenerationInputs{
			Name:          name,
			EvaluatorName: name,
			Model:         evalModel,
			Sources:       sources,
		},
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
func SplitEvaluators(evaluators evalcore.EvaluatorList) (generated, builtin evalcore.EvaluatorList) {
	for _, e := range evaluators {
		// Name labels the criterion in results and is empty for a plain
		// `- evaluator: builtin.coherence`, so testing it classified every
		// built-in as generated. Evaluator is the reference IsBuiltin reads.
		if e.IsBuiltin() {
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
