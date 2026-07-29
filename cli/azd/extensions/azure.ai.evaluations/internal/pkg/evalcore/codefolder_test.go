// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package evalcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeFile creates a file and every directory above it.
func writeFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// evaluatorSource is a minimal evaluator matching the packaging convention.
func evaluatorSource(className string) string {
	return "class " + className + ":\n" +
		"    def __call__(self, **kwargs):\n" +
		"        return {\"result\": len(kwargs.get(\"response\", \"\"))}\n"
}

// relPaths reads the walk result into a comparable list.
func relPaths(files []CodeFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.RelPath)
	}
	return out
}

func TestWalkCodeFolder_ExcludesBuildAndDependencyTrees(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "answer_length.py", evaluatorSource("AnswerLengthEvaluator"))
	writeFile(t, dir, "helpers/text.py", "def clean(s): return s.strip()\n")
	writeFile(t, dir, "README.md", "docs\n")

	// Every one of these must be skipped, along with everything under it.
	writeFile(t, dir, "__pycache__/answer_length.cpython-311.pyc", "cache")
	writeFile(t, dir, ".git/config", "[core]")
	writeFile(t, dir, ".venv/lib/site.py", "venv")
	writeFile(t, dir, "venv/lib/site.py", "venv")
	writeFile(t, dir, "node_modules/pkg/index.js", "js")
	writeFile(t, dir, ".mypy_cache/report.json", "{}")
	writeFile(t, dir, "helpers/__pycache__/text.cpython-311.pyc", "cache")
	// Compiled artifacts are skipped wherever they sit, not just in caches.
	writeFile(t, dir, "stale.pyc", "cache")
	writeFile(t, dir, "stale.pyo", "cache")

	files, err := WalkCodeFolder(dir)
	require.NoError(t, err)

	require.Equal(t,
		[]string{"README.md", "answer_length.py", "helpers/text.py"},
		relPaths(files),
		"only source and data files belong in the package, in sorted order")
}

// Dot-prefixed files are excluded because an evaluator folder living in a repo
// collects credential files, and publishing the package would copy them into
// blob storage. Excluding the directories alone is not enough: the ones that
// hold secrets sit at the root, next to the source.
func TestWalkCodeFolder_ExcludesDotFiles(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "answer_length.py", evaluatorSource("AnswerLengthEvaluator"))
	writeFile(t, dir, ".env", "AZURE_OPENAI_API_KEY=super-secret\n")
	writeFile(t, dir, ".netrc", "machine example.com password hunter2\n")
	writeFile(t, dir, ".pypirc", "[pypi]\npassword = leaked\n")
	writeFile(t, dir, "helpers/.env", "NESTED_SECRET=1\n")

	files, err := WalkCodeFolder(dir)
	require.NoError(t, err)

	require.Equal(t, []string{"answer_length.py"}, relPaths(files),
		"no dot-prefixed file may reach the upload, at any depth")

	for _, f := range files {
		require.NotContains(t, f.RelPath, ".env")
		require.NotContains(t, f.RelPath, ".netrc")
		require.NotContains(t, f.RelPath, ".pypirc")
	}
}

// Relative paths are the blob names and part of the fingerprint, so they must
// not depend on the host's path separator.
func TestWalkCodeFolder_UsesForwardSlashes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a/b/c.py", "x = 1\n")

	files, err := WalkCodeFolder(dir)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "a/b/c.py", files[0].RelPath)
	require.NotContains(t, files[0].RelPath, "\\")
}

func TestWalkCodeFolder_RejectsAFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "rubric.json", "{}")

	_, err := WalkCodeFolder(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "folder")
}

func TestEvaluatorClassName(t *testing.T) {
	cases := map[string]string{
		"answer_length":           "AnswerLengthEvaluator",
		"answer-length":           "AnswerLengthEvaluator",
		"answer_length_evaluator": "AnswerLengthEvaluator",
		"tone":                    "ToneEvaluator",
		"ToneEvaluator":           "ToneEvaluator",
		"my.custom_check":         "MyCustomCheckEvaluator",
	}
	for name, want := range cases {
		require.Equal(t, want, EvaluatorClassName(name), "for %q", name)
	}
}

