// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"
)

type preparedEval struct {
	declared        project.Eval
	group           project.Eval
	request         *eval_api.CreateOpenAIEvalRequest
	schemas         map[string]*eval_api.EvaluatorSummary
	columns         map[string]bool
	localEvaluators []string
}

// Validate prepares every eval before the first dependency is published.
// Unlike the best-effort catalog used for discovery, a failed reference read
// must stop reconciliation: it is not evidence that a reference is valid.
func (r *evalReconciler) Validate(ctx context.Context, cfg *project.EvalConfig, baseDir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	columns := map[string]map[string]bool{}
	for _, decl := range cfg.Datasets {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := project.ResolveSource(baseDir, decl.File)
		if path == "" {
			if _, err := r.datasetReference(ctx, decl); err != nil {
				return messages.DatasetProblem(decl.Name, err)
			}
			continue
		}
		available := map[string]any{}
		fields, err := inspectJSONL(ctx, path, func(row map[string]any, index int) error {
			for field := range row {
				available[field] = nil
			}
			for i := range cfg.Evals {
				group := &cfg.Evals[i]
				if group.Dataset != decl.Name || group.Simulation == nil {
					continue
				}
				if err := refuseUnusableSeedRow(group, row, index); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return messages.DatasetProblem(decl.Name, err)
		}
		for i := range cfg.Evals {
			group := &cfg.Evals[i]
			if group.Dataset == decl.Name {
				if err := validateDatasetTarget(group, available); err != nil {
					return messages.DatasetProblem(decl.Name, err)
				}
			}
		}
		columns[decl.Name] = fields
	}

	schemas := map[string]*eval_api.EvaluatorSummary{}
	for _, decl := range cfg.Evaluators {
		if err := ctx.Err(); err != nil {
			return err
		}
		var body json.RawMessage
		var err error
		if decl.CarriesItsRubric() {
			body, _, err = localEvaluator(decl, project.ResolveSource(baseDir, decl.Source))
		} else {
			body, err = r.ec.evalClient.GetEvaluatorRaw(ctx, decl.Name, decl.Version, ProjectEndpointAPIVersion)
		}
		if err != nil {
			return messages.EvaluatorProblem(decl.Name, err)
		}
		schema, err := evaluatorContract(body)
		if err != nil {
			return messages.EvaluatorProblem(decl.Name, err)
		}
		if decl.CarriesItsRubric() && schema.SupportedEvaluationLevels == nil {
			schema.SupportedEvaluationLevels = slices.Clone(decl.SupportedEvaluationLevels)
		}
		if decl.CarriesItsRubric() {
			// Authored rubrics omit the schemas Foundry adds on publication.
			// Reuse that contract when present, without replacing authored
			// fields or treating a failed read as a missing evaluator.
			remote, err := r.ec.evalClient.GetEvaluatorRaw(ctx, decl.Name, "", ProjectEndpointAPIVersion)
			if err != nil && !eval_api.IsNotFound(err) {
				return messages.CheckingEvaluatorExists(decl.Name, err)
			}
			if err == nil {
				published, err := evaluatorContract(remote)
				if err != nil {
					return messages.EvaluatorProblem(decl.Name, err)
				}
				if schema.Definition.DataSchema == nil {
					schema.Definition.DataSchema = published.DataSchema()
				}
				if schema.Definition.InitParameters == nil {
					schema.Definition.InitParameters = published.InitSchema()
				}
			}
		}
		schemas[evaluatorSchemaKey(decl.Name, decl.Version)] = schema
	}

	prepared := map[string]preparedEval{}
	for _, group := range cfg.Evals {
		if err := ctx.Err(); err != nil {
			return err
		}
		if group.ID != "" {
			if _, err := r.ec.evalClient.GetOpenAIEval(ctx, group.ID); err != nil {
				return messages.ReadingEval(group.ID, err)
			}
		}
		declared := group
		group = withCatalogEvaluatorPins(group, cfg)
		var localEvaluators []string
		for i := range group.Evaluators {
			ref := &group.Evaluators[i]
			if decl, ok := cfg.EvaluatorDeclaration(ref.Evaluator); ok {
				if ref.Version == "" {
					if decl.CarriesItsRubric() {
						localEvaluators = append(localEvaluators, decl.Name)
					}
				}
			}
			key := evaluatorSchemaKey(ref.Evaluator, ref.Version)
			if schemas[key] != nil {
				continue
			}
			body, err := r.ec.evalClient.GetEvaluatorRaw(ctx, ref.Evaluator, ref.Version, ProjectEndpointAPIVersion)
			if err != nil {
				return messages.EvaluatorNotLocalNorFound(ref.Evaluator, err)
			}
			schema, err := evaluatorContract(body)
			if err != nil {
				return messages.EvaluatorProblem(ref.Evaluator, err)
			}
			schemas[key] = schema
		}
		request, err := buildEvalRequest(&group, schemas, columns[group.Dataset])
		if err != nil {
			return messages.EvalProblem(group.Name, err)
		}
		prepared[group.Name] = preparedEval{
			declared: declared, group: group, request: request, schemas: schemas,
			columns: columns[group.Dataset], localEvaluators: localEvaluators,
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.prepared = prepared
	return nil
}

// withCatalogEvaluatorPins resolves only authored pins. A service-resolved
// latest version is not an edit and must never change an eval's identity.
func withCatalogEvaluatorPins(group project.Eval, cfg *project.EvalConfig) project.Eval {
	group.Evaluators = slices.Clone(group.Evaluators)
	for i := range group.Evaluators {
		ref := &group.Evaluators[i]
		if ref.Version == "" {
			if decl, ok := cfg.EvaluatorDeclaration(ref.Evaluator); ok {
				ref.Version = decl.Version
			}
		}
	}
	return group
}

func validateDatasetTarget(group *project.Eval, available map[string]any) error {
	if group.Simulation != nil || group.Target == nil {
		return nil
	}
	// These constructors describe the same template used by the run path.
	// Resolving the remote agent name is unnecessary for checking its inputs.
	var source *eval_api.EvalRunDataSource
	if group.Target.Type == project.TargetTypeModel {
		source = eval_api.NewModelTargetDataSource(group.Target.Name)
	} else {
		source = eval_api.NewAgentTargetDataSource(group.Target.Name, nil)
	}
	// The run allows sparse target inputs. Keep the union of available columns,
	// separately from the intersection used for required evaluator bindings.
	return refuseUnboundTemplate(group, source, []map[string]any{available})
}

func evaluatorSchemaKey(name, version string) string {
	if version == "" {
		return name
	}
	return name + "\x00" + version
}

func evaluatorContract(body json.RawMessage) (*eval_api.EvaluatorSummary, error) {
	var schema *eval_api.EvaluatorSummary
	if err := json.Unmarshal(body, &schema); err != nil {
		return nil, fmt.Errorf("reading evaluator contract: %w", err)
	}
	if schema == nil || schema.Definition == nil {
		return nil, fmt.Errorf("evaluator response has no definition")
	}
	return schema, nil
}

func (r *evalReconciler) datasetReference(ctx context.Context, decl project.DatasetDecl) (string, error) {
	version := decl.Version
	if version == "" {
		list, err := r.ec.datasetClient.ListDatasetVersions(ctx, decl.Name, ProjectEndpointAPIVersion)
		if err != nil {
			return "", messages.DatasetNotLocalNorFound(decl.Name, err)
		}
		if list == nil || len(list.Value) == 0 {
			return "", messages.DatasetNotLocalNorRegistered(decl.Name)
		}
		version = dataset_api.LatestVersion(list.Value)
	}
	if _, err := r.ec.datasetClient.GetDataset(ctx, decl.Name, version, ProjectEndpointAPIVersion); err != nil {
		if !dataset_api.IsNotFound(err) {
			return "", messages.DatasetNotLocalNorFound(decl.Name, err)
		}
		return "", messages.DatasetVersionNotFoundWithHint(decl.Name, version)
	}
	return version, nil
}

// localEvaluator reads the authored bytes only. Catalog metadata still joins
// the publish body after digest and drift decisions in EnsureEvaluator.
func localEvaluator(decl project.EvaluatorDecl, path string) (json.RawMessage, string, error) {
	if decl.Definition != nil {
		raw, err := json.Marshal(decl.Definition)
		if err != nil {
			return nil, "", messages.EvaluatorProblem(decl.Name, err)
		}
		body, err := normalizeRubricBody(decl.Name, raw)
		if err != nil {
			return nil, "", messages.EvaluatorProblem(decl.Name, err)
		}
		return body, project.FingerprintBytes(body), nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", messages.EvaluatorNotGeneratedYet(decl.Name, path)
		}
		return nil, "", messages.EvaluatorSource(path, err)
	}
	raw, err := project.ReadFileNoBOM(path)
	if err != nil {
		return nil, "", messages.EvaluatorSource(path, err)
	}
	body, err := normalizeRubricBody(decl.Name, raw)
	if err != nil {
		return nil, "", messages.EvaluatorProblem(decl.Name, err)
	}
	digest, err := project.Fingerprint(path)
	if err != nil {
		return nil, "", messages.EvaluatorSource(path, err)
	}
	return body, digest, nil
}
