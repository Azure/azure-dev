// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jobWithWarning is a completed job the service qualified.
func jobWithWarning() *eval_api.GenerationJob {
	return &eval_api.GenerationJob{
		ID:       "evaluatorgen-1",
		Status:   "succeeded",
		Warnings: []eval_api.JobWarning{{Code: "input_quality"}},
	}
}

// jsonCmd is a command with the inherited -o flag the real tree carries.
func jsonCmd(t *testing.T, format string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().String("output", format, "")
	return cmd
}

// Under -o json, stdout carries one document and nothing else.
//
// `job show` narrates what it collected -- the artifact it wrote, and any
// warning the service returned -- straight to stdout, ahead of the document.
// The result did not parse, which is the whole point of asking for JSON.
func TestProseIsDroppedUnderJSON(t *testing.T) {
	var out bytes.Buffer

	assert.Equal(t, io.Discard, humanOut(jsonCmd(t, "json"), &out),
		"the document says the same things in fields, so the prose is dropped")

	human := jsonCmd(t, "")
	assert.Equal(t, io.Writer(&out), humanOut(human, &out),
		"and a human run still gets it")
}

// The guard has to hold for the writers that narrate, not only in principle.
func TestACollectedArtifactSaysNothingUnderJSON(t *testing.T) {
	var out bytes.Buffer
	w := humanOut(jsonCmd(t, "json"), &out)

	writeJobWarnings(w, "evaluator",
		jobWithWarning(), "evals/evaluators/support.json")

	require.Empty(t, out.String(),
		"a warning printed here lands ahead of the document and stops it parsing")

	// And the document that follows is still one readable object.
	require.NoError(t, emitJSON(&out, map[string]any{"job": "evaluatorgen-1"}))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
	assert.Equal(t, "evaluatorgen-1", doc["job"])
}