func TestLoadCodeEvaluator_AcceptsAConventionalFolder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "answer_length.py", evaluatorSource("AnswerLengthEvaluator"))
	writeFile(t, dir, "helpers.py", "def n(s): return len(s)\n")

	pkg, err := LoadCodeEvaluator("answer_length", dir)
	require.NoError(t, err)
	require.Equal(t, "answer_length.py", pkg.EntryPoint)
	require.Equal(t, "AnswerLengthEvaluator", pkg.ClassName)
	require.Equal(t, []string{"answer_length.py", "helpers.py"}, relPaths(pkg.Files))
	require.Nil(t, pkg.Metadata)
}

// The class may inherit, and may be indented inside a conditional.
func TestLoadCodeEvaluator_AcceptsInheritingClass(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tone.py", "import abc\n\nclass ToneEvaluator(abc.ABC):\n    pass\n")

	pkg, err := LoadCodeEvaluator("tone", dir)
	require.NoError(t, err)
	require.Equal(t, "ToneEvaluator", pkg.ClassName)
}

func TestLoadCodeEvaluator_ReportsMissingEntryPoint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.py", evaluatorSource("AnswerLengthEvaluator"))

	_, err := LoadCodeEvaluator("answer_length", dir)
	require.Error(t, err)
	// The message has to say what is missing, what it must hold, and what was
	// actually found — a bare "not found" leaves the author guessing.
	require.Contains(t, err.Error(), "answer_length.py")
	require.Contains(t, err.Error(), "AnswerLengthEvaluator")
	require.Contains(t, err.Error(), "main.py")
}

func TestLoadCodeEvaluator_ReportsMissingClass(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tone.py", "class SomethingElse:\n    pass\n")

	_, err := LoadCodeEvaluator("tone", dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ToneEvaluator")
	require.Contains(t, err.Error(), "__call__")
}

func TestLoadCodeEvaluator_RejectsAnEmptyFolder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "__pycache__/x.pyc", "cache")

	_, err := LoadCodeEvaluator("tone", dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no files")
}

func TestLoadCodeEvaluator_ReadsFolderMetadata(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tone.py", evaluatorSource("ToneEvaluator"))
	writeFile(t, dir, CodeEvaluatorMetadataFile, `{
		"display_name": "Tone",
		"description": "Scores tone.",
		"metrics": {"result": {"type": "ordinal", "min_value": 1, "max_value": 5}},
		"data_schema": {"type": "object", "properties": {"response": {"type": "string"}}}
	}`)

	pkg, err := LoadCodeEvaluator("tone", dir)
	require.NoError(t, err)
	require.NotNil(t, pkg.Metadata)
	require.Equal(t, "Tone", pkg.Metadata.DisplayName)
	require.Contains(t, string(pkg.Metadata.Metrics), "ordinal")
	require.Contains(t, string(pkg.Metadata.DataSchema), "response")
}

// A descriptor that cannot be read is a mistake worth reporting: publishing
// without the schemas it declares would register an evaluator the author did
// not describe.
func TestLoadCodeEvaluator_RejectsMalformedMetadata(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tone.py", evaluatorSource("ToneEvaluator"))
	writeFile(t, dir, CodeEvaluatorMetadataFile, "{not json")

	_, err := LoadCodeEvaluator("tone", dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), CodeEvaluatorMetadataFile)
}

func TestIsCodeEvaluatorSource(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "rubric.json", "{}")
	// A folder can be named like a file, so the decision cannot be made by
	// looking at the string.
	misleading := filepath.Join(dir, "looks_like.json")
	require.NoError(t, os.MkdirAll(misleading, 0o755))

	require.True(t, IsCodeEvaluatorSource(dir))
	require.True(t, IsCodeEvaluatorSource(misleading))
	require.False(t, IsCodeEvaluatorSource(file))
	require.False(t, IsCodeEvaluatorSource(filepath.Join(dir, "absent")))
	require.False(t, IsCodeEvaluatorSource(""))
}

