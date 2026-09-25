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
		strings.TrimSpace(params.DataMapping["response_id"]) != "" && params.Source != nil
	if valid {
		switch params.Source.Type {
		case eval_api.EvalRunDataContentTypeFileContent:
			column, mapped := itemColumn(params.DataMapping["response_id"])
			valid = mapped && len(params.Source.Content) > 0
			for _, row := range params.Source.Content {
				item, ok := row["item"].(map[string]any)
				if !ok {
					valid = false
					continue
				}
				id, ok := item[column].(string)
				if !ok || strings.TrimSpace(id) == "" {
					valid = false
				}
			}
		case eval_api.EvalRunDataContentTypeFileID:
			valid = strings.TrimSpace(params.Source.ID) != ""
		default:
			valid = false
		}
	}
	if !valid {
		return exterrors.Validation(exterrors.CodeConflictingArguments,
			"the stored-responses run source is incompatible with response retrieval",
			"Run the declared eval by name with source.response_ids. "+
				"Inline reruns require a nested response_id mapping to {{item.<field>}} "+
				"and a non-empty string ID at that field in every item object.")
	}
	return nil
}
