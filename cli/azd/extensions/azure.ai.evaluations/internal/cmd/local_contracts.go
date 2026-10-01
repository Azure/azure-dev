// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"
)

// The name/version key and authored-pin precedence match the prepared-eval
// reconciliation contract; a selected version must never overwrite latest.
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

// localEvaluator reads the same authored bytes used for publication and preflight.
func localEvaluator(decl project.EvaluatorDecl, path string) (json.RawMessage, string, error) {
	if decl.Definition != nil {
		raw, err := json.Marshal(decl.Definition)
		if err != nil {
			return nil, "", err
		}
		body, err := normalizeRubricBody(decl.Name, raw)
		if err != nil {
			return nil, "", err
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
		return nil, "", err
	}
	digest, err := project.Fingerprint(path)
	if err != nil {
		return nil, "", err
	}
	return body, digest, nil
}

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

func (ec *evalContext) localEvaluatorSchemas(
	ctx context.Context, group *project.Eval,
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
		if ref.Version == "" {
			continue
		}
		key := evaluatorSchemaKey(ref.Evaluator, ref.Version)
		schema, err := ec.selectedEvaluatorContract(ctx, ref.Evaluator, ref.Version)
		if err != nil {
			return nil, err
		}
		index[key] = schema
	}
	return index, nil
}

type preparedLocalEval struct {
	group           project.Eval
	schemas         map[string]*eval_api.EvaluatorSummary
	localEvaluators []string
}

// ValidateLocalSources validates prospective authored contracts before publication.
// Service-added constraints that no authored or published contract describes are
// checked again after publication; preflight does not invent them.
func (r *evalReconciler) ValidateLocalSources(ctx context.Context, cfg *project.EvalConfig, baseDir string) error {
	prepared := map[string]preparedLocalEval{}
	for _, declared := range cfg.Evals {
		if !declared.IsLocalSource() {
			continue
		}
		group := cfg.WithCatalogEvaluatorPins(declared)
		rows, columns, err := readLocalRows(ctx, &group, group.LocalSourcePath(baseDir))
		if err != nil {
			return err
		}
		schemas, err := r.ec.localEvaluatorSchemas(ctx, &group)
		if err != nil {
			return err
		}
		var localEvaluators []string
		for _, ref := range group.Evaluators {
			decl, ok := cfg.EvaluatorDeclaration(ref.Evaluator)
			if !ok || !decl.CarriesItsRubric() || ref.Version != "" {
				continue
			}
			body, _, err := localEvaluator(*decl, project.ResolveSource(baseDir, decl.Source))
			if err != nil {
				return messages.EvaluatorProblem(decl.Name, err)
			}
			body, err = withCatalogMetadata(body, *decl)
			if err != nil {
				return messages.EvaluatorProblem(decl.Name, err)
			}
			prospective, err := evaluatorContract(body)
			if err != nil {
				return messages.EvaluatorProblem(decl.Name, err)
			}
			// Authored constraints take precedence over service-enriched fields.
			// Absent fields can use the currently published contract, as in the
			// prepared-eval preflight, but never the reverse.
			if published := schemas[decl.Name]; published != nil {
				if prospective.DataSchema() == nil {
					prospective.Definition.DataSchema = published.DataSchema()
				}
				if prospective.InitSchema() == nil {
					prospective.Definition.InitParameters = published.InitSchema()
				}
				if prospective.SupportedEvaluationLevels == nil {
					prospective.SupportedEvaluationLevels = slices.Clone(published.SupportedEvaluationLevels)
				}
			}
			schemas[decl.Name] = prospective
			localEvaluators = append(localEvaluators, decl.Name)
		}
		if _, err := buildLocalEvalRequest(&group, rows, columns, schemas); err != nil {
			return err
		}
		prepared[group.Name] = preparedLocalEval{group: group, schemas: schemas, localEvaluators: localEvaluators}
	}
	r.preparedLocal = prepared
	return nil
}

func (r *evalReconciler) preparedLocalRequest(
	ctx context.Context, group *project.Eval, path string, prepared preparedLocalEval,
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
	rows, columns, err := readLocalRows(ctx, group, path)
	if err != nil {
		return nil, err
	}
	return buildLocalEvalRequest(group, rows, columns, schemas)
}