func TestFingerprintCodeFolder_IsStableAndSensitive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "answer_length.py", evaluatorSource("AnswerLengthEvaluator"))
	writeFile(t, dir, "helpers/text.py", "def clean(s): return s.strip()\n")

	first, err := FingerprintCodeFolder(dir)
	require.NoError(t, err)

	again, err := FingerprintCodeFolder(dir)
	require.NoError(t, err)
	require.Equal(t, first, again, "an unchanged folder must hash the same")

	// One byte.
	writeFile(t, dir, "helpers/text.py", "def clean(s): return s.rstrip()\n")
	changed, err := FingerprintCodeFolder(dir)
	require.NoError(t, err)
	require.NotEqual(t, first, changed, "changed content must hash differently")
}

// Renaming a file changes the package even when every byte is preserved: the
// entry point is resolved by name and imports are written against it.
func TestFingerprintCodeFolder_NoticesARename(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tone.py", evaluatorSource("ToneEvaluator"))
	writeFile(t, dir, "helpers.py", "X = 1\n")

	before, err := FingerprintCodeFolder(dir)
	require.NoError(t, err)

	require.NoError(t, os.Rename(
		filepath.Join(dir, "helpers.py"), filepath.Join(dir, "util.py")))

	after, err := FingerprintCodeFolder(dir)
	require.NoError(t, err)
	require.NotEqual(t, before, after)
}

// The filesystem does not promise a stable iteration order, so the digest must
// describe the package rather than the order it was handed over in. The input
// here is the production walk's own output, permuted — not a hand-built list.
func TestFingerprintCodeFiles_IgnoresInputOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tone.py", evaluatorSource("ToneEvaluator"))
	writeFile(t, dir, "helpers/a.py", "A = 1\n")
	writeFile(t, dir, "helpers/b.py", "B = 2\n")
	writeFile(t, dir, "data/prompts.txt", "hello\n")

	files, err := WalkCodeFolder(dir)
	require.NoError(t, err)
	require.Len(t, files, 4)

	fromFolder, err := FingerprintCodeFolder(dir)
	require.NoError(t, err)

	reversed := make([]CodeFile, 0, len(files))
	for i := len(files) - 1; i >= 0; i-- {
		reversed = append(reversed, files[i])
	}
	fromReversed, err := FingerprintCodeFiles(reversed)
	require.NoError(t, err)
	require.Equal(t, fromFolder, fromReversed, "file order must not change the digest")

	rotated := append(append([]CodeFile{}, files[2:]...), files[:2]...)
	fromRotated, err := FingerprintCodeFiles(rotated)
	require.NoError(t, err)
	require.Equal(t, fromFolder, fromRotated)
}

// Where the folder sits must not affect the digest: two checkouts of the same
// repo, or the same repo on two machines, have to agree or every deploy
// republishes.
func TestFingerprintCodeFolder_IgnoresLocation(t *testing.T) {
	build := func(root string) string {
		writeFile(t, root, "tone.py", evaluatorSource("ToneEvaluator"))
		writeFile(t, root, "helpers/a.py", "A = 1\n")
		digest, err := FingerprintCodeFolder(root)
		require.NoError(t, err)
		return digest
	}

	require.Equal(t, build(t.TempDir()), build(t.TempDir()))
}

// Excluded content must not feed the digest, or a rebuild that only refreshes
// __pycache__ would look like an evaluator change and publish a version.
func TestFingerprintCodeFolder_IgnoresExcludedContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tone.py", evaluatorSource("ToneEvaluator"))

	before, err := FingerprintCodeFolder(dir)
	require.NoError(t, err)

	writeFile(t, dir, "__pycache__/tone.cpython-311.pyc", strings.Repeat("x", 64))
	writeFile(t, dir, ".venv/lib/site.py", "noise")

	after, err := FingerprintCodeFolder(dir)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
