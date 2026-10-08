// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
)

func notFoundErr() error {
	return &azcore.ResponseError{StatusCode: http.StatusNotFound, ErrorCode: "not_found"}
}

// A misspelled --target is the common cause, and 404 answers it with ten lines
// of URL, status rule and nested JSON for a fact that fits on one.
func TestAgentWarningsAnswerA404InOneLine(t *testing.T) {
	for _, line := range []string{
		WarningAgentUnreadable("suport-agent", notFoundErr()),
		CouldNotReadAgentForModel("suport-agent", notFoundErr()),
	} {
		assert.Contains(t, line, `no agent "suport-agent" in this project`)
		assert.NotContains(t, line, "RESPONSE 404")
		assert.NotContains(t, line, "https://")
		assert.Equal(t, 1, countLines(line), "a 404 is one fact, so it gets one line: %s", line)
	}

	// Anything else keeps the detail, because it is not a fact anyone knows yet.
	other := WarningAgentUnreadable("support-agent", errors.New("connection reset"))
	assert.Contains(t, other, "connection reset")
}

// The declared dataset has not been generated yet, which is an ordering mistake
// and not a broken configuration. The bare stat failure underneath is a Windows
// syscall name and says nothing about what to run.
func TestDatasetNotGeneratedYet(t *testing.T) {
	err := DatasetProblem("support-agent-eval",
		DatasetNotGeneratedYet("support-agent-eval",
			filepath.Join("evals", "datasets", "support-agent-eval.jsonl")))

	got := err.Error()
	assert.Contains(t, got, "evals/datasets/support-agent-eval.jsonl")
	assert.NotContains(t, got, `\\`)
	assert.NotContains(t, got, "GetFileAttributesEx")
	assert.Contains(t, got, "azd ai eval generate --dataset --dataset-name support-agent-eval")
	assert.Equal(t, 1, countOccurrences(got, `"support-agent-eval"`),
		"the wrapper names the dataset, so the message must not name it again")
}

func countLines(s string) int {
	n := 0
	for _, r := range s {
		if r == '\n' {
			n++
		}
	}
	return n
}
