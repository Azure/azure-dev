// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

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

// Only transitions into or out of the response scenario require this migration.
// Unrelated custom schemas and their fingerprint histories remain untouched.
func responseSchemaMatches(group *project.Eval, remote *eval_api.OpenAIEval) bool {
	return isResponsesEval(group) == hasResponsesSchema(remote)
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
	ctx context.Context, evalID string, source *eval_api.EvalRunDataSource,
) error {
	if source == nil || source.Type != eval_api.EvalRunDataSourceTypeResponses {
		return nil
	}
	remote, err := ec.evalClient.GetOpenAIEval(ctx, evalID)
	if err != nil {
		return messages.ReadingEval(evalID, err)
	}
	if !hasResponsesSchema(remote) {
		return incompatibleResponsesSchema(evalID, true)
	}
	params := source.ItemGenerationParams
	valid := params != nil && params.Type == "response_retrieval" &&
		params.DataMapping["response_id"] != "" && params.Source != nil
	if valid {
		switch params.Source.Type {
		case eval_api.EvalRunDataContentTypeFileContent:
			valid = len(params.Source.Content) > 0
			for _, row := range params.Source.Content {
				item, ok := row["item"].(map[string]any)
				if !ok || item == nil {
					valid = false
				}
			}
		case eval_api.EvalRunDataContentTypeFileID:
			valid = params.Source.ID != ""
		default:
			valid = false
		}
	}
	if !valid {
		return exterrors.Validation(exterrors.CodeConflictingArguments,
			"the stored-responses run source is incompatible with response retrieval",
			"Run the declared eval by name with source.response_ids. "+
				"Reruns by eval ID require a nested response_id mapping and source rows wrapped in item objects.")
	}
	return nil
}
