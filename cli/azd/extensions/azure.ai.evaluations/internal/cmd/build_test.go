// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/require"
)

// schema builds an evaluator contract the way the service publishes one.
func schema(
	name string,
	dataRequired, dataProps, initRequired, initProps []string,
	levels ...string,
) *eval_api.EvaluatorSummary {
	toProps := func(names []string) map[string]any {
		if names == nil {
			return nil
		}
		out := map[string]any{}
		for _, n := range names {
			out[n] = map[string]any{"type": "string"}
		}
		return out
	}
	return &eval_api.EvaluatorSummary{
		Name:                      name,
		SupportedEvaluationLevels: levels,
		Definition: &eval_api.EvaluatorContract{
			DataSchema:     &eval_api.JSONSchema{Required: dataRequired, Properties: toProps(dataProps)},
			InitParameters: &eval_api.JSONSchema{Required: initRequired, Properties: toProps(initProps)},
		},
	}
}

func groupWith(evaluators []evalcore.EvaluatorRef, level string) *project.Eval {
	return &project.Eval{
		Name:            "g",
		Dataset:         "d",
		Target:          &project.Target{Type: "agent", Name: "my-agent"},
		Evaluators:      evaluators,
		EvaluationLevel: level,
	}
}

// withJudge declares the judge deployment where the service reads it from: an
// evaluator's initialization parameters, not a setting on the eval. It merges,
// so a parameter the reference already carries survives.
func withJudge(model string, refs ...evalcore.EvaluatorRef) []evalcore.EvaluatorRef {
	for i := range refs {
		if refs[i].InitializationParameters == nil {
			refs[i].InitializationParameters = map[string]any{}
		}
		refs[i].InitializationParameters["deployment_name"] = model
	}
	return refs
}

// An agent evaluator takes its response from the sample and its query from the
// dataset.
func TestBuildBindsAgentFieldsFromSample(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{
		"builtin.task_adherence": schema("builtin.task_adherence",
			nil, []string{"query", "response", "tool_definitions", "messages"},
			[]string{"deployment_name"}, []string{"deployment_name", "threshold", "evaluation_level"},
			"turn"),
	}
	group := groupWith(
		withJudge("gpt-4.1-nano", evalcore.EvaluatorRef{Evaluator: "builtin.task_adherence"}),
		"",
	)

	req, err := buildEvalRequest(group, schemas, map[string]bool{"query": true})
	require.NoError(t, err)
	require.Len(t, req.TestingCriteria, 1)

	mapping := req.TestingCriteria[0].DataMapping
	require.Equal(t, "{{item.query}}", mapping["query"])
	require.Equal(t, "{{sample.output_items}}", mapping["response"])
	require.Equal(t, "{{sample.tool_definitions}}", mapping["tool_definitions"])
	// `messages` is not a dataset column here, so it stays unbound.
	require.NotContains(t, mapping, "messages")
}

// A required field the dataset does not carry is reported before the request
// is sent, naming the field.
func TestBuildRejectsUnsatisfiableEvaluator(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{
		"builtin.ifeval": schema("builtin.ifeval",
			[]string{"response", "instruction_id_list", "instruction_kwargs"},
			[]string{"response", "instruction_id_list", "instruction_kwargs"},
			nil, nil, "turn"),
	}
	group := groupWith([]evalcore.EvaluatorRef{{Evaluator: "builtin.ifeval"}}, "")

	_, err := buildEvalRequest(group, schemas, map[string]bool{"query": true})
	require.Error(t, err)
	require.Contains(t, err.Error(), "instruction_id_list")
	require.Contains(t, err.Error(), "instruction_kwargs")
}

// The same evaluator succeeds once the dataset supplies the columns.
func TestBuildAcceptsEvaluatorWhenDatasetSupplies(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{
		"builtin.ifeval": schema("builtin.ifeval",
			[]string{"response", "instruction_id_list", "instruction_kwargs"},
			[]string{"response", "instruction_id_list", "instruction_kwargs"},
			nil, nil, "turn"),
	}
	group := groupWith([]evalcore.EvaluatorRef{{Evaluator: "builtin.ifeval"}}, "")

	req, err := buildEvalRequest(group, schemas, map[string]bool{
		"instruction_id_list": true,
		"instruction_kwargs":  true,
	})
	require.NoError(t, err)

	mapping := req.TestingCriteria[0].DataMapping
	// response is satisfied by the agent target.
	require.Equal(t, "{{sample.output_items}}", mapping["response"])
	require.Equal(t, "{{item.instruction_id_list}}", mapping["instruction_id_list"])

	// The item schema has to declare the columns the criteria reference.
	props := req.DataSourceConfig.ItemSchema["properties"].(map[string]any)
	require.Contains(t, props, "instruction_id_list")
	require.Contains(t, props, "instruction_kwargs")
}

