// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootIncludesExperimentTrackingCommands(t *testing.T) {
	root := NewRootCommand()
	names := map[string]bool{}
	for _, command := range root.Commands() {
		names[command.Name()] = true
	}

	assert.True(t, names["run"])
	assert.True(t, names["ingest"])
	assert.True(t, names["wandb"])
}

func TestRunCommandIncludesAllReadAndAgentSurfaces(t *testing.T) {
	command := newExperimentRunCommand(nil)
	names := map[string]bool{}
	for _, child := range command.Commands() {
		names[child.Name()] = true
	}

	for _, name := range []string{
		"compare",
		"history-keys",
		"list",
		"log-records",
		"logs",
		"metrics",
		"spans",
		"summary",
		"system-metrics",
		"trace",
		"traces",
	} {
		assert.True(t, names[name], "missing run command %q", name)
	}
}

func TestReadFilterExpressionDefaultsToMatchAll(t *testing.T) {
	filter, err := readFilterExpression("", "")
	require.NoError(t, err)
	assert.JSONEq(t, `{"$expr":true}`, string(filter))
}

func TestReadFilterExpressionFromFile(t *testing.T) {
	filterPath := filepath.Join(t.TempDir(), "filter.json")
	require.NoError(t, os.WriteFile(
		filterPath,
		[]byte(`{
			"$expr": {
				"$eq": [
					{"$getField": "span_name"},
					{"$literal": "chat"}
				]
			}
		}`),
		0o600,
	))

	filter, err := readFilterExpression("", filterPath)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(filter, &parsed))
	require.Contains(t, parsed, "$expr")
}

func TestReadFilterExpressionRejectsConflictingInputs(t *testing.T) {
	_, err := readFilterExpression(`{"$expr":true}`, "filter.json")
	require.Error(t, err)
}

func TestBuildSpanQueryBody(t *testing.T) {
	body := buildSpanQueryBody(
		"my-project",
		json.RawMessage(`{
			"$expr": {
				"$eq": [
					{"$getField": "span_name"},
					{"$literal": "chat"}
				]
			}
		}`),
		true,
		10,
	)

	data, err := json.Marshal(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"project_id": "my-project",
		"query": {
			"$expr": {
				"$eq": [
					{"$getField": "span_name"},
					{"$literal": "chat"}
				]
			}
		},
		"include_details": true,
		"limit": 10
	}`, string(data))
}

func TestReadJSONObjectPreservesLargeInteger(t *testing.T) {
	requestPath := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, os.WriteFile(
		requestPath,
		[]byte(`{"id":9007199254740993}`),
		0o600,
	))

	body, err := readJSONObject(requestPath)
	require.NoError(t, err)

	id, ok := body["id"].(json.Number)
	require.True(t, ok)
	assert.Equal(t, "9007199254740993", id.String())

	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":9007199254740993}`, string(encoded))
}

func TestReadJSONObjectRejectsMultipleValues(t *testing.T) {
	requestPath := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, os.WriteFile(
		requestPath,
		[]byte(`{"first":true} {"second":true}`),
		0o600,
	))

	_, err := readJSONObject(requestPath)
	require.Error(t, err)
}

func TestReadNonEmptyExperimentInputRejectsEmptyPayload(t *testing.T) {
	payloadPath := filepath.Join(t.TempDir(), "payload.pb")
	require.NoError(t, os.WriteFile(payloadPath, nil, 0o600))

	_, err := readNonEmptyExperimentInput(payloadPath)
	require.Error(t, err)
}

func TestExperimentCommandsUseJSONOutput(t *testing.T) {
	commands := []*cobra.Command{
		newRunListCommand(nil),
		newRunSpansQueryCommand(nil),
		newOTLPIngestCommand(nil, "metrics"),
		newWandBGraphQLCommand(nil),
	}
	for _, command := range commands {
		assertOutputFlagOptions(t, command, "json", []string{"json"})
	}
}
