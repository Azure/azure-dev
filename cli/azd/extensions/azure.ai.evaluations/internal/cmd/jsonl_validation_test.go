// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeJSONL(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "d.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// The service accepts whatever bytes it is given, so a malformed row becomes a
// published version with an eval bound to it, and only fails much later
// on a row nobody has looked at. A live deploy published `{not json at all}`
// as version 1.0 before this existed.
func TestValidateJSONL_RejectsAMalformedRowByLine(t *testing.T) {
	err := validateJSONL(writeJSONL(t, "{\"query\":\"fine\"}\n{not json at all}\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 2")
	assert.Contains(t, err.Error(), "one JSON object")
}

func TestValidateJSONL_AcceptsWellFormedRows(t *testing.T) {
	assert.NoError(t, validateJSONL(writeJSONL(t,
		"{\"query\":\"a\"}\n{\"query\":\"b\"}\n")))
}

// Trailing and interior blank lines are formatting, not rows.
func TestValidateJSONL_IgnoresBlankLines(t *testing.T) {
	assert.NoError(t, validateJSONL(writeJSONL(t,
		"{\"query\":\"a\"}\n\n{\"query\":\"b\"}\n\n")))
}

// A file with nothing in it publishes a version that can never score anything.
func TestValidateJSONL_RejectsAFileWithNoRows(t *testing.T) {
	err := validateJSONL(writeJSONL(t, "\n\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no rows")
}

// A JSON array is the shape people reach for when they mean JSONL.
func TestValidateJSONL_RejectsAJSONArray(t *testing.T) {
	err := validateJSONL(writeJSONL(t, "[{\"query\":\"a\"},{\"query\":\"b\"}]\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 1")
}

// An empty object parses but evaluates to nothing.
func TestValidateJSONL_RejectsAnEmptyObject(t *testing.T) {
	err := validateJSONL(writeJSONL(t, "{\"query\":\"a\"}\n{}\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 2")
	assert.Contains(t, err.Error(), "empty object")
}

// A conversation-level row holds a whole transcript and runs past bufio's
// default 64KB line limit, which would otherwise be reported as invalid JSON.
func TestValidateJSONL_AcceptsAVeryLongRow(t *testing.T) {
	long := make([]byte, 200*1024)
	for i := range long {
		long[i] = 'x'
	}
	assert.NoError(t, validateJSONL(writeJSONL(t,
		"{\"query\":\""+string(long)+"\"}\n")))
}
