// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package evalcore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// gradeSource is the minimal script that satisfies the grader contract.
const gradeSource = `def grade(sample, item) -> float:
    return float(len((item or {}).get("response", "")))
`

func writeFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// The extension decides which definition type to publish from the source's
// extension alone, and it has to answer for a path that does not exist yet so
// a config can be validated before its files are written.
func TestIsCodeEvaluatorSource(t *testing.T) {
	require.True(t, IsCodeEvaluatorSource("tone.py"))
	require.True(t, IsCodeEvaluatorSource("evaluators/tone.py"))
	require.True(t, IsCodeEvaluatorSource(`evaluators\tone.PY`),
		"the extension is matched case-insensitively")
	require.True(t, IsCodeEvaluatorSource("/absent/never/created.py"),
		"classification must not touch the filesystem")

	require.False(t, IsCodeEvaluatorSource("rubric.json"))
	require.False(t, IsCodeEvaluatorSource("evaluators/rubric.json"))
	require.False(t, IsCodeEvaluatorSource("evaluator"),
		"a folder is no longer a code evaluator; the grader takes one script")
	require.False(t, IsCodeEvaluatorSource("tone.python"))
	require.False(t, IsCodeEvaluatorSource(""))
}

func TestLoadCodeEvaluator_AcceptsATopLevelGrade(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "tone.py", gradeSource)

	script, err := LoadCodeEvaluator("tone", path)
	require.NoError(t, err)
	require.Equal(t, "tone", script.Name)
	require.Equal(t, path, script.Path)
	require.Equal(t, gradeSource, script.Source,
		"the whole file is what the grader is sent")
}

// The grader is handed source and calls grade(); an async definition is still
// a top-level grade().
func TestLoadCodeEvaluator_AcceptsAsyncAndAnnotatedForms(t *testing.T) {
	for label, source := range map[string]string{
		"async":      "async def grade(sample, item) -> float:\n    return 1.0\n",
		"spaced":     "def grade (sample, item):\n    return 1.0\n",
		"no-annot":   "def grade(sample, item):\n    return 1.0\n",
		"after-code": "import json\n\n\ndef grade(sample, item):\n    return 1.0\n",
	} {
		dir := t.TempDir()
		path := writeFile(t, dir, "tone.py", source)
		_, err := LoadCodeEvaluator("tone", path)
		require.NoError(t, err, "for %s", label)
	}
}

// Without this the failure surfaces only when a run executes, as "Invalid
// grader source: top-level grade() function not found in source" — long after
// a version has been published and an eval bound to it.
func TestLoadCodeEvaluator_ReportsAMissingGrade(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "tone.py",
		"class ToneEvaluator:\n    def __call__(self, **kwargs):\n        return {\"result\": 1}\n")

	_, err := LoadCodeEvaluator("tone", path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "grade(sample, item)")
	require.Contains(t, err.Error(), path)
}

// A grade() nested inside a class is a method. The grader only ever calls a
// module-level function, so an indented match must not pass validation.
func TestLoadCodeEvaluator_RejectsANestedGrade(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "tone.py",
		"class ToneEvaluator:\n    def grade(self, sample, item):\n        return 1.0\n")

	_, err := LoadCodeEvaluator("tone", path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "top-level")
}

func TestLoadCodeEvaluator_RejectsAFolder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tone.py", gradeSource)

	_, err := LoadCodeEvaluator("tone", dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "single")
}

func TestLoadCodeEvaluator_RejectsBadInput(t *testing.T) {
	dir := t.TempDir()

	_, err := LoadCodeEvaluator("", writeFile(t, dir, "tone.py", gradeSource))
	require.Error(t, err, "a name is required to publish under")

	_, err = LoadCodeEvaluator("tone", "")
	require.Error(t, err)

	_, err = LoadCodeEvaluator("tone", filepath.Join(dir, "absent.py"))
	require.Error(t, err)

	_, err = LoadCodeEvaluator("tone", writeFile(t, dir, "rubric.json", "{}"))
	require.Error(t, err, "a rubric is not a code evaluator")
	require.Contains(t, err.Error(), ".py")

	_, err = LoadCodeEvaluator("tone", writeFile(t, dir, "empty.py", "   \n\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")
}
