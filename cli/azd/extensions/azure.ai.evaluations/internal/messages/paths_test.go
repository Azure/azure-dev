// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Running `create` before `init` is the first thing anyone does wrong, and the
// bare read failure underneath is a Windows syscall phrase naming neither
// command.
//
// It must still unwrap to fs.ErrNotExist: the callers that tolerate an absent
// configuration decide that by asking, so a nicer sentence that stopped
// answering would turn every one of those into a failure.
func TestNoEvalConfigStaysDetectable(t *testing.T) {
	err := ReadingEvalConfig(`evals\azure.eval.yaml`, fmt.Errorf("open x: %w", fs.ErrNotExist))

	assert.Contains(t, err.Error(), "no eval configuration at evals/azure.eval.yaml")
	assert.Contains(t, err.Error(), "azd ai eval init")
	assert.NotContains(t, err.Error(), "The system cannot find")
	assert.True(t, errors.Is(err, fs.ErrNotExist),
		"callers tolerate an absent config by asking, so it has to keep answering")

	other := ReadingEvalConfig(`evals\azure.eval.yaml`, errors.New("permission denied"))
	assert.Contains(t, other.Error(), "reading eval config")
	assert.False(t, errors.Is(other, fs.ErrNotExist))
}

// as "evals\\azure.eval.yaml". A reader who copies that into a shell gets a path
// that does not exist, which is the opposite of what naming the file is for.
func TestPathsInMessagesStayCopyable(t *testing.T) {
	boom := errors.New("no such file")

	for _, err := range []error{
		ReadingEvalConfig(`evals\azure.eval.yaml`, boom),
		ParsingEvalConfig(`evals\azure.eval.yaml`, boom),
		WritingEvalConfig(`evals\azure.eval.yaml`, boom),
		ReadingFromFile(`evals\datasets\d.jsonl`, boom),
		FromFileMustBeJSONL(`evals\datasets`),
		DatasetSource(`evals\datasets\d.jsonl`, boom),
		EvaluatorSource(`evals\evaluators\e.json`, boom),
		DatasetFileEmpty(`evals\datasets\d.jsonl`),
		ReadingInstructionFile(`docs\instructions.md`, boom),
		InstructionFileEmpty(`docs\instructions.md`),
		Hashing(`evals\evaluators\e.json`, boom),
		Creating(`evals\out`, boom),
		Serializing(`evals\out\x.json`, boom),
		Writing(`evals\out\x.json`, boom),
	} {
		got := err.Error()
		assert.NotContains(t, got, `\\`, "a doubled separator is not a path anyone can use: %s", got)
		assert.Truef(t, strings.Contains(got, "/"), "the path should read with forward slashes: %s", got)
	}
}
