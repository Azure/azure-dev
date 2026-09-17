// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// JSON null is the one non-object the decoder accepts without an error: it
// leaves a nil map behind, so the discriminator assignment that follows used to
// panic with "assignment to entry in nil map" rather than refuse the file. A
// stack trace on a malformed evaluator reads as a broken CLI, not a bad file.
func TestANullDefinitionIsRefusedRatherThanPanicking(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"definition written as null", `{"name":"x","definition":null}`},
		{"the whole document is null", `null`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := normalizeRubricBody("x", []byte(tc.body))
				assert.Error(t, err, "a null definition is not something to publish")
				assert.Contains(t, err.Error(), "null",
					"the message has to name what was wrong with the file")
			})
		})
	}
}

// The shapes the decoder already rejected keep their own errors, so the guard
// above did not swallow them into one message.
func TestNonObjectDefinitionsKeepTheirOwnError(t *testing.T) {
	for _, body := range []string{
		`{"name":"x","definition":5}`,
		`{"name":"x","definition":[]}`,
		`{"name":"x","definition":"text"}`,
	} {
		_, err := normalizeRubricBody("x", []byte(body))
		assert.Error(t, err, "%s is not an object", body)
		assert.NotContains(t, err.Error(), "is null",
			"only null decodes silently; the rest carry the decoder's own reason")
	}
}

// A definition that is an object still round-trips and gains the discriminator.
func TestAnObjectDefinitionStillGainsItsType(t *testing.T) {
	out, err := normalizeRubricBody("x", []byte(`{"name":"ignored","definition":{"dimensions":[]}}`))
	require.NoError(t, err)

	var doc struct {
		Name       string `json:"name"`
		Definition struct {
			Type string `json:"type"`
		} `json:"definition"`
	}
	require.NoError(t, json.Unmarshal(out, &doc))
	assert.Equal(t, "x", doc.Name, "the argument names the evaluator, not the file")
	assert.Equal(t, rubricDefinitionType, doc.Definition.Type)
}
