// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package gen_api

import (
	"encoding/json"
	"strings"
)

// This is the data-generation half of the evaluation service's API. The
// evaluator half stays with `azure.ai.evaluations`, because only that extension
// generates evaluators. azd extensions share no code, so the shapes both need
// are spelled out in each rather than imported.

// DataGenerationJobRequest is the request body for CreateDataGenerationJob.
type DataGenerationJobRequest struct {
	Inputs DataGenerationInputs `json:"inputs"`
}

// DataGenerationInputs holds the inputs for a data generation job.
type DataGenerationInputs struct {
	Name     string                `json:"name"`
	Scenario string                `json:"scenario"`
	Options  DataGenerationOptions `json:"options"`
	Sources  []GenerationSource    `json:"sources"`
}

// DataGenerationOptions holds configuration for data generation.
type DataGenerationOptions struct {
	Type         string       `json:"type"`
	MaxSamples   int          `json:"max_samples"`
	ModelOptions ModelOptions `json:"model_options"`
}

// ModelOptions holds the model selection for generation.
type ModelOptions struct {
	Model string `json:"model"`
}

// GenerationSource describes a source used for dataset generation.
type GenerationSource struct {
	Type         string `json:"type"`
	Prompt       string `json:"prompt,omitempty"`
	AgentName    string `json:"agent_name,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`
	StartTime    int64  `json:"start_time,omitempty"`
}

// Agent is the part of a catalog agent that describes what it does.
//
// An agent is returned with its versions inlined rather than as a list, and
// only `latest` is populated on a plain read.
type Agent struct {
	Name     string `json:"name"`
	Versions struct {
		Latest *AgentVersion `json:"latest"`
	} `json:"versions"`
}

// AgentVersion is one published revision of an agent.
type AgentVersion struct {
	Version    string `json:"version"`
	Definition struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
	} `json:"definition"`
}

// Instructions returns the newest version's system prompt, or "" when the agent
// has no published version.
func (a *Agent) Instructions() string {
	if a == nil || a.Versions.Latest == nil {
		return ""
	}
	return strings.TrimSpace(a.Versions.Latest.Definition.Instructions)
}

// GenerationJob is the response for data generation job operations.
type GenerationJob struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *JobError       `json:"error,omitempty"`
}

// JobError captures error details from a failed generation job.
type JobError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// GenerationJobList is the listing envelope the job routes answer with. It is
// `data`, not the `value` the dataset routes use.
type GenerationJobList struct {
	Data []GenerationJob `json:"data"`
}

// ResolvedNameVersion extracts the name and version from the generation job
// result. An empty name means there is no result to read; an empty version
// means the service left it to be resolved as `latest`.
func (j *GenerationJob) ResolvedNameVersion() (string, string) {
	name := j.resultStringField("name")
	if name == "" {
		return "", ""
	}
	version := j.resultStringField("version")
	if version == "" {
		version = "latest"
	}
	return name, version
}

// resultStringField reads a string field out of the raw Result JSON, trying a
// top-level key before the nested outputs[0] shape the service also returns.
func (j *GenerationJob) resultStringField(key string) string {
	if len(j.Result) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(j.Result, &m); err != nil {
		return ""
	}

	if raw, ok := m[key]; ok {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			return s
		}
	}

	if rawOutputs, ok := m["outputs"]; ok {
		var outputs []map[string]json.RawMessage
		if err := json.Unmarshal(rawOutputs, &outputs); err == nil && len(outputs) > 0 {
			if raw, ok := outputs[0][key]; ok {
				var s string
				if err := json.Unmarshal(raw, &s); err == nil {
					return s
				}
			}
		}
	}
	return ""
}
