// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/require"
)

func writeTestFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

const toneEvaluatorSource = "class ToneEvaluator:\n" +
	"    def __call__(self, **kwargs):\n" +
	"        return {\"result\": 1}\n"

// An evaluator is either a rubric or code. Naming both, or neither, is a
// mistake the command has to name precisely — the two flags take different
// kinds of path and produce different definition types.
func TestValidateEvaluatorSource(t *testing.T) {
	err := validateEvaluatorSource("", "", codeEvaluatorFlags{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--rubric")
	require.Contains(t, err.Error(), "--folder")
	require.Contains(t, err.Error(), "required")

	err = validateEvaluatorSource("rubric.json", "./evaluator", codeEvaluatorFlags{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be used together")

	require.NoError(t, validateEvaluatorSource("rubric.json", "", codeEvaluatorFlags{}))
	require.NoError(t, validateEvaluatorSource("", "./evaluator", codeEvaluatorFlags{}))
}

// The schema overrides describe a code evaluator. Accepting them beside a
// rubric and dropping them would leave the author believing the evaluator was
// published carrying schemas it never had.
func TestValidateEvaluatorSource_RejectsCodeFlagsOnARubric(t *testing.T) {
	for flag, flags := range map[string]codeEvaluatorFlags{
		"init-params": {initParams: "init.json"},
		"data-schema": {dataSchema: "schema.json"},
		"metrics":     {metrics: "metrics.json"},
	} {
		err := validateEvaluatorSource("rubric.json", "", flags)
		require.Error(t, err, "for --%s", flag)
		require.Contains(t, err.Error(), "--"+flag)
		require.Contains(t, err.Error(), "--folder")

		require.NoError(t, validateEvaluatorSource("", "./evaluator", flags),
			"--%s is valid with --folder", flag)
	}
}

// The same check the command runs must be reachable from the command, so a
// future refactor cannot leave the flags declared but unvalidated.
func TestEvaluatorCreateRejectsBothSources(t *testing.T) {
	cmd := newEvaluatorCreateCommand()
	cmd.SetArgs([]string{"--name", "tone", "--rubric", "r.json", "--folder", "./x"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be used together")
}

func TestEvaluatorCreateRejectsNeitherSource(t *testing.T) {
	cmd := newEvaluatorCreateCommand()
	cmd.SetArgs([]string{"--name", "tone"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "one of --rubric or --folder")
}

// The folder is the natural home for the schemas, but a folder that carries
// none still has to be publishable without editing it.
func TestCodeEvaluatorOptions_FlagsOverrideFolderMetadata(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "tone.py", toneEvaluatorSource)
	writeTestFile(t, dir, evalcore.CodeEvaluatorMetadataFile, `{
		"display_name": "Tone",
		"metrics": {"result": {"type": "ordinal"}},
		"data_schema": {"type": "object", "properties": {"a": {"type": "string"}}}
	}`)

	pkg, err := evalcore.LoadCodeEvaluator("tone", dir)
	require.NoError(t, err)

	// Nothing overridden: the folder wins.
	opts, err := codeEvaluatorOptions(pkg, codeEvaluatorFlags{})
	require.NoError(t, err)
	require.Equal(t, "Tone", opts.DisplayName)
	require.Contains(t, string(opts.Metrics), "ordinal")
	require.Contains(t, string(opts.DataSchema), `"a"`)
	require.Empty(t, opts.InitParameters)

	overrides := t.TempDir()
	metricsPath := writeTestFile(t, overrides, "metrics.json",
		`{"result":{"type":"continuous"}}`)
	initPath := writeTestFile(t, overrides, "init.json",
		`{"type":"object","properties":{"deployment_name":{"type":"string"}}}`)

	opts, err = codeEvaluatorOptions(pkg, codeEvaluatorFlags{
		metrics:    metricsPath,
		initParams: initPath,
	})
	require.NoError(t, err)
	require.Contains(t, string(opts.Metrics), "continuous")
	require.NotContains(t, string(opts.Metrics), "ordinal")
	require.Contains(t, string(opts.InitParameters), "deployment_name")
	// Untouched by the overrides.
	require.Contains(t, string(opts.DataSchema), `"a"`)
}

// A typo in a schema file must be reported against the flag that named it,
// not discovered by the service after a version has been published.
func TestCodeEvaluatorOptions_RejectsMalformedOverride(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "tone.py", toneEvaluatorSource)
	pkg, err := evalcore.LoadCodeEvaluator("tone", dir)
	require.NoError(t, err)

	bad := writeTestFile(t, dir, "metrics.json", "[1,2,3]")
	_, err = codeEvaluatorOptions(pkg, codeEvaluatorFlags{metrics: bad})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--metrics")
	require.Contains(t, err.Error(), "JSON object")

	_, err = codeEvaluatorOptions(pkg, codeEvaluatorFlags{
		dataSchema: filepath.Join(dir, "absent.json"),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--data-schema")
}

// The service rejects a code definition carrying no metrics, so a folder that
// declares none still has to publish with one.
func TestDefaultCodeMetricsIsAJSONObject(t *testing.T) {
	var metrics map[string]map[string]any
	require.NoError(t, json.Unmarshal(eval_api.DefaultCodeMetrics, &metrics))
	require.Len(t, metrics, 1)
	require.Contains(t, metrics, "result")
	require.Equal(t, "continuous", metrics["result"]["type"])
}

// Change detection has to work for both kinds of evaluator source, and the
// reconciler decides which by stat-ing the path.
func TestFingerprintPath_HandlesFilesAndFolders(t *testing.T) {
	root := t.TempDir()

	file := writeTestFile(t, root, "rubric.json", `{"dimensions":[]}`)
	fileDigest, err := project.FingerprintPath(file)
	require.NoError(t, err)
	plainDigest, err := project.Fingerprint(file)
	require.NoError(t, err)
	require.Equal(t, plainDigest, fileDigest,
		"a file must hash the same through either entry point")

	dir := t.TempDir()
	writeTestFile(t, dir, "tone.py", toneEvaluatorSource)
	folderDigest, err := project.FingerprintPath(dir)
	require.NoError(t, err)
	require.NotEmpty(t, folderDigest)
	require.NotEqual(t, fileDigest, folderDigest)

	writeTestFile(t, dir, "helpers.py", "X = 1\n")
	changed, err := project.FingerprintPath(dir)
	require.NoError(t, err)
	require.NotEqual(t, folderDigest, changed,
		"adding a file to the folder must change the digest")

	_, err = project.FingerprintPath(filepath.Join(root, "absent"))
	require.Error(t, err)
}
