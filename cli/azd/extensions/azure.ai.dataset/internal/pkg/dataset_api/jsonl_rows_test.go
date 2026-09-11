// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The service stores whatever bytes it is given, so a malformed row registers a
// version that looks healthy and only fails in the run that reads it, against a
// line number nobody has any more. It is refused here instead.
func TestJSONLContentRefusesAMalformedRow(t *testing.T) {
	good := "{\"query\":\"a\"}\n{\"query\":\"b\"}\n"
	_, err := jsonlContent("ds.jsonl", []byte(good))
	require.NoError(t, err)

	_, err = jsonlContent("ds.jsonl", []byte("{\"query\":\"a\"}\nnot json\n{\"query\":\"c\"}\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 2", "the failing row is named by line")
	assert.Contains(t, err.Error(), "ds.jsonl")

	_, err = jsonlContent("ds.jsonl", []byte("{\"query\":\"a\"}\n{}\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 2")

	// A blank line between records is formatting, not a row.
	_, err = jsonlContent("ds.jsonl", []byte("{\"query\":\"a\"}\n\n{\"query\":\"b\"}\n"))
	assert.NoError(t, err)
}
