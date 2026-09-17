// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The bare stat failure underneath this is a Windows syscall name and a path
// with doubled separators, which says nothing about what to do next. Both
// callers wrap it with EvaluatorProblem, so the evaluator is named once.
func TestEvaluatorNotGeneratedYet(t *testing.T) {
	err := EvaluatorProblem("support-agent-quality",
		EvaluatorNotGeneratedYet("support-agent-quality",
			filepath.Join("evals", "evaluators", "support-agent-quality.json")))

	got := err.Error()
	assert.Equal(t, 1, countOccurrences(got, `"support-agent-quality"`),
		"the wrapper names the evaluator, so the message must not name it again")
	assert.Contains(t, got, "evals/evaluators/support-agent-quality.json",
		"the path reads as a path, not as an escaped Windows literal")
	assert.NotContains(t, got, `\\`)
	assert.Contains(t, got, "azd ai eval generate --evaluator --evaluator-name support-agent-quality",
		"the way out is the command that writes the definition")
}

func countOccurrences(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}
