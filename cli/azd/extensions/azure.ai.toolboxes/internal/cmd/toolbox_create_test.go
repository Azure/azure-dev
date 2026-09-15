// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"path/filepath"
	"testing"

	"azure.ai.toolboxes/internal/definition"
	"azure.ai.toolboxes/internal/exterrors"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildToolboxVersionRequestAllowsSkillsOnly(t *testing.T) {
	request, err := buildToolboxVersionRequest(
		t.Context(), newStubConnectionResolver(), "https://example.test",
		&definition.Definition{Skills: []definition.SkillReference{{Name: "triage"}}},
	)
	require.NoError(t, err)
	assert.Empty(t, request.Tools)
	assert.Len(t, request.Skills, 1)
}

func TestCreateRejectsDefinitionNameMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), definition.DefaultPath)
	require.NoError(t, definition.Save(path, &definition.Definition{
		Name:  "from-file",
		Tools: []map[string]any{{"type": "web_search"}},
	}))

	err := runToolboxCreateWith(
		t.Context(), newMockToolboxClient("https://example.test"),
		newStubConnectionResolver(), "https://example.test", "from-command",
		toolboxCreateFlags{fromFile: path}, toolboxFlags{output: "json"},
	)
	requireLocalError(t, err, exterrors.CodeInvalidToolboxName)
}
