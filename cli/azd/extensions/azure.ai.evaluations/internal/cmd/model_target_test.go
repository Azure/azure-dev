// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A model answers as plain text and calls no tools. Binding an agent's richer
// output would leave the evaluator waiting on fields the run never produces,
// which the service reports as a missing input rather than a mapping mistake.
func TestSampleBindingsFor(t *testing.T) {
	agent := sampleBindingsFor(project.TargetTypeAgent)
	assert.Equal(t, "{{sample.output_items}}", agent["response"])
	assert.Contains(t, agent, "tool_calls")
	assert.Contains(t, agent, "tool_definitions")

	model := sampleBindingsFor(project.TargetTypeModel)
	assert.Equal(t, "{{sample.output_text}}", model["response"])
	assert.NotContains(t, model, "tool_calls", "a model calls no tools")
	assert.NotContains(t, model, "tool_definitions")

	assert.Nil(t, sampleBindingsFor(""), "with nothing invoked, nothing is bound")
}

// The criteria a group sends depend on what it targets.
func TestBuildEvalRequest_BindsByTargetKind(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{
		"builtin.coherence": {
			Name: "builtin.coherence",
			Definition: &eval_api.EvaluatorContract{
				DataSchema: &eval_api.JSONSchema{
					Required: []string{"query", "response"},
					Properties: map[string]any{
						"query":    map[string]any{"type": "string"},
						"response": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	for _, tc := range []struct {
		targetType string
		want       string
	}{
		{project.TargetTypeAgent, "{{sample.output_items}}"},
		{project.TargetTypeModel, "{{sample.output_text}}"},
	} {
		t.Run(tc.targetType, func(t *testing.T) {
			group := &project.Eval{
				Name:       "quality",
				Evaluators: []evalcore.EvaluatorRef{{Name: "builtin.coherence"}},
				Target:     &project.Target{Type: tc.targetType, Name: "thing"},
			}
			req, err := buildEvalRequest(group, schemas, map[string]bool{"query": true})
			require.NoError(t, err)
			require.Len(t, req.TestingCriteria, 1)
			assert.Equal(t, tc.want, req.TestingCriteria[0].DataMapping["response"])
			assert.Equal(t, "{{item.query}}", req.TestingCriteria[0].DataMapping["query"])
		})
	}
}

// The target the run posts has to match what the group's criteria expect.
func TestNewModelTargetDataSource(t *testing.T) {
	ds := eval_api.NewModelTargetDataSource("gpt-4.1-nano")
	require.NotNil(t, ds.Target)
	assert.Equal(t, "azure_ai_model", ds.Target.Type)
	assert.Equal(t, "gpt-4.1-nano", ds.Target.Model)

	raw, err := json.Marshal(ds)
	require.NoError(t, err)
	body := string(raw)
	assert.Contains(t, body, `"model":"gpt-4.1-nano"`)
	assert.NotContains(t, body, `"name"`, "a model target is addressed by deployment, not name")
	assert.NotContains(t, body, "tool_descriptions", "a model calls no tools")
}

func TestNewAgentTargetDataSource_StillSendsAgentFields(t *testing.T) {
	ds := eval_api.NewAgentTargetDataSource("support-agent", nil)
	require.NotNil(t, ds.Target)
	assert.Equal(t, "azure_ai_agent", ds.Target.Type)
	assert.Equal(t, "support-agent", ds.Target.Name)

	raw, err := json.Marshal(ds)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"model"`)
}
