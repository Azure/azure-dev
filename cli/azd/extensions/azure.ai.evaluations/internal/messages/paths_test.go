// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// %q escapes a Windows separator, so a path printed straight through comes back
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
