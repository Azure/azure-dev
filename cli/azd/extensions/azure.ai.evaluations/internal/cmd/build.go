// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"
)

// dataMapping is the template binding the service uses to feed each evaluator.
// It pairs with the item schema below, which declares a single `query` field.
func dataMapping() map[string]string {
	return map[string]string{
		"query":            "{{item.query}}",
		"response":         "{{sample.output_items}}",
		"tool_calls":       "{{sample.tool_calls}}",
		"tool_definitions": "{{sample.tool_definitions}}",
	}
}

// agentItemSchema mirrors the shape the agent-target runner expects. It is a
// fixed schema, not inferred from the dataset.
func agentItemSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
	}
}

// buildEvalGroupRequest converts an eval group declaration into the create
// request. Evaluators become testing criteria; a per-evaluator threshold is
// carried in initialization_parameters alongside the judge model.
func buildEvalGroupRequest(group *project.EvalGroup) *eval_api.CreateOpenAIEvalRequest {
	metadata := map[string]string{}
	if group.Target != nil && group.Target.Name != "" {
		metadata["azd_agent"] = group.Target.Name
	}
	metadata["azd_eval_group"] = group.Name

	req := &eval_api.CreateOpenAIEvalRequest{
		Name:     group.Name,
		Metadata: metadata,
		DataSourceConfig: &eval_api.DataSourceConfig{
			Type:                "custom",
			IncludeSampleSchema: true,
			ItemSchema:          agentItemSchema(),
		},
	}

	evalModel := ""
	if group.Options != nil {
		evalModel = group.Options.EvalModel
	}

	for _, ref := range group.Evaluators {
		criterion := eval_api.TestingCriterion{
			Type: "azure_ai_evaluator",
			// Name drops the builtin prefix; EvaluatorName keeps it.
			Name:          ref.APIName(),
			EvaluatorName: ref.Name,
			DataMapping:   dataMapping(),
		}

		params := map[string]any{}
		if evalModel != "" {
			params["model"] = evalModel
			params["deployment_name"] = evalModel
		}
		if ref.Threshold != nil {
			params["threshold"] = *ref.Threshold
		}
		if len(params) > 0 {
			criterion.InitializationParameters = params
		}

		req.TestingCriteria = append(req.TestingCriteria, criterion)
	}

	return req
}