// Initialization parameters are filtered to what the evaluator accepts.
// builtin.ifeval takes none, so nothing is sent even when a model is set.
func TestBuildOmitsUnacceptedInitParameters(t *testing.T) {
	threshold := 4.0
	schemas := map[string]*eval_api.EvaluatorSummary{
		"builtin.ifeval": schema("builtin.ifeval",
			nil, []string{"response"}, nil, nil, "turn"),
		"builtin.similarity": schema("builtin.similarity",
			nil, []string{"query", "response", "ground_truth"},
			[]string{"deployment_name"}, []string{"deployment_name", "threshold"}, "turn"),
	}
	group := groupWith(withJudge("gpt-4.1-nano",
		evalcore.EvaluatorRef{Evaluator: "builtin.ifeval",
			InitializationParameters: map[string]any{"threshold": threshold}},
		evalcore.EvaluatorRef{Evaluator: "builtin.similarity",
			InitializationParameters: map[string]any{"threshold": threshold}},
	), "")

	req, err := buildEvalRequest(group, schemas, map[string]bool{
		"query": true, "ground_truth": true,
	})
	require.NoError(t, err)

	// ifeval accepts no init parameters at all.
	require.Empty(t, req.TestingCriteria[0].InitializationParameters)

	// similarity accepts both, and never the `model` alias.
	params := req.TestingCriteria[1].InitializationParameters
	require.Equal(t, "gpt-4.1-nano", params["deployment_name"])
	require.InDelta(t, 4.0, params["threshold"], 0.0001)
	require.NotContains(t, params, "model")
}

// evaluation_level is an initialization parameter, not run metadata, and only
// on evaluators that declare it.
func TestBuildPassesEvaluationLevelAsInitParameter(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{
		"builtin.task_completion": schema("builtin.task_completion",
			nil, []string{"query", "response"},
			[]string{"deployment_name"}, []string{"deployment_name", "evaluation_level"},
			"conversation", "turn"),
		"builtin.similarity": schema("builtin.similarity",
			nil, []string{"query", "response"},
			[]string{"deployment_name"}, []string{"deployment_name", "threshold"}, "turn"),
	}
	group := groupWith(withJudge("m",
		evalcore.EvaluatorRef{Evaluator: "builtin.task_completion"},
		evalcore.EvaluatorRef{Evaluator: "builtin.similarity"},
	), "turn")

	req, err := buildEvalRequest(group, schemas, map[string]bool{"query": true})
	require.NoError(t, err)

	require.Equal(t, "turn", req.TestingCriteria[0].InitializationParameters["evaluation_level"])
	require.NotContains(t, req.TestingCriteria[1].InitializationParameters, "evaluation_level")
}

// An evaluator that does not support the requested level is rejected with the
// levels it does support.
func TestBuildRejectsUnsupportedLevel(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{
		"builtin.similarity": schema("builtin.similarity",
			nil, []string{"query", "response"},
			[]string{"deployment_name"}, []string{"deployment_name"}, "turn"),
	}
	group := groupWith(withJudge("m", evalcore.EvaluatorRef{Evaluator: "builtin.similarity"}),
		"conversation")

	_, err := buildEvalRequest(group, schemas, map[string]bool{"query": true})
	require.Error(t, err)
	require.Contains(t, err.Error(), "conversation")
	require.Contains(t, err.Error(), "turn")
}

// A required init parameter with no judge model configured is caught locally.
func TestBuildRequiresJudgeModelWhenEvaluatorDoes(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{
		"builtin.similarity": schema("builtin.similarity",
			nil, []string{"query", "response"},
			[]string{"deployment_name"}, []string{"deployment_name"}, "turn"),
	}
	group := groupWith([]evalcore.EvaluatorRef{{Evaluator: "builtin.similarity"}}, "")

	_, err := buildEvalRequest(group, schemas, map[string]bool{"query": true})
	require.Error(t, err)
	require.Contains(t, err.Error(), "deployment_name")
}

