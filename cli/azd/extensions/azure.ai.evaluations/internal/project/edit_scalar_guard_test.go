// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The edit replaces value, tag and style but not kind or children, so writing
// over a mapping-valued entry left its children in place under a !!str tag.
// The command reported success and the next strict read refused the file.
func TestRewritingAMappingValuedEntryIsRefused(t *testing.T) {
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("name: rows\nfile:\n  path: ./a.jsonl\n"), &doc))
	mapping := doc.Content[0]

	changed, err := setMappingScalar(mapping, "file", "./b.jsonl")

	require.Error(t, err, "a mapping is not something this edit can rewrite")
	assert.False(t, changed)
	assert.Contains(t, err.Error(), "file")

	// And the document is untouched, so nothing was half-written.
	out, marshalErr := yaml.Marshal(&doc)
	require.NoError(t, marshalErr)
	assert.Contains(t, string(out), "path: ./a.jsonl")
	assert.NotContains(t, string(out), "./b.jsonl")
}

// A sequence is the same problem in a different shape.
func TestRewritingASequenceValuedEntryIsRefused(t *testing.T) {
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("name: rows\nfile:\n  - ./a.jsonl\n"), &doc))

	_, err := setMappingScalar(doc.Content[0], "file", "./b.jsonl")

	assert.Error(t, err)
}

// The ordinary path still works: a scalar is replaced, and replacing it with
// what it already says is not a change.
func TestRewritingAScalarStillWorks(t *testing.T) {
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("name: rows\nfile: ./a.jsonl\n"), &doc))
	mapping := doc.Content[0]

	changed, err := setMappingScalar(mapping, "file", "./b.jsonl")
	require.NoError(t, err)
	assert.True(t, changed)

	again, err := setMappingScalar(mapping, "file", "./b.jsonl")
	require.NoError(t, err)
	assert.False(t, again, "rewriting the file to change nothing would still rewrite it")

	out, err := yaml.Marshal(&doc)
	require.NoError(t, err)
	assert.Contains(t, string(out), "./b.jsonl")
}

// A key the entry does not carry is appended rather than refused.
func TestAMissingKeyIsAppended(t *testing.T) {
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("name: rows\n"), &doc))

	changed, err := setMappingScalar(doc.Content[0], "file", "./a.jsonl")
	require.NoError(t, err)
	assert.True(t, changed)

	out, err := yaml.Marshal(&doc)
	require.NoError(t, err)
	assert.Contains(t, string(out), "file: ./a.jsonl")
}
