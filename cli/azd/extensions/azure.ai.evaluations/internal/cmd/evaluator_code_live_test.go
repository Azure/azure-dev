// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

// This file proves the code-evaluator body the extension builds is accepted by
// the real service, and that what comes back is the shape the extension
// expects.
//
// It exists because the wire contract was settled from source rather than from
// a live call: two published documents disagreed on the definition body, and
// only the service can say which one it honours. It asserts the round trip
// field by field so a drift shows up as a named mismatch, not a vague failure.
//
//	go test -tags live -v ./internal/cmd/ -run TestLiveCodeEvaluator
//
// Required: AZURE_AI_EVAL_E2E_LIVE=1 and FOUNDRY_PROJECT_ENDPOINT.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"

	"github.com/stretchr/testify/require"
)

// liveCodeEvaluatorName is unique per run so concurrent runs, and reruns after
// a failure that skipped cleanup, do not collide.
func liveCodeEvaluatorName(t *testing.T, suffix string) string {
	t.Helper()
	return fmt.Sprintf("azdcode_%s_%d", suffix, time.Now().UnixNano())
}

// writeLiveEvaluator writes a self-contained evaluator script and returns its
// path. It goes through the production loader afterwards, so the shipping
// validation is exercised rather than bypassed.
func writeLiveEvaluator(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name+".py")

	source := `def grade(sample, item) -> float:
    return float(len((item or {}).get("response", "")))
`
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	return path
}

// codeDefinitionOnService reads the registered version back and returns its
// definition, so the assertions run against what the service persisted rather
// than against what was sent.
func codeDefinitionOnService(
	t *testing.T,
	client *eval_api.EvalClient,
	name, version string,
) map[string]json.RawMessage {
	t.Helper()

	raw, err := client.GetEvaluatorRaw(
		context.Background(), name, version, ProjectEndpointAPIVersion)
	require.NoError(t, err, "reading back evaluator %s version %s", name, version)

	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.Contains(t, doc, "definition",
		"the registered evaluator carries no definition: %s", string(raw))

	var definition map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(doc["definition"], &definition))
	return definition
}

func stringField(t *testing.T, definition map[string]json.RawMessage, key string) string {
	t.Helper()
	raw, ok := definition[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

// TestLiveCodeEvaluatorRoundTrip publishes a script and asserts it comes back
// as a code definition carrying the source inline.
//
// code_text is the only source property that reaches the executor: the
// definition is consumed as an OpenAI python grader, whose contract is a
// single Source string. A version registered with blob_uri instead publishes
// cleanly and then fails every run with "top-level grade() function not found
// in source", so what matters here is that the source itself round-trips.
func TestLiveCodeEvaluatorRoundTrip(t *testing.T) {
	client, _ := liveEvalClient(t)
	ctx := context.Background()

	name := liveCodeEvaluatorName(t, "roundtrip")
	path := writeLiveEvaluator(t, name)

	// The shipping loader, not a hand-built script: this test has to fail if
	// the production path stops producing a publishable script.
	script, err := evalcore.LoadCodeEvaluator(name, path)
	require.NoError(t, err)
	require.Contains(t, script.Source, "def grade(")

	opts, err := codeEvaluatorOptions(codeEvaluatorFlags{})
	require.NoError(t, err)

	created, err := client.CreateCodeEvaluatorVersion(ctx, script, opts, ProjectEndpointAPIVersion)
	require.NoError(t, err, "the service rejected the code evaluator body")
	require.NotEmpty(t, created.Version)
	t.Cleanup(func() {
		_ = client.DeleteEvaluatorVersion(
			context.Background(), name, created.Version, ProjectEndpointAPIVersion)
	})

	definition := codeDefinitionOnService(t, client, name, created.Version)

	require.Equal(t, eval_api.CodeDefinitionType, stringField(t, definition, "type"),
		"the discriminator must round-trip as the lowercase snake_case value")
	require.Contains(t, stringField(t, definition, "code_text"), "def grade(",
		"the source must round-trip inline; an empty code_text means the grader "+
			"would be handed nothing to run")
	require.Contains(t, definition, "metrics",
		"a code definition must carry metrics; the service rejects one without")
}

// TestLiveCodeEvaluatorCarriesSchemasAndImage proves the settings that only
// reach the service through flags survive the round trip.
//
// They cannot come from anywhere else. The grader is handed one file of
// source, so a descriptor beside the script would never travel with it, and an
// image tag dropped on the way would leave an evaluator whose imports fail at
// run time with no sign of why.
func TestLiveCodeEvaluatorCarriesSchemasAndImage(t *testing.T) {
	client, _ := liveEvalClient(t)
	ctx := context.Background()

	name := liveCodeEvaluatorName(t, "schemas")
	path := writeLiveEvaluator(t, name)

	script, err := evalcore.LoadCodeEvaluator(name, path)
	require.NoError(t, err)

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(
		`{"type":"object","properties":{"response":{"type":"string"}},"required":["response"]}`,
	), 0o600))

	opts, err := codeEvaluatorOptions(codeEvaluatorFlags{dataSchema: schemaPath})
	require.NoError(t, err)
	require.NotEmpty(t, opts.DataSchema)

	created, err := client.CreateCodeEvaluatorVersion(ctx, script, opts, ProjectEndpointAPIVersion)
	require.NoError(t, err, "the service rejected a definition carrying a data schema")
	t.Cleanup(func() {
		_ = client.DeleteEvaluatorVersion(
			context.Background(), name, created.Version, ProjectEndpointAPIVersion)
	})

	definition := codeDefinitionOnService(t, client, name, created.Version)
	require.Contains(t, definition, "data_schema",
		"the declared data schema must round-trip; without it the criteria builder "+
			"derives no data_mapping and the eval cannot be created")
	require.Contains(t, string(definition["data_schema"]), "response")
}

