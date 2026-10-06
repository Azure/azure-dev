// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"maps"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"
)

func (ec *evalContext) selectedEvaluatorContract(
	ctx context.Context, name, version string,
) (*eval_api.EvaluatorSummary, error) {
	body, err := ec.evalClient.GetEvaluatorRaw(ctx, name, version, ProjectEndpointAPIVersion)
	if err != nil {
		return nil, messages.ReadingEvaluator(name, err)
	}
	schema, err := evaluatorContract(body)
	if err != nil {
		return nil, messages.EvaluatorProblem(name, err)
	}
	if version != "" && schema.Version != "" && schema.Version != version {
		return nil, fmt.Errorf("evaluator %q returned version %q instead of selected version %q",
			name, schema.Version, version)
	}
	return schema, nil
}

// readableContract reports whether a catalog entry carries the definition a
// row is validated against. A listing entry without one is treated as missing,
// so it is point-read and fails closed when the service has none either.
func readableContract(schema *eval_api.EvaluatorSummary) bool {
	return schema != nil && schema.Definition != nil
}

func (ec *evalContext) localEvaluatorSchemas(
	ctx context.Context, group *project.Eval, pending map[string]bool,
) (map[string]*eval_api.EvaluatorSummary, error) {
	index, err := ec.readEvaluatorSchemas(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading evaluator contracts for local eval %q: %w", group.Name, err)
	}
	index = maps.Clone(index)
	if index == nil {
		index = map[string]*eval_api.EvaluatorSummary{}
	}
	for _, ref := range group.Evaluators {
		key := evaluatorSchemaKey(ref.Evaluator, ref.Version)
		if ref.Version == "" && (readableContract(index[key]) || pending[ref.Evaluator]) {
			continue
		}
		schema, err := ec.selectedEvaluatorContract(ctx, ref.Evaluator, ref.Version)
		if err != nil {
			return nil, err
		}
		index[key] = schema
	}
	return index, nil
}

func (r *evalReconciler) preparedLocalRequest(
	ctx context.Context, group *project.Eval, path string, prepared preparedEval,
) (*eval_api.CreateOpenAIEvalRequest, error) {
	schemas := maps.Clone(prepared.schemas)
	for _, name := range prepared.localEvaluators {
		version := r.evaluatorVersions[name]
		if version == "" {
			return nil, fmt.Errorf("evaluator %q has no reconciled version to read", name)
		}
		schema, err := r.ec.selectedEvaluatorContract(ctx, name, version)
		if err != nil {
			return nil, err
		}
		schemas[name] = schema
	}
	input, err := openLocalInput(ctx, group, path)
	if err != nil {
		return nil, err
	}
	defer input.file.Close()
	req, err := buildLocalEvalRequest(group, input.columns, schemas)
	if err != nil {
		return nil, err
	}
	validate, err := localRowValidator(group, req.TestingCriteria, req.DataSourceConfig.ItemSchema)
	if err != nil {
		return nil, err
	}
	_, err = input.collect(ctx, -1, validate)
	return req, err
}