// An evaluator with no published contract keeps the historical agent-target
// shape, so custom evaluators still deploy.
func TestBuildFallsBackWithoutSchema(t *testing.T) {
	group := groupWith(withJudge("m", evalcore.EvaluatorRef{Evaluator: "my-custom-evaluator"}), "")

	req, err := buildEvalRequest(group, nil, nil)
	require.NoError(t, err)

	mapping := req.TestingCriteria[0].DataMapping
	require.Equal(t, "{{item.query}}", mapping["query"])
	require.Equal(t, "{{sample.output_items}}", mapping["response"])
	require.Equal(t, "{{sample.tool_calls}}", mapping["tool_calls"])
	require.Equal(t, "{{sample.tool_definitions}}", mapping["tool_definitions"])
	require.Equal(t, "m", req.TestingCriteria[0].InitializationParameters["deployment_name"])
}

// `messages` and `query`/`response` are mutually exclusive; the evaluation
// level picks which shape is bound. Sending both is rejected by the service.
func TestBuildResolvesConversationTurnExclusivity(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{
		"builtin.task_completion": schema("builtin.task_completion",
			nil, []string{"query", "response", "messages", "tool_definitions"},
			[]string{"deployment_name"}, []string{"deployment_name", "evaluation_level"},
			"conversation", "turn"),
	}
	columns := map[string]bool{"query": true, "messages": true, "response": true}

	// Turn level keeps query/response and drops messages.
	turn := groupWith(withJudge("m", evalcore.EvaluatorRef{Evaluator: "builtin.task_completion"}),
		"turn")
	req, err := buildEvalRequest(turn, schemas, columns)
	require.NoError(t, err)
	mapping := req.TestingCriteria[0].DataMapping
	require.Contains(t, mapping, "query")
	require.NotContains(t, mapping, "messages")
	turnProperties, ok := req.DataSourceConfig.ItemSchema["properties"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, map[string]any{"type": "string"}, turnProperties["query"])

	// Conversation level keeps messages and drops query/response.
	conv := groupWith(withJudge("m", evalcore.EvaluatorRef{Evaluator: "builtin.task_completion"}),
		"conversation")
	req, err = buildEvalRequest(conv, schemas, columns)
	require.NoError(t, err)
	mapping = req.TestingCriteria[0].DataMapping
	require.Contains(t, mapping, "messages")
	require.NotContains(t, mapping, "query")
	require.NotContains(t, mapping, "response")
	conversationProperties, ok := req.DataSourceConfig.ItemSchema["properties"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, map[string]any{
		"type": "array", "items": map[string]any{"type": "object"},
	}, conversationProperties["messages"])

	// An unset level behaves as turn, matching the service default.
	dflt := groupWith(withJudge("m", evalcore.EvaluatorRef{Evaluator: "builtin.task_completion"}), "")
	req, err = buildEvalRequest(dflt, schemas, columns)
	require.NoError(t, err)
	require.NotContains(t, req.TestingCriteria[0].DataMapping, "messages")
}

// A simulation is graded on the conversations the run creates, not on the seed
// rows it creates them from. The eval therefore describes a dataset of
// conversations even though the registered dataset holds seeds, and the seed
// columns must not reach the criterion: binding test_case_description is how a
// simulation eval ended up refused at deploy for a column the graded rows do
// not have.
func TestBuildSimulationGradesConversationsNotSeeds(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{
		"builtin.task_completion": schema("builtin.task_completion",
			nil, []string{"query", "response", "messages", "tool_definitions"},
			[]string{"deployment_name"}, []string{"deployment_name", "evaluation_level"},
			"conversation", "turn"),
	}

	group := groupWith(withJudge("m", evalcore.EvaluatorRef{Evaluator: "builtin.task_completion"}),
		"conversation")
	group.Simulation = &project.Simulation{Model: "gpt-4o", NumConversations: 2, MaxTurns: 4}

	// The seed columns are what the registered dataset actually holds. None of
	// them may be bound, and none of them may reach the item schema.
	req, err := buildEvalRequest(group, schemas, map[string]bool{
		"test_case_description":    true,
		"simulation_configuration": true,
	})
	require.NoError(t, err)

	mapping := req.TestingCriteria[0].DataMapping
	require.Equal(t, "{{item.messages}}", mapping["messages"],
		"the graded conversation arrives in item.messages")
	require.NotContains(t, mapping, "test_case_description")
	require.NotContains(t, mapping, "simulation_configuration")

	properties, ok := req.DataSourceConfig.ItemSchema["properties"].(map[string]any)
	require.True(t, ok, "item schema declares properties")
	require.Equal(t, map[string]any{
		"type": "array", "items": map[string]any{"type": "object"},
	}, properties["messages"], "the schema must accept arrays of conversation messages")
	require.NotContains(t, properties, "test_case_description")

	// The service holds the conversation itself, so there is no per-row target
	// invocation to produce a `sample` namespace to bind against.
	require.False(t, req.DataSourceConfig.IncludeSampleSchema)
	for _, value := range mapping {
		require.NotContains(t, value, "{{sample.",
			"a simulation has no sample namespace to bind")
	}
}