// TestLiveCodeEvaluatorPublishesANewVersion proves a second publish does not
// overwrite the first.
//
// Versions are immutable and evals bind to one, so a publish that replaced the
// previous version would silently change what every existing eval evaluates.
//
// The wait between the two publishes is not padding. The service assigns the
// next version from its own listing, and that listing lags the create by about
// a second: two publishes issued back to back were both answered with version
// 1, the second overwriting the first. The reconciler waits for a published
// version to appear in the listing before it moves on, so this waits the same
// way — the assertion is about publishing twice, not about racing the service.
func TestLiveCodeEvaluatorPublishesANewVersion(t *testing.T) {
	client, _ := liveEvalClient(t)
	ctx := context.Background()

	name := liveCodeEvaluatorName(t, "versions")
	path := writeLiveEvaluator(t, name)

	script, err := evalcore.LoadCodeEvaluator(name, path)
	require.NoError(t, err)
	opts, err := codeEvaluatorOptions(codeEvaluatorFlags{})
	require.NoError(t, err)

	first, err := client.CreateCodeEvaluatorVersion(ctx, script, opts, ProjectEndpointAPIVersion)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.DeleteEvaluatorVersion(
			context.Background(), name, first.Version, ProjectEndpointAPIVersion)
	})
	require.NotEmpty(t, first.Version, "the service must assign a version")

	awaitVersionListed(t, client, name, first.Version)

	second, err := client.CreateCodeEvaluatorVersion(ctx, script, opts, ProjectEndpointAPIVersion)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.DeleteEvaluatorVersion(
			context.Background(), name, second.Version, ProjectEndpointAPIVersion)
	})
	require.NotEqual(t, first.Version, second.Version,
		"a second publish must create a new version rather than replace the first")
}

// awaitVersionListed blocks until a published version shows up in the version
// listing, which is the view the service's own version assignment reads.
func awaitVersionListed(t *testing.T, client *eval_api.EvalClient, name, version string) {
	t.Helper()
	ctx := context.Background()

	start := time.Now()
	for {
		list, err := client.ListEvaluatorVersions(ctx, name, ProjectEndpointAPIVersion)
		if err == nil && list != nil {
			for _, entry := range list.Value {
				if entry.Version == version {
					t.Logf("version %s listed after %s",
						version, time.Since(start).Round(time.Millisecond))
					return
				}
			}
		}
		if time.Since(start) > 30*time.Second {
			t.Fatalf("version %s of evaluator %s never appeared in the listing", version, name)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
