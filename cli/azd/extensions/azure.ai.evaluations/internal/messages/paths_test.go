// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// configPath and friends build the separator this OS actually uses. Hardcoding
// a backslash only exercises the escaping on Windows: filepath.ToSlash rewrites
// the platform separator, so on Linux a literal backslash is left alone -- it is
// part of a filename there, not a separator.
var (
	testConfigPath    = filepath.Join("evals", "azure.eval.yaml")
	testDatasetDir    = filepath.Join("evals", "datasets")
	testDatasetPath   = filepath.Join("evals", "datasets", "d.jsonl")
	testEvaluatorPath = filepath.Join("evals", "evaluators", "e.json")
	testInstructions  = filepath.Join("docs", "instructions.md")
	testOutDir        = filepath.Join("evals", "out")
	testOutPath       = filepath.Join("evals", "out", "x.json")
)

// Running `create` before `init` is the first thing anyone does wrong, and the
// bare read failure underneath is a Windows syscall phrase naming neither
// command.
//
// It must still unwrap to fs.ErrNotExist: the callers that tolerate an absent
// configuration decide that by asking, so a nicer sentence that stopped
// answering would turn every one of those into a failure.
func TestNoEvalConfigStaysDetectable(t *testing.T) {
	err := ReadingEvalConfig(testConfigPath, fmt.Errorf("open x: %w", fs.ErrNotExist))

	assert.Contains(t, err.Error(), "no eval configuration at evals/azure.eval.yaml")
	assert.Contains(t, err.Error(), "azd ai eval init")
	assert.NotContains(t, err.Error(), "The system cannot find")
	assert.True(t, errors.Is(err, fs.ErrNotExist),
		"callers tolerate an absent config by asking, so it has to keep answering")

	other := ReadingEvalConfig(testConfigPath, errors.New("permission denied"))
	assert.Contains(t, other.Error(), "reading eval config")
	assert.False(t, errors.Is(other, fs.ErrNotExist))
}

// as "evals\\azure.eval.yaml". A reader who copies that into a shell gets a path
// that does not exist, which is the opposite of what naming the file is for.
func TestPathsInMessagesStayCopyable(t *testing.T) {
	boom := errors.New("no such file")

	for _, err := range []error{
		ReadingEvalConfig(testConfigPath, boom),
		ParsingEvalConfig(testConfigPath, boom),
		WritingEvalConfig(testConfigPath, boom),
		ReadingFromFile(testDatasetPath, boom),
		FromFileMustBeJSONL(testDatasetDir),
		DatasetSource(testDatasetPath, boom),
		EvaluatorSource(testEvaluatorPath, boom),
		DatasetFileEmpty(testDatasetPath),
		ReadingInstructionFile(testInstructions, boom),
		InstructionFileEmpty(testInstructions),
		Hashing(testEvaluatorPath, boom),
		Creating(testOutDir, boom),
		Serializing(testOutPath, boom),
		Writing(testOutPath, boom),
	} {
		got := err.Error()
		assert.NotContains(t, got, `\\`, "a doubled separator is not a path anyone can use: %s", got)
		assert.Truef(t, strings.Contains(got, "/"), "the path should read with forward slashes: %s", got)
	}
}

// The separator conversion only has anything to convert on Windows: on Linux a
// backslash is an ordinary character in a filename, and filepath.ToSlash leaves
// it alone -- correctly. Without this case the suite would stay green on Linux
// with every filepath.ToSlash call deleted.
func TestWindowsSeparatorsRenderAsForwardSlashes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("filepath.ToSlash only rewrites the platform separator")
	}

	got := ReadingEvalConfig(`evals\azure.eval.yaml`, errors.New("no such file")).Error()

	assert.Contains(t, got, "evals/azure.eval.yaml")
	assert.NotContains(t, got, `\\`,
		"%%q escapes a Windows separator, so the path stops being copyable: %s", got)
}
