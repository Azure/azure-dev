// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const recordedRouteID = "cu_cli_route_target_v1"

var recordedTemplateCases = []struct {
	name, kind, prefix, analyzer string
	interactions                 int
	wait                         time.Duration
}{
	{"extraction", "extraction", "template_analyzer", "cu_cli_tmpl_analyzer_v1", 5, 2 * time.Second},
	{"classifier-routing", "classification", "classifier_route", "cu_cli_tmpl_classifier_v1", 3, 0},
}

func recordedTemplateGolden(t *testing.T, kind, analyzer string) json.RawMessage {
	t.Helper()
	body, err := os.ReadFile("../schema/testdata/authoring-python.json")
	require.NoError(t, err)
	var oracle struct {
		Source    string                     `json:"source"`
		Templates map[string]json.RawMessage `json:"templates"`
	}
	require.NoError(t, json.Unmarshal(body, &oracle))
	require.Contains(t, oracle.Source, paritySourceCommit)
	var definition map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(oracle.Templates[kind], &definition))
	definition["analyzerId"], err = json.Marshal(analyzer)
	require.NoError(t, err)
	if kind == "extraction" {
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(definition["fieldSchema"], &fields))
		fields["name"], err = json.Marshal(analyzer + "_schema")
		require.NoError(t, err)
		definition["fieldSchema"], err = json.Marshal(fields)
		require.NoError(t, err)
	}
	result, err := json.Marshal(definition)
	require.NoError(t, err)
	return result
}

func recordedClassifierRouting(t *testing.T, definition json.RawMessage) json.RawMessage {
	t.Helper()
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(definition, &document))
	var config map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(document["config"], &config))
	var categories map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(config["contentCategories"], &categories))
	require.Len(t, categories, 4)
	categories["invoice"]["analyzerId"] = json.RawMessage(`"prebuilt-invoice"`)
	categories["purchase_order"]["analyzerId"] = json.RawMessage(`"` + recordedRouteID + `"`)
	body, err := json.Marshal(categories)
	require.NoError(t, err)
	config["contentCategories"] = body
	document["config"], err = json.Marshal(config)
	require.NoError(t, err)
	result, err := json.Marshal(document)
	require.NoError(t, err)
	return result
}

func executeRecordedOfflineSchema(t *testing.T, args []string, expected json.RawMessage, format string) {
	t.Helper()
	fixture := newDefaultsFixture(t)
	t.Setenv("AZD_SERVER", "")
	t.Setenv("AZD_ACCESS_TOKEN", "")
	t.Setenv("CU_ENDPOINT", "invalid-not-read-by-offline-command")
	root := fixture.root()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	args = append(args, "--api-version", "2025-11-01", "--no-prompt", "--debug", "-o", format)
	require.NoError(t, ValidateInvocation(root, args))
	root.SetArgs(args)
	require.NoError(t, root.ExecuteContext(t.Context()))
	require.Zero(t, fixture.opens, "offline authoring/validation must not open a service/host session")
	assert.Zero(t, fixture.closes)
	assert.Empty(t, fixture.client.calls)
	assert.Empty(t, stderr.String())
	if format == "none" {
		assert.Empty(t, stdout.String())
	} else {
		want, err := recordedJSON(expected)
		require.NoError(t, err)
		actual, err := recordedJSON(stdout.Bytes())
		require.NoError(t, err)
		assert.Equal(t, want, actual, "compare the complete source-pinned offline result")
	}
}

