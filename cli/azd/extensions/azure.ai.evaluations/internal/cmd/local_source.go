// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	_, _, err := r.ec.localEvalInput(ctx, &group, path, -1, "")
	return err
}

func (ec *evalContext) localEvalInput(
	ctx context.Context, group *project.Eval, path string, limit int, evalID string,
) ([]map[string]any, *eval_api.CreateOpenAIEvalRequest, error) {
	input, err := openLocalInput(ctx, group, path)
	if err != nil {
		return nil, nil, err
	}
	defer input.file.Close()
	schemas, err := ec.localEvaluatorSchemas(ctx, group, nil)
	if err != nil {
		return nil, nil, err
	}
	req, err := buildLocalEvalRequest(group, input.columns, input.availableColumns, schemas)
	if err != nil {
		return nil, nil, messages.InEval(group.Name, err)
	}
	validate, err := localRowValidator(group, req.TestingCriteria, req.DataSourceConfig.ItemSchema)
	if err != nil {
		return nil, nil, err
	}
	rows, err := input.collect(ctx, limit, validate)
	if err != nil {
		return nil, nil, err
	}
	if evalID != "" {
		stored, err := ec.localRunValidator(ctx, evalID, group)
		if err != nil {
			return nil, nil, err
		}
		if _, err := input.collect(ctx, -1, stored); err != nil {
			return nil, nil, err
		}
	}
	return rows, req, nil
}

type localInput struct {
	file             *os.File
	path             string
	columns          map[string]bool
	availableColumns map[string]bool
	digest           []byte
}

func validateLocalFile(
	ctx context.Context, group *project.Eval, path string, schemas map[string]*eval_api.EvaluatorSummary,
) (*eval_api.CreateOpenAIEvalRequest, map[string]bool, error) {
	input, err := openLocalInput(ctx, group, path)
	if err != nil {
		return nil, nil, err
	}
	defer input.file.Close()
	req, err := buildLocalEvalRequest(group, input.columns, input.availableColumns, schemas)
	if err != nil {
		return nil, nil, err
	}
	validate, err := localRowValidator(group, req.TestingCriteria, req.DataSourceConfig.ItemSchema)
	if err != nil {
		return nil, nil, err
	}
	_, err = input.collect(ctx, -1, validate)
	return req, input.columns, err
}

