// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"
)

func isResponsesEval(group *project.Eval) bool {
	return group != nil && group.Source != nil && group.Source.Type == project.SourceTypeResponses
}

func hasResponsesSchema(remote *eval_api.OpenAIEval) bool {
	return remote != nil && remote.DataSourceConfig["type"] == "azure_ai_source" &&
		remote.DataSourceConfig["scenario"] == "responses"
}

// Known schema types must match the selected source. Older projections that omit
// the type provide no mismatch evidence for unrelated custom-schema histories.
func responseSchemaMatches(group *project.Eval, remote *eval_api.OpenAIEval) bool {
	if isResponsesEval(group) {
		return hasResponsesSchema(remote)
	}
	if remote == nil {
		return true
	}
	typ, reported := remote.DataSourceConfig["type"]
	if typ == "azure_ai_source" && group != nil && group.Source != nil &&
		group.Source.Type == project.SourceTypeTraces {
		return remote.DataSourceConfig["scenario"] == "traces" || remote.DataSourceConfig["scenario"] == "traces_preview"
	}
	return !reported || typ == "custom"
}

func incompatibleResponsesSchema(id string, responses bool) error {
	expected := "custom schema"
	if responses {
		expected = "stored-responses schema (azure_ai_source, scenario responses)"
	}
	return exterrors.Validation(exterrors.CodeConflictingArguments,
		fmt.Sprintf("eval %q does not use the %s required by the selected source", id, expected),
		"Deploy the declaration without an explicit id to create a compatible eval, then run it by name. "+
			"The existing eval and its run history are retained.")
}

func (ec *evalContext) validateResponsesRun(
	ctx context.Context, evalID string, source *eval_api.EvalRunDataSource, declared bool,
) error {
	if source == nil {
		return messages.NoEvalToRun()
	}
	remote, err := ec.evalClient.GetOpenAIEval(ctx, evalID)
	if err != nil {
		return messages.ReadingEval(evalID, err)
	}
	responses := source.Type == eval_api.EvalRunDataSourceTypeResponses
	group := &project.Eval{}
	switch source.Type {
	case eval_api.EvalRunDataSourceTypeResponses:
		group.Source = &project.SourceDecl{Type: project.SourceTypeResponses}
	case eval_api.EvalRunDataSourceTypeTraces, eval_api.EvalRunDataSourceTypeTracePreview:
		group.Source = &project.SourceDecl{Type: project.SourceTypeTraces}
	}
	if !declared && !responses && !hasResponsesSchema(remote) {
		// A bare-ID rerun repeats a service-accepted source, not a new declaration.
		traceSchema := remote != nil && remote.DataSourceConfig["type"] == "azure_ai_source" &&
			(remote.DataSourceConfig["scenario"] == "traces" || remote.DataSourceConfig["scenario"] == "traces_preview")
		if !traceSchema || group.Source != nil {
			return nil
		}
	}
	if !responseSchemaMatches(group, remote) {
		return incompatibleResponsesSchema(evalID, responses)
	}
	if !responses {
		return nil
	}
	params := source.ItemGenerationParams
	if params == nil {
		return invalidResponsesSource("item_generation_params is required")
	}
	if params.Type != "response_retrieval" {
		return invalidResponsesSource("item_generation_params.type must be response_retrieval")
	}
	if params.Source == nil {
		return invalidResponsesSource("item_generation_params.source is required")
	}
	if params.Source.Type != eval_api.EvalRunDataContentTypeFileContent {
		return invalidResponsesSource("item_generation_params.source.type must be file_content; file_id is not supported")
	}
	if strings.TrimSpace(params.DataMapping["response_id"]) == "" {
		return invalidResponsesSource("item_generation_params.data_mapping.response_id is required")
	}
	column, mapped := itemColumn(params.DataMapping["response_id"])
	if !mapped {
		return invalidResponsesSource("item_generation_params.data_mapping.response_id must bind {{item.<field>}}")
	}
	if len(params.Source.Content) == 0 {
		return invalidResponsesSource("item_generation_params.source.content must contain at least one item")
	}
	for i, row := range params.Source.Content {
		item, ok := row["item"].(map[string]any)
		if !ok || item == nil {
			return invalidResponsesSource(fmt.Sprintf("item_generation_params.source.content[%d].item must be an object", i))
		}
		value, present := item[column]
		if !present {
			return invalidResponsesSource(fmt.Sprintf(
				"item_generation_params.source.content[%d].item has no mapped response ID", i))
		}
		id, ok := value.(string)
		if !ok {
			return invalidResponsesSource(fmt.Sprintf(
				"item_generation_params.source.content[%d].item mapped response ID must be a string", i))
		}
		if strings.TrimSpace(id) == "" {
			return invalidResponsesSource(fmt.Sprintf(
				"item_generation_params.source.content[%d].item mapped response ID must not be blank", i))
		}
	}
	return nil
}

func incompatibleSourceContract(id string) error {
	return exterrors.Validation(exterrors.CodeConflictingArguments,
		fmt.Sprintf("eval %q has a stored source contract incompatible with the declared source", id),
		"Remove the explicit id and deploy the declaration to create a compatible eval, then run it by name. "+
			"The existing eval and its run history are retained.")
}

func invalidResponsesSource(reason string) error {
	return exterrors.Validation(exterrors.CodeConflictingArguments,
		"the stored-responses run source is incompatible with response retrieval: "+reason,
		"Run the declared eval by name with source.responseIds. "+
			"Response retrieval supports file_content only, with a nested response_id mapping to {{item.<field>}} "+
			"and a non-blank string ID at that field in every item object.")
}
