// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"azure.ai.toolboxes/internal/definition"
	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/projectctx"

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

func TestCreateEnsuresDeveloperAccessForActiveEnvironmentProject(t *testing.T) {
	// No t.Parallel: package-level seams are swapped.
	previousReadSources := projectctx.ReadAzdHostedSourcesFunc
	projectctx.ReadAzdHostedSourcesFunc = func(context.Context) (projectctx.AzdHostedSources, error) {
		return projectctx.AzdHostedSources{
			EnvValue: "https://project.services.ai.azure.com/api/projects/test", EnvName: "test",
		}, nil
	}
	t.Cleanup(func() { projectctx.ReadAzdHostedSourcesFunc = previousReadSources })

	sentinel := errors.New("access check reached")
	var calls int
	previousEnsure := ensureDeveloperAccessFunc
	ensureDeveloperAccessFunc = func(context.Context) error {
		calls++
		return sentinel
	}
	t.Cleanup(func() { ensureDeveloperAccessFunc = previousEnsure })

	err := runToolboxCreate(
		t.Context(), "research", toolboxCreateFlags{}, toolboxFlags{output: "json"}, nil,
	)

	assert.Equal(t, 1, calls)
	require.ErrorIs(t, err, sentinel)
}
