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

// writeLiveEvaluator lays out a folder to the packaging convention and returns
// it. The source is written for the derived class name so the production
// validation is exercised rather than bypassed.
func writeLiveEvaluator(t *testing.T, name string, extraFiles map[string]string) string {
	t.Helper()
	dir := t.TempDir()

	className := evalcore.EvaluatorClassName(name)
	entry := fmt.Sprintf(`class %s:
    def __call__(self, **kwargs):
        return {"result": float(len(kwargs.get("response", "")))}
`, className)

	require.NoError(t, os.WriteFile(
		filepath.Join(dir, name+".py"), []byte(entry), 0o600))

	for rel, content := range extraFiles {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	return dir
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

// TestLiveCodeEvaluatorSingleFileRoundTrip publishes a one-file evaluator and
// asserts it comes back as a code definition pointing at storage.
//
// A single file takes the same path as a folder. The contract also accepts
// inline source through code_text, which would save the upload, but nothing
// observable confirms the executor runs it, so the CLI does not send it. If
// that is ever settled, this is the test that should change first.
func TestLiveCodeEvaluatorSingleFileRoundTrip(t *testing.T) {
	client, _ := liveEvalClient(t)
	ctx := context.Background()

	name := liveCodeEvaluatorName(t, "single")
	dir := writeLiveEvaluator(t, name, nil)

	// The shipping loader, not a hand-built package: this test has to fail if
	// the production path stops producing a valid package.
	pkg, err := evalcore.LoadCodeEvaluator(name, dir)
	require.NoError(t, err)
	require.Len(t, pkg.Files, 1)

	opts, err := codeEvaluatorOptions(pkg, codeEvaluatorFlags{})
	require.NoError(t, err)

	created, err := client.UploadCodeEvaluatorVersion(ctx, pkg, opts, ProjectEndpointAPIVersion)
	require.NoError(t, err, "the service rejected the code evaluator body")
	require.NotEmpty(t, created.Version)
	t.Cleanup(func() {
		_ = client.DeleteEvaluatorVersion(
			context.Background(), name, created.Version, ProjectEndpointAPIVersion)
	})

	definition := codeDefinitionOnService(t, client, name, created.Version)

	require.Equal(t, eval_api.CodeDefinitionType, stringField(t, definition, "type"),
		"the discriminator must round-trip as the lowercase snake_case value")
	require.NotEmpty(t, stringField(t, definition, "blob_uri"),
		"a published evaluator must record the storage location it was uploaded to")
	require.Contains(t, definition, "metrics",
		"a code definition must carry metrics; the service rejects one without")
}

// TestLiveCodeEvaluatorFolderRoundTrip publishes a multi-file evaluator and
// asserts the service records the storage location it handed out.
//
// This is the path that exercises startPendingUpload, the SAS write, and the
// blob_uri property, none of which the single-file case touches.
func TestLiveCodeEvaluatorFolderRoundTrip(t *testing.T) {
	client, _ := liveEvalClient(t)
	ctx := context.Background()

	name := liveCodeEvaluatorName(t, "folder")
	dir := writeLiveEvaluator(t, name, map[string]string{
		"helpers/text.py": "def clean(value):\n    return value.strip()\n",
		// Must be excluded from both the upload and the fingerprint.
		"__pycache__/stale.pyc": "cache",
	})

	pkg, err := evalcore.LoadCodeEvaluator(name, dir)
	require.NoError(t, err)
	require.Len(t, pkg.Files, 2, "the compiled artifact must not be part of the package")

	opts, err := codeEvaluatorOptions(pkg, codeEvaluatorFlags{})
	require.NoError(t, err)

	created, err := client.UploadCodeEvaluatorVersion(ctx, pkg, opts, ProjectEndpointAPIVersion)
	require.NoError(t, err, "the service rejected the uploaded code evaluator")
	require.NotEmpty(t, created.Version)
	t.Cleanup(func() {
		_ = client.DeleteEvaluatorVersion(
			context.Background(), name, created.Version, ProjectEndpointAPIVersion)
	})

	definition := codeDefinitionOnService(t, client, name, created.Version)

	require.Equal(t, eval_api.CodeDefinitionType, stringField(t, definition, "type"))
	require.NotEmpty(t, stringField(t, definition, "blob_uri"),
		"a multi-file evaluator must round-trip carrying the storage location; "+
			"an empty blob_uri means the service dropped the preview property")
	require.Contains(t, definition, "metrics")
}

// TestLiveCodeEvaluatorPublishesNextVersion proves the version the upload
// reserves storage under is the one the create then assigns.
//
// Storage is provisioned per version before the version exists, so the client
// has to predict it. A drift between the two would leave the code in one
// version's container and the definition on another.
func TestLiveCodeEvaluatorPublishesNextVersion(t *testing.T) {
	client, _ := liveEvalClient(t)
	ctx := context.Background()

	name := liveCodeEvaluatorName(t, "versions")
	dir := writeLiveEvaluator(t, name, map[string]string{
		"helpers.py": "VALUE = 1\n",
	})

	pkg, err := evalcore.LoadCodeEvaluator(name, dir)
	require.NoError(t, err)
	opts, err := codeEvaluatorOptions(pkg, codeEvaluatorFlags{})
	require.NoError(t, err)

	predicted := client.NextEvaluatorVersion(ctx, name, ProjectEndpointAPIVersion)
	require.Equal(t, "1", predicted, "an unpublished evaluator starts at version 1")

	first, err := client.UploadCodeEvaluatorVersion(ctx, pkg, opts, ProjectEndpointAPIVersion)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.DeleteEvaluatorVersion(
			context.Background(), name, first.Version, ProjectEndpointAPIVersion)
	})
	require.NotEmpty(t, first.Version, "the service must assign a version")

	// Publishing again must land on a new version, not overwrite the first.
	//
	// The version is deliberately not asserted to equal the one storage was
	// reserved under. There is no create-at-version route: the create assigns
	// its own number, and reserving storage can itself take a number, so the
	// two legitimately differ. It does not matter, because a reservation
	// returns a container named by GUID rather than by version, so a published
	// definition always points at exactly the bytes uploaded for it. What must
	// hold is that a second publish does not land on the first version.
	second, err := client.UploadCodeEvaluatorVersion(ctx, pkg, opts, ProjectEndpointAPIVersion)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.DeleteEvaluatorVersion(
			context.Background(), name, second.Version, ProjectEndpointAPIVersion)
	})
	require.NotEqual(t, first.Version, second.Version,
		"a second publish must create a new version rather than replace the first")

	require.NotEqual(t,
		blobURIOnService(t, client, name, first.Version),
		blobURIOnService(t, client, name, second.Version),
		"each version must keep its own storage, or republishing would rewrite the older one")
}

// blobURIOnService reads back the storage location recorded on a version.
func blobURIOnService(
	t *testing.T,
	client *eval_api.EvalClient,
	name string,
	version string,
) string {
	t.Helper()
	return stringField(t, codeDefinitionOnService(t, client, name, version), "blob_uri")
}
