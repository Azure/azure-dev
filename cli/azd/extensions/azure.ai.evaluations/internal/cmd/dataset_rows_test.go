// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadJSONLBytes(t *testing.T) {
	content := []byte(
		"{\"query\":\"a\"}\n" +
			"\n" + // blank lines are skipped, not treated as rows
			"{\"query\":\"b\"}\n" +
			"  {\"query\":\"c\"}  \n")

	all, err := readJSONLBytes(content, 0)
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, "a", all[0]["query"])
	assert.Equal(t, "c", all[2]["query"], "surrounding whitespace is not part of the row")
}

// The limit is what makes --max-samples mean the same thing for a published
// dataset as for a local file.
func TestReadJSONLBytes_StopsAtTheLimit(t *testing.T) {
	content := []byte("{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n")

	two, err := readJSONLBytes(content, 2)
	require.NoError(t, err)
	require.Len(t, two, 2)
	assert.EqualValues(t, 1, two[0]["n"])
	assert.EqualValues(t, 2, two[1]["n"])

	// A limit larger than the file is not an error.
	more, err := readJSONLBytes(content, 99)
	require.NoError(t, err)
	assert.Len(t, more, 3)
}

func TestReadJSONLBytes_ReportsTheOffendingLine(t *testing.T) {
	_, err := readJSONLBytes([]byte("{\"n\":1}\nnot json\n"), 0)
	require.ErrorContains(t, err, "line 2")
}

func TestReadJSONLBytes_EmptyIsNotAnError(t *testing.T) {
	items, err := readJSONLBytes([]byte("\n\n"), 0)
	require.NoError(t, err)
	assert.Empty(t, items, "the caller decides whether no rows is a problem")
}
