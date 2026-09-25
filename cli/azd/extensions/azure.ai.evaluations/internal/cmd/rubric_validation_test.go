// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRubricValidationPreservesValidAuthoredBytes(t *testing.T) {
	for _, body := range []string{
		`{"type":"rubric","dimensions":[{"id":"clarity","description":"Clear answer"}]}`,
		`{"type":"rubric","dimensions":[{"id":"clarity","weight":1}],"pass_threshold":0}`,
		`{"type":"rubric","dimensions":[{"id":"clarity","weight":10}],"pass_threshold":1}`,
		`{"type":"rubric","dimensions":[{"id":"clarity","weight":5}],"pass_threshold":0.5}`,
		`{"type":"custom_kind","dimensions":[{"weight":0}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			got, err := validateRubricDefinition(json.RawMessage(body))
			require.NoError(t, err)
			assert.Equal(t, body, string(got), "validation must not alter fingerprint input")
		})
	}
}

func TestEvaluatorWriteRejectsInvalidWeightBeforeConnecting(t *testing.T) {
	for _, verb := range []string{"create", "update"} {
		t.Run(verb, func(t *testing.T) {
			path := writeEvaluatorFile(t, `{"dimensions":[{"id":"clarity","weight":0}]}`)
			cmd := newEvaluatorWriteCommand(verb, "")
			cmd.SetContext(t.Context())
			action := &evaluatorWriteAction{
				cmd: cmd, verb: verb, name: "quality",
				flags: &evaluatorWriteFlags{fromFile: path},
			}
			require.ErrorContains(t, action.Run(), "weight must be a whole number between 1 and 10")
		})
	}
}