func TestRecordedTemplateClassifierLifecycle(t *testing.T) {
	for _, test := range recordedTemplateCases {
		t.Run(test.name, func(t *testing.T) {
			for _, format := range []string{"default", "json", "none"} {
				t.Run(format, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						directory := testutil.TempDir(t)
						path := filepath.Join(directory, "template.json")
						definition := recordedTemplateGolden(t, test.kind, test.analyzer)
						executeRecordedOfflineSchema(t, []string{"analyzer", "schema", "create",
							"--from-template", "--type", test.kind, "--name", test.analyzer,
							"--output-file", path}, definition, format)
						// #nosec G304 -- Reads only this test's private temporary schema.
						saved, err := os.ReadFile(path)
						require.NoError(t, err)
						want, err := recordedJSON(definition)
						require.NoError(t, err)
						actual, err := recordedJSON(saved)
						require.NoError(t, err)
						require.Equal(t, want, actual, "saved template must match every independent Python oracle field")
						if test.kind == "classification" {
							definition = recordedClassifierRouting(t, definition)
							require.NoError(t, os.WriteFile(path, definition, 0o600))
						}
						executeRecordedOfflineSchema(t, []string{"analyzer", "validate", path, "--strict", "--spec"},
							json.RawMessage(`{
								"ok":true,"errors":[],"warnings":[],"strict":true,"apiVersion":"2025-11-01"
							}`), format)

						type resource struct {
							analyzer, prefix, path string
							definition             json.RawMessage
							interactions           int
							wait                   time.Duration
						}
						resources := []resource{{test.analyzer, test.prefix, path, definition, test.interactions, test.wait}}
						if test.kind == "classification" {
							routePath := filepath.Join(directory, "route.json")
							var route map[string]json.RawMessage
							base := recordedLifecycleDefinition(t, recordedFixture{}, true)
							require.NoError(t, json.Unmarshal(base, &route))
							route["analyzerId"] = json.RawMessage(`"` + recordedRouteID + `"`)
							route["apiVersion"] = json.RawMessage(`"2025-11-01"`)
							routeDefinition, err := json.Marshal(route)
							require.NoError(t, err)
							require.NoError(t, os.WriteFile(routePath, routeDefinition, 0o600))
							resources = append([]resource{
								{recordedRouteID, "route_target", routePath, routeDefinition, 6, 3 * time.Second},
							}, resources...)
						}
						for _, item := range resources {
							create := loadRecordedFixture(t, item.prefix+"_create")
							require.Len(t, create.Interactions, item.interactions)
							final := create
							final.Interactions = create.Interactions[len(create.Interactions)-1:]
							ready := final.Interactions[0].Response.Body
							var terminal struct {
								Result json.RawMessage `json:"result"`
							}
							require.NoError(t, json.Unmarshal(
								create.Interactions[len(create.Interactions)-2].Response.Body, &terminal))
							want, err := recordedJSON(ready)
							require.NoError(t, err)
							actual, err := recordedJSON(terminal.Result)
							require.NoError(t, err)
							require.Equal(t, want, actual, "preserve complete embedded and final-read definitions")
							if item.prefix == "classifier_route" {
								actual, err := recordedJSON(create.Interactions[0].Response.Body)
								require.NoError(t, err)
								require.Equal(t, want, actual, "recorded PUT already returns the complete ready classifier")
								create.Interactions = create.Interactions[:1]
							} else {
								create.Interactions = create.Interactions[:len(create.Interactions)-1]
							}
							executeRecordedAnalyzerStep(t, recordedAnalyzerStep{
								"create", create, []string{"--schema", item.path}, ready, item.wait,
							}, item.analyzer, item.definition, format)
							executeRecordedAnalyzerStep(t, recordedAnalyzerStep{
								"show", final, nil, ready, 0,
							}, item.analyzer, nil, format)
						}
						shown := loadRecordedFixture(t, test.prefix+"_show")
						require.Len(t, shown.Interactions, 1)
						executeRecordedAnalyzerStep(t, recordedAnalyzerStep{
							"show", shown, nil, shown.Interactions[0].Response.Body, 0,
						}, test.analyzer, nil, format)
						for index := len(resources) - 1; index >= 0; index-- {
							item := resources[index]
							deletion := loadRecordedFixture(t, item.prefix+"_delete")
							require.Len(t, deletion.Interactions, 2)
							require.Equal(t, "GET", deletion.Interactions[0].Request.Method)
							require.Equal(t, 204, deletion.Interactions[1].Response.Status)
							result, err := json.Marshal(map[string]any{"analyzerId": item.analyzer, "deleted": true})
							require.NoError(t, err)
							executeRecordedAnalyzerStep(t, recordedAnalyzerStep{
								"delete", deletion, []string{"--yes"}, result, 0,
							}, item.analyzer, nil, format)
						}
					})
				})
			}
		})
	}
}

func TestRecordedClassifierSyntheticPendingStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := loadRecordedFixture(t, "classifier_route_create")
		require.Len(t, fixture.Interactions, 3)
		definition := recordedTemplateGolden(t, "classification", "cu_cli_tmpl_classifier_v1")
		definition = recordedClassifierRouting(t, definition)
		path := filepath.Join(testutil.TempDir(t), "classifier.json")
		require.NoError(t, os.WriteFile(path, definition, 0o600))
		var initial map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(fixture.Interactions[0].Response.Body, &initial))
		initial["status"] = json.RawMessage(`"creating"`)
		body, err := json.Marshal(initial)
		require.NoError(t, err)
		fixture.Interactions[0].Response.Body = body
		final := fixture.Interactions[2].Response.Body
		fixture.Interactions = fixture.Interactions[:2]
		executeRecordedAnalyzerStep(t, recordedAnalyzerStep{
			"create", fixture, []string{"--schema", path}, final, 0,
		}, "cu_cli_tmpl_classifier_v1", definition, "json")
	})
}
