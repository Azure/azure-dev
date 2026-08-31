// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"path/filepath"
	"testing"

	"azure.ai.toolboxes/internal/definition"
	"azure.ai.toolboxes/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootContainsVerbFirstAddCommands(t *testing.T) {
	t.Parallel()

	root := NewRootCommand()
	command, _, err := root.Find([]string{"add", "skill"})
	require.NoError(t, err)
	assert.Equal(t, "skill", command.Name())

	command, _, err = root.Find([]string{"add", "connection"})
	require.NoError(t, err)
	assert.Equal(t, "connection", command.Name())
}

func TestRunLocalSkillAddUpdatesDefinition(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), definition.DefaultPath)
	require.NoError(t, definition.Save(path, &definition.Definition{Name: "support-tools"}))

	err := runLocalSkillAdd(
		"triage@2",
		localAddFlags{file: path},
		toolboxFlags{output: "json"},
	)
	require.NoError(t, err)

	got, err := definition.Load(path)
	require.NoError(t, err)
	require.Equal(t, []definition.SkillReference{{Name: "triage", Version: "2"}}, got.Skills)
}

func TestRunLocalConnectionAddUpdatesDefinition(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), definition.DefaultPath)
	require.NoError(t, definition.Save(path, &definition.Definition{Name: "support-tools"}))

	err := runLocalConnectionAdd(
		"search",
		localConnectionAddFlags{
			localAddFlags: localAddFlags{file: path},
			index:         "tickets",
		},
		toolboxFlags{output: "json"},
	)
	require.NoError(t, err)

	got, err := definition.Load(path)
	require.NoError(t, err)
	require.Equal(t, []definition.ConnectionReference{{Name: "search", Index: "tickets"}}, got.Connections)
}

func TestRunLocalAddRejectsDuplicates(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), definition.DefaultPath)
	require.NoError(t, definition.Save(path, &definition.Definition{
		Skills:      []definition.SkillReference{{Name: "triage"}},
		Connections: []definition.ConnectionReference{{Name: "search"}},
	}))

	err := runLocalSkillAdd(
		"triage",
		localAddFlags{file: path},
		toolboxFlags{output: "json"},
	)
	requireLocalError(t, err, exterrors.CodeSkillAlreadyAttached)

	err = runLocalConnectionAdd(
		"search",
		localConnectionAddFlags{localAddFlags: localAddFlags{file: path}},
		toolboxFlags{output: "json"},
	)
	requireLocalError(t, err, exterrors.CodeDuplicateConnection)
}

func TestRunLocalAddDoesNotRequireProjectEndpoint(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), definition.DefaultPath)
	require.NoError(t, definition.Save(path, &definition.Definition{}))

	err := runLocalSkillAdd(
		"triage",
		localAddFlags{file: path},
		toolboxFlags{projectEndpoint: "not-a-url", output: "json"},
	)
	require.NoError(t, err)
}

func TestRunLocalAddMissingDefinition(t *testing.T) {
	t.Parallel()

	err := runLocalSkillAdd(
		"triage",
		localAddFlags{file: filepath.Join(t.TempDir(), definition.DefaultPath)},
		toolboxFlags{output: "json"},
	)

	localErr := requireLocalError(t, err, exterrors.CodeInvalidParameter)
	assert.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
}
