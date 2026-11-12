// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A registered dataset is referenced, not copied. Inline rows lose the version
// binding and lineage, and the portal reports "Inline data" for a run the
// author pointed at a catalog dataset. ADO 5631468.
func TestSetFileID_ReferencesTheRegisteredDataset(t *testing.T) {
	t.Parallel()

	ds := NewAgentTargetDataSource("hero-agent", nil)
	ds.SetFileID("azureai://accounts/acct/projects/proj/data/support-golden/versions/1.0")

	require.NotNil(t, ds.Source)
	assert.Equal(t, EvalRunDataContentTypeFileID, ds.Source.Type)
	assert.Equal(t, "azureai://accounts/acct/projects/proj/data/support-golden/versions/1.0", ds.Source.ID)
	assert.Empty(t, ds.Source.Content, "a referenced dataset carries no copied rows")
}

// The wire shape is what the service validates: a file_id source sends an id
// and no content, and a file_content source sends content and no id.
func TestSetFileID_WireShape(t *testing.T) {
	t.Parallel()

	ds := NewAgentTargetDataSource("hero-agent", nil)
	ds.SetFileID("azureai://accounts/acct/data/d/versions/2")

	body, err := json.Marshal(ds)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))

	source, ok := decoded["source"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "file_id", source["type"])
	assert.Equal(t, "azureai://accounts/acct/data/d/versions/2", source["id"])
	_, hasContent := source["content"]
	assert.False(t, hasContent, "content is omitted when the dataset is referenced")
}

func TestSetFileContent_WireShapeIsUnchanged(t *testing.T) {
	t.Parallel()

	ds := NewAgentTargetDataSource("hero-agent", nil)
	ds.SetFileContent([]map[string]any{{"query": "where is my order?"}})

	body, err := json.Marshal(ds)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))

	source, ok := decoded["source"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "file_content", source["type"])
	_, hasID := source["id"]
	assert.False(t, hasID, "an inline source carries no id")
	assert.Len(t, source["content"], 1)
}

// Setting one replaces the other outright, so a data source cannot end up
// claiming to be both a reference and a copy.
func TestSetFileID_ReplacesInlineContent(t *testing.T) {
	t.Parallel()

	ds := NewAgentTargetDataSource("hero-agent", nil)
	ds.SetFileContent([]map[string]any{{"query": "q"}})
	ds.SetFileID("azureai://accounts/acct/data/d/versions/1")

	assert.Equal(t, EvalRunDataContentTypeFileID, ds.Source.Type)
	assert.Empty(t, ds.Source.Content)

	ds.SetFileContent([]map[string]any{{"query": "q"}})
	assert.Equal(t, EvalRunDataContentTypeFileContent, ds.Source.Type)
	assert.Empty(t, ds.Source.ID)
}
