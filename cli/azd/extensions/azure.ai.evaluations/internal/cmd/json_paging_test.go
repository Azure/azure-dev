// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"strconv"
	"testing"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/dataset_api"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pagedCommand is a listing command with the flags a reader would pass.
func pagedCommand(t *testing.T, format string, args ...string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	var limit int
	var all bool
	cmd := &cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error { return nil }}
	addDisplayPagingFlags(cmd, &limit, &all, defaultPageSize)
	cmd.Flags().StringP("output", "o", "", "")

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	full := args
	if format != "" {
		full = append(append([]string{}, args...), "-o", format)
	}
	require.NoError(t, cmd.ParseFlags(full))
	return cmd, &buf
}

func datasetsNumbering(n int) *dataset_api.DatasetList {
	rows := make([]dataset_api.Dataset, 0, n)
	for i := range n {
		rows = append(rows, dataset_api.Dataset{Name: "ds-" + strconv.Itoa(i), Version: "1.0"})
	}
	return &dataset_api.DatasetList{Value: rows}
}

type listPage struct {
	Items             []map[string]any `json:"items"`
	Count             int              `json:"count"`
	TotalCount        *int             `json:"total_count"`
	ContinuationToken *string          `json:"continuation_token"`
}

// --limit means the same thing for a script as it does for a reader.
//
// It used to be ignored for `-o json`, so the flag did nothing on half the
// surface. It could not do otherwise: a bare array cannot say it is short, and
// a script that got one would read it as the whole collection.
func TestJSONListingsHonourLimit(t *testing.T) {
	cmd, out := pagedCommand(t, "json", "--limit", "3")
	require.NoError(t, renderDatasets(cmd, datasetsNumbering(25), messages.NoDatasets()))

	var page listPage
	require.NoError(t, json.Unmarshal(out.Bytes(), &page))
	assert.Len(t, page.Items, 3, "--limit bounds the rows a parser receives")
	assert.Equal(t, 3, page.Count)
	require.NotNil(t, page.TotalCount)
	assert.Equal(t, 25, *page.TotalCount,
		"and the envelope says how many there were, so the short list is not read as all of them")
	assert.Nil(t, page.ContinuationToken,
		"a listing walked in full has no cursor to resume from, and offering one would be a lie")
}

// --all is the reader saying they want the flood.
func TestJSONListingsHonourAll(t *testing.T) {
	cmd, out := pagedCommand(t, "json", "--all")
	require.NoError(t, renderDatasets(cmd, datasetsNumbering(25), messages.NoDatasets()))

	var page listPage
	require.NoError(t, json.Unmarshal(out.Bytes(), &page))
	assert.Len(t, page.Items, 25)
	assert.Equal(t, 25, page.Count)
}

// An empty listing is still something to range over, and still says so.
//
// A delete is checked for idempotence by listing what is left, and a caller
// piping into a parser needs a key rather than the sentence a human reads.
func TestJSONListingsAlwaysCarryItems(t *testing.T) {
	cmd, out := pagedCommand(t, "json")
	require.NoError(t, renderDatasets(cmd, &dataset_api.DatasetList{}, messages.NoDatasets()))

	var page listPage
	require.NoError(t, json.Unmarshal(out.Bytes(), &page))
	assert.NotNil(t, page.Items)
	assert.Empty(t, page.Items)
	assert.Equal(t, 0, page.Count)
	assert.NotContains(t, out.String(), "No datasets found",
		"the sentence is for a reader, not for a parser")
}

// total_count is absent rather than zero where the service does not report one:
// "none" and "not said" are different answers, and a script reading 0 for the
// second concludes the collection is empty.
func TestPageEnvelopeOmitsAnUnknownTotal(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, emitJSONPage(&buf, []string{"a"}, nil, "cursor-1"))

	assert.NotContains(t, buf.String(), "total_count")
	assert.Contains(t, buf.String(), `"continuation_token": "cursor-1"`)

	var page struct {
		Items             []string `json:"items"`
		TotalCount        *int     `json:"total_count"`
		ContinuationToken *string  `json:"continuation_token"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &page))
	assert.Nil(t, page.TotalCount)
	require.NotNil(t, page.ContinuationToken)
	assert.Equal(t, "cursor-1", *page.ContinuationToken)
}
