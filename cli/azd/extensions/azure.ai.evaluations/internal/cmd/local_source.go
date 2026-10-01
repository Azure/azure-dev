// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// PreflightLocalEval checks local inputs before a deploy can publish any artifact.
func (r *evalReconciler) PreflightLocalEval(ctx context.Context, group project.Eval, path string) error {
	if !group.IsLocalSource() {
		return nil
	}
	_, _, err := r.ec.localEvalInput(ctx, &group, path)
	return err
}

func (ec *evalContext) localEvalInput(
	ctx context.Context, group *project.Eval, path string,
) ([]map[string]any, *eval_api.CreateOpenAIEvalRequest, error) {
	if err := runnableEval(group); err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, messages.InEval(group.Name, messages.ReadingPath(path, err))
	}
	if !info.Mode().IsRegular() {
		return nil, nil, messages.InEval(group.Name, errors.New("source.file must be a regular JSONL file"))
	}
	var rows []map[string]any
	columns, err := inspectJSONL(ctx, path, func(row map[string]any, _ int) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return nil, nil, messages.InEval(group.Name, err)
	}
	if group.Target != nil && !columns["query"] {
		return nil, nil, fmt.Errorf("eval %q: every local row must provide query for the target", group.Name)
	}
	schemas, err := ec.readEvaluatorSchemas(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("reading evaluator contracts for local eval %q: %w", group.Name, err)
	}
	req, err := buildEvalRequest(group, schemas, columns)
	if err != nil {
		return nil, nil, messages.InEval(group.Name, err)
	}
	req.DataSourceConfig.ItemSchema = localItemSchema(group, req.TestingCriteria, schemas)
	if err := validateLocalRows(group, rows, req.TestingCriteria, req.DataSourceConfig.ItemSchema); err != nil {
		return nil, nil, err
	}
	return rows, req, nil
}

func localItemSchema(
	group *project.Eval, criteria []eval_api.TestingCriterion, schemas map[string]*eval_api.EvaluatorSummary,
) map[string]any {
	properties := map[string]any{}
	add := func(column string, constraint any) {
		if previous, exists := properties[column]; exists {
			// One column can feed several evaluators; every published constraint applies.
			properties[column] = map[string]any{"allOf": []any{previous, constraint}}
		} else {
			properties[column] = constraint
		}
	}
	for _, criterion := range criteria {
		published := schemas[criterion.EvaluatorName].DataSchema()
		for field, binding := range criterion.DataMapping {
			column, mapped := itemColumn(binding)
			if !mapped {
				continue
			}
			// An absent property contract is unknown, not a declaration of string.
			var constraint any = map[string]any{}
			if published != nil {
				if property, exists := published.Properties[field]; exists {
					constraint = property
				}
			}
			add(column, constraint)
		}
	}
	if group.Target != nil {
		add("query", map[string]any{"type": "string"})
	}
	schema := map[string]any{"type": "object", "properties": properties}
	if len(properties) > 0 {
		schema["required"] = slices.Sorted(maps.Keys(properties))
	}
	return schema
}

func (ec *evalContext) validateLocalRunDefinition(
	ctx context.Context, evalID string, group *project.Eval, rows []map[string]any,
) error {
	definition, err := ec.evalClient.GetOpenAIEval(ctx, evalID)
	if err != nil {
		return messages.ReadingEval(evalID, err)
	}
	if definition == nil {
		return messages.EvalNotFound(evalID)
	}
	schema, ok := definition.DataSourceConfig["item_schema"].(map[string]any)
	if !ok {
		return fmt.Errorf("eval %q: the registered eval has no readable item_schema to validate local rows", group.Name)
	}
	return validateLocalRows(group, rows, definition.TestingCriteria, schema)
}

type localSchemaLoader struct{}

func (localSchemaLoader) Load(string) (any, error) {
	return nil, errors.New("external schema references are not supported for local-source validation")
}

func validateLocalRows(
	group *project.Eval, rows []map[string]any, criteria []eval_api.TestingCriterion, itemSchema map[string]any,
) error {
	const resource = "urn:azd:local-source-schema"
	// Validate the JSON shape sent on the wire, including Go-built slices and numbers.
	raw, err := json.Marshal(itemSchema)
	if err != nil {
		return messages.InEval(group.Name, err)
	}
	var document any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return messages.InEval(group.Name, err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(localSchemaLoader{})
	if err := compiler.AddResource(resource, document); err != nil {
		return messages.InEval(group.Name, err)
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		// Compiler errors can quote credential-bearing external references.
		return fmt.Errorf("eval %q: item_schema is invalid or contains unsupported external references", group.Name)
	}
	for i, row := range rows {
		if err := schema.Validate(row); err != nil {
			return fmt.Errorf("eval %q: local row %d does not match item_schema; check its columns and value types",
				group.Name, i+1)
		}
		if group.Target != nil {
			if _, ok := row["query"].(string); !ok {
				return fmt.Errorf("eval %q: local row %d must provide a string query for the target", group.Name, i+1)
			}
		}
		for _, criterion := range criteria {
			for field, binding := range criterion.DataMapping {
				if column, ok := itemColumn(binding); ok {
					if _, present := row[column]; !present {
						return fmt.Errorf("eval %q: local row %d lacks column %q mapped by evaluator %q field %q",
							group.Name, i+1, column, criterion.Name, field)
					}
				} else if strings.Contains(binding, "{{") {
					if group.Target == nil || !strings.HasPrefix(binding, "{{sample.") || !strings.HasSuffix(binding, "}}") {
						return fmt.Errorf("eval %q: cannot validate mapping for evaluator %q field %q against local rows",
							group.Name, criterion.Name, field)
					}
				}
			}
		}
	}
	return nil
}
