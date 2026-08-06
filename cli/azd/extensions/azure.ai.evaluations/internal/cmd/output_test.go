// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The list commands are backed by two different services whose envelopes
// disagree — `data` on one side, `value` on the other. Emitting whichever one
// came back would make a caller's parsing depend on that accident, so every
// list emits a bare array instead.
func TestEmitJSONList_EmitsAnArrayNotAnEnvelope(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, emitJSONList(&buf, []string{"a", "b"}))
	assert.Equal(t, "[\n  \"a\",\n  \"b\"\n]\n", buf.String())
}

// A nil slice marshals to `null`, which a caller iterating the output cannot
// range over. An empty listing has to come back as an empty array.
func TestEmitJSONList_NilBecomesEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	var none []string
	require.NoError(t, emitJSONList(&buf, none))
	assert.Equal(t, "[]\n", buf.String())
}