func openLocalInput(
	ctx context.Context, group *project.Eval, path string,
) (*localInput, error) {
	if err := runnableEval(group); err != nil {
		return nil, err
	}
	// Reject known special files before Open can block; verify the opened handle too.
	info, err := os.Stat(path)
	if err != nil {
		return nil, messages.InEval(group.Name, messages.ReadingPath(path, err))
	}
	if !info.Mode().IsRegular() {
		return nil, messages.InEval(group.Name, errors.New("source.file must be a regular JSONL file"))
	}
	// #nosec G304 -- source.file explicitly selects this local input.
	file, err := os.Open(path)
	if err != nil {
		return nil, messages.ReadingPath(path, err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = file.Close()
		}
	}()
	info, err = file.Stat()
	if err != nil {
		return nil, messages.ReadingPath(path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, messages.InEval(group.Name, errors.New("source.file must be a regular JSONL file"))
	}
	digest := sha256.New()
	availableColumns := map[string]bool{}
	columns, err := inspectJSONLReader(ctx, path, io.TeeReader(file, digest), func(row map[string]any, _ int) error {
		for field := range row {
			availableColumns[field] = true
		}
		return nil
	})
	if err != nil {
		return nil, messages.InEval(group.Name, err)
	}
	if group.Target != nil && !columns["query"] {
		return nil, fmt.Errorf("eval %q: every local row must provide query for the target", group.Name)
	}
	keep = true
	return &localInput{
		file: file, path: path, columns: columns, availableColumns: availableColumns, digest: digest.Sum(nil),
	}, nil
}

// collect validates every row while retaining only the requested prefix. A negative
// limit is validation-only; zero retains all rows for an explicitly uncapped run.
func (input *localInput) collect(
	ctx context.Context, limit int, validators ...func(map[string]any, int) error,
) ([]map[string]any, error) {
	if _, err := input.file.Seek(0, io.SeekStart); err != nil {
		return nil, messages.ReadingPath(input.path, err)
	}
	digest := sha256.New()
	var rows []map[string]any
	_, err := inspectJSONLReader(ctx, input.path, io.TeeReader(input.file, digest), func(row map[string]any, i int) error {
		for _, validate := range validators {
			if err := validate(row, i); err != nil {
				return err
			}
		}
		if limit == 0 || (limit > 0 && len(rows) < limit) {
			rows = append(rows, row)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(input.digest, digest.Sum(nil)) {
		return nil, fmt.Errorf("local source changed during validation; retry with an unchanged file")
	}
	return rows, nil
}

func buildLocalEvalRequest(
	group *project.Eval, columns, availableColumns map[string]bool,
	schemas map[string]*eval_api.EvaluatorSummary,
) (*eval_api.CreateOpenAIEvalRequest, error) {
	req, err := buildEvalRequestWithAvailableColumns(group, schemas, columns, availableColumns)
	if err != nil {
		return nil, messages.InEval(group.Name, err)
	}
	return req, nil
}

func localItemSchema(
	group *project.Eval, criteria []eval_api.TestingCriterion, schemas map[string]*eval_api.EvaluatorSummary,
) map[string]any {
	properties := map[string]any{}
	required := map[string]bool{}
	add := func(column string, constraint any) {
		if previous, exists := properties[column]; exists {
			// One column can feed several evaluators; every published constraint applies.
			properties[column] = map[string]any{"allOf": []any{previous, constraint}}
		} else {
			properties[column] = constraint
		}
	}
	for i, criterion := range criteria {
		selected := schemas[evaluatorSchemaKey(criterion.EvaluatorName, criterion.EvaluatorVersion)]
		if selected == nil {
			selected = schemas[criterion.EvaluatorName]
		}
		published := selected.DataSchema()
		for _, field := range slices.Sorted(maps.Keys(criterion.DataMapping)) {
			binding := criterion.DataMapping[field]
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
			if published != nil && slices.Contains(published.Required, field) {
				required[column] = true
			}
			if i < len(group.Evaluators) {
				if _, explicit := group.Evaluators[i].DataMapping[field]; explicit {
					required[column] = true
				}
			}
		}
	}
	if group.Target != nil {
		add("query", map[string]any{"type": "string"})
		required["query"] = true
	}
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = slices.Sorted(maps.Keys(required))
	}
	return schema
}

func (ec *evalContext) localRunValidator(
	ctx context.Context, evalID string, group *project.Eval,
) (func(map[string]any, int) error, error) {
	definition, err := ec.evalClient.GetOpenAIEval(ctx, evalID)
	if err != nil {
		return nil, messages.ReadingEval(evalID, err)
	}
	if definition == nil {
		return nil, messages.EvalNotFound(evalID)
	}
	schema, ok := definition.DataSourceConfig["item_schema"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("eval %q: the registered eval has no readable item_schema to validate local rows", group.Name)
	}
	return localRowValidator(group, definition.TestingCriteria, schema)
}

type localSchemaLoader struct{}

func (localSchemaLoader) Load(string) (any, error) {
	return nil, errors.New("external schema references are not supported for local-source validation")
}

func localRowValidator(
	group *project.Eval, criteria []eval_api.TestingCriterion, itemSchema map[string]any,
) (func(map[string]any, int) error, error) {
	const resource = "urn:azd:local-source-schema"
	// Validate the JSON shape sent on the wire, including Go-built slices and numbers.
	raw, err := json.Marshal(itemSchema)
	if err != nil {
		return nil, messages.InEval(group.Name, err)
	}
	var document any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, messages.InEval(group.Name, err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(localSchemaLoader{})
	if err := compiler.AddResource(resource, document); err != nil {
		return nil, messages.InEval(group.Name, err)
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		// Compiler errors can quote credential-bearing external references.
		return nil, fmt.Errorf("eval %q: item_schema is invalid or contains unsupported external references", group.Name)
	}
	request := &eval_api.CreateOpenAIEvalRequest{TestingCriteria: criteria}
	return func(row map[string]any, i int) error {
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
		columns, malformed := map[string]bool{}, map[string]bool{}
		for column, value := range row {
			columns[column] = true
			malformed[column] = malformedTextValue(value)
		}
		if err := validateDatasetInteractions(group, request, columns, malformed); err != nil {
			return fmt.Errorf("eval %q: local row %d: %w", group.Name, i+1, err)
		}
		return nil
	}, nil
}