// Even when no criterion happens to bind it, the rows a simulation grades
// arrive in `messages`, and a schema that omits that column describes a
// different dataset than the one the run produces. This is the evaluator that
// scores a conversation without declaring a column for it.
func TestBuildSimulationDeclaresMessagesWithoutABinding(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{
		"builtin.violence": schema("builtin.violence",
			nil, []string{"query", "response"},
			nil, nil, "conversation", "turn"),
	}

	group := groupWith([]evalcore.EvaluatorRef{{Evaluator: "builtin.violence"}}, "conversation")
	group.Simulation = &project.Simulation{Model: "gpt-4o", NumConversations: 1, MaxTurns: 2}

	req, err := buildEvalRequest(group, schemas, map[string]bool{"test_case_description": true})
	require.NoError(t, err)
	require.NotContains(t, req.TestingCriteria[0].DataMapping, "messages",
		"no criterion bound the conversation column")

	properties, ok := req.DataSourceConfig.ItemSchema["properties"].(map[string]any)
	require.True(t, ok, "item schema declares properties")
	require.Equal(t, map[string]any{
		"type": "array", "items": map[string]any{"type": "object"},
	}, properties["messages"])
	require.NotContains(t, properties, "test_case_description")
	require.False(t, req.DataSourceConfig.IncludeSampleSchema)
}

// Evaluators disagree on what the judge model is called. Built-ins declare
// deployment_name; a custom rubric declares model, and rejects the eval with
// "requires model" if only deployment_name is sent. One declaration binds
// whichever the evaluator actually accepts.
func TestBuildBindsJudgeModelUnderTheDeclaredName(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{
		"builtin.similarity": schema("builtin.similarity",
			nil, []string{"query", "response"},
			[]string{"deployment_name"}, []string{"deployment_name"}, "turn"),
		"my-rubric": schema("my-rubric",
			nil, []string{"query", "response"},
			[]string{"model"}, []string{"model"}, "turn"),
	}
	group := groupWith(withJudge("gpt-4.1-nano",
		evalcore.EvaluatorRef{Evaluator: "builtin.similarity"},
		evalcore.EvaluatorRef{Evaluator: "my-rubric"},
	), "")

	req, err := buildEvalRequest(group, schemas, map[string]bool{"query": true})
	require.NoError(t, err)

	builtin := req.TestingCriteria[0].InitializationParameters
	require.Equal(t, "gpt-4.1-nano", builtin["deployment_name"])
	require.NotContains(t, builtin, "model")

	custom := req.TestingCriteria[1].InitializationParameters
	require.Equal(t, "gpt-4.1-nano", custom["model"])
	require.NotContains(t, custom, "deployment_name")
}

// Without an agent target the sample bindings are unavailable, so every field
// has to come from the dataset and the sample schema is not requested.
func TestBuildWithoutTargetSourcesEverythingFromDataset(t *testing.T) {
	schemas := map[string]*eval_api.EvaluatorSummary{
		"builtin.similarity": schema("builtin.similarity",
			[]string{"query", "response", "ground_truth"},
			[]string{"query", "response", "ground_truth"},
			nil, nil, "turn"),
	}
	group := groupWith([]evalcore.EvaluatorRef{{Evaluator: "builtin.similarity"}}, "")
	group.Target = nil

	req, err := buildEvalRequest(group, schemas, map[string]bool{
		"query": true, "response": true, "ground_truth": true,
	})
	require.NoError(t, err)
	require.False(t, req.DataSourceConfig.IncludeSampleSchema)
	require.Equal(t, "{{item.response}}", req.TestingCriteria[0].DataMapping["response"])
}
