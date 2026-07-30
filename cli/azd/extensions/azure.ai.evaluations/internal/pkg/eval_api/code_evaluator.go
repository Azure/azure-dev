// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"azureaieval/internal/pkg/evalcore"
)

// CodeDefinitionType is the discriminator the service uses to deserialize a
// code evaluator definition.
//
// The wire shape is snake_case with a lowercase discriminator, matching
// CodeBasedEvaluatorDefinition in the Foundry data-plane OpenAPI document
// (`type` enum ["code"], plus code_text, image_tag, init_parameters,
// data_schema and metrics). An earlier draft documented a camelCase body with
// `type: "CodeBased"`; that shape is not what the deployed service accepts.
const CodeDefinitionType = "code"

// evaluatorTypeCustom marks an evaluator as authored by the project rather
// than shipped by the platform.
const evaluatorTypeCustom = "custom"

// foundryFeaturesHeader opts a request in to preview behaviour. The code
// definition's properties are declared as preview, so the header is sent with
// every call that sets one.
const (
	foundryFeaturesHeader  = "Foundry-Features"
	foundryFeatureEvalsV1  = "Evaluations=V1Preview"
	defaultCodeMetricName  = "result"
	defaultCodeMetricType  = "continuous"
	defaultMetricDirection = "increase"
)

// DefaultCodeMetrics is used when the caller declares none.
//
// The service rejects a code definition carrying no metrics, and the documented
// evaluator output is a JSON object whose `result` field holds the score, so
// this describes exactly that. It is a default, not a constraint: any declared
// metrics replace it wholesale.
var DefaultCodeMetrics = json.RawMessage(fmt.Sprintf(
	`{%q:{"type":%q,"desirable_direction":%q,"is_primary":true}}`,
	defaultCodeMetricName, defaultCodeMetricType, defaultMetricDirection,
))

// CodeEvaluatorOptions carries the parts of an evaluator version that do not
// come from the Python source itself.
type CodeEvaluatorOptions struct {
	DisplayName    string
	Description    string
	Categories     []string
	ImageTag       string
	InitParameters json.RawMessage
	DataSchema     json.RawMessage
	Metrics        json.RawMessage
}

// codeDefinition is the wire body of a code evaluator definition.
//
// code_text carries the whole evaluator. The contract's other source property,
// blob_uri, is deliberately absent: the definition is consumed as an OpenAI
// python grader, whose contract (GraderPython) is a single `Source` string
// with no notion of a folder, archive, file list or entry point. A definition
// published with blob_uri alone registers cleanly and then fails the run with
// "Invalid grader source: top-level grade() function not found in source",
// because nothing reads the blob back into Source.
//
// image_tag is how a grader gets dependencies. Only one file is ever sent, so
// a helper module cannot travel with it and anything beyond the standard
// library has to already be in the image.
type codeDefinition struct {
	Type           string          `json:"type"`
	CodeText       string          `json:"code_text,omitempty"`
	ImageTag       string          `json:"image_tag,omitempty"`
	InitParameters json.RawMessage `json:"init_parameters,omitempty"`
	DataSchema     json.RawMessage `json:"data_schema,omitempty"`
	Metrics        json.RawMessage `json:"metrics,omitempty"`
}

// createEvaluatorVersionRequest is the POST body for a new evaluator version.
// The service assigns the version; it is not carried here.
type createEvaluatorVersionRequest struct {
	Name          string          `json:"name,omitempty"`
	DisplayName   string          `json:"display_name,omitempty"`
	Description   string          `json:"description,omitempty"`
	EvaluatorType string          `json:"evaluator_type,omitempty"`
	Categories    []string        `json:"categories,omitempty"`
	Definition    *codeDefinition `json:"definition"`
}

// CreateCodeEvaluatorVersion publishes a Python script as a new version of a
// code evaluator.
//
// The source is sent inline. There is no upload step and no storage to
// reserve: the executor is handed a string of source, so a blob it would never
// read adds a round trip, a SAS write, and a failure mode in exchange for
// nothing that reaches the grader.
func (c *EvalClient) CreateCodeEvaluatorVersion(
	ctx context.Context,
	script *evalcore.CodeEvaluatorScript,
	opts CodeEvaluatorOptions,
	apiVersion string,
) (*EvaluatorVersion, error) {
	if script == nil {
		return nil, fmt.Errorf("no evaluator script to publish")
	}

	definition := &codeDefinition{
		Type:           CodeDefinitionType,
		CodeText:       script.Source,
		ImageTag:       opts.ImageTag,
		InitParameters: opts.InitParameters,
		DataSchema:     opts.DataSchema,
		Metrics:        opts.Metrics,
	}
	if len(definition.Metrics) == 0 {
		definition.Metrics = DefaultCodeMetrics
	}

	body := &createEvaluatorVersionRequest{
		Name:          script.Name,
		DisplayName:   opts.DisplayName,
		Description:   opts.Description,
		EvaluatorType: evaluatorTypeCustom,
		Categories:    opts.Categories,
		Definition:    definition,
	}

	path := pathEvaluators + "/" + url.PathEscape(script.Name) + "/versions"
	respBody, err := c.doRequestWithHeaders(
		ctx, http.MethodPost, path, nil, body, apiVersion, previewHeaders(),
	)
	if err != nil {
		return nil, err
	}

	var created EvaluatorVersion
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &created); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
	}
	if created.Name == "" {
		created.Name = script.Name
	}
	return &created, nil
}

// LatestEvaluatorVersionNumber reports the newest registered version as an
// integer, or 0 when the evaluator is unknown or its versions are not numeric.
func (c *EvalClient) LatestEvaluatorVersionNumber(
	ctx context.Context,
	name string,
	apiVersion string,
) int {
	list, err := c.ListEvaluatorVersions(ctx, name, apiVersion)
	if err != nil || list == nil || len(list.Value) == 0 {
		return 0
	}
	number, err := strconv.Atoi(pickLatestVersion(list.Value))
	if err != nil {
		return 0
	}
	return number
}

// previewHeaders returns the opt-in header for the preview properties the code
// definition relies on.
func previewHeaders() map[string]string {
	return map[string]string{foundryFeaturesHeader: foundryFeatureEvalsV1}
}
