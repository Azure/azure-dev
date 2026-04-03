// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- currentAzdSemver Tests ---

func Test_CurrentAzdSemver_DevVersion(t *testing.T) {
	// Default dev build returns nil
	v := currentAzdSemver()
	assert.Nil(t, v, "dev build should return nil")
}

func Test_CurrentAzdSemver_ReleaseVersion(t *testing.T) {
	old := internal.Version
	internal.Version = "1.24.3 (commit aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa)"
	defer func() { internal.Version = old }()

	v := currentAzdSemver()
	require.NotNil(t, v)
	assert.Equal(t, uint64(1), v.Major())
	assert.Equal(t, uint64(24), v.Minor())
	assert.Equal(t, uint64(3), v.Patch())
	assert.Equal(t, "", v.Prerelease())
}

func Test_CurrentAzdSemver_PrereleaseStripped(t *testing.T) {
	old := internal.Version
	internal.Version = "1.25.0-beta.1-pr.12345 (commit bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb)"
	defer func() { internal.Version = old }()

	v := currentAzdSemver()
	require.NotNil(t, v)
	// Prerelease tag should be stripped
	assert.Equal(t, "", v.Prerelease())
	assert.Equal(t, uint64(1), v.Major())
	assert.Equal(t, uint64(25), v.Minor())
	assert.Equal(t, uint64(0), v.Patch())
}

// --- selectDistinctExtension Tests ---

func Test_SelectDistinctExtension_NoMatches(t *testing.T) {
	t.Parallel()
	_, err := selectDistinctExtension(
		context.Background(),
		mockinput.NewMockConsole(),
		"test-ext",
		[]*extensions.ExtensionMetadata{},
		&internal.GlobalCommandOptions{},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no extensions found")
}

func Test_SelectDistinctExtension_SingleMatch(t *testing.T) {
	t.Parallel()
	meta := &extensions.ExtensionMetadata{Source: "registry"}
	result, err := selectDistinctExtension(
		context.Background(),
		mockinput.NewMockConsole(),
		"test-ext",
		[]*extensions.ExtensionMetadata{meta},
		&internal.GlobalCommandOptions{},
	)
	require.NoError(t, err)
	assert.Equal(t, meta, result)
}

func Test_SelectDistinctExtension_MultipleNoPrompt(t *testing.T) {
	t.Parallel()
	meta1 := &extensions.ExtensionMetadata{Source: "registry1"}
	meta2 := &extensions.ExtensionMetadata{Source: "registry2"}
	_, err := selectDistinctExtension(
		context.Background(),
		mockinput.NewMockConsole(),
		"test-ext",
		[]*extensions.ExtensionMetadata{meta1, meta2},
		&internal.GlobalCommandOptions{NoPrompt: true},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple sources")
}

// --- namespacesConflict Tests (additional paths) ---

func Test_NamespacesConflict_SameNamespace(t *testing.T) {
	t.Parallel()
	conflict, _ := namespacesConflict("ai", "ai")
	assert.True(t, conflict)
}

func Test_NamespacesConflict_CaseInsensitive(t *testing.T) {
	t.Parallel()
	conflict, _ := namespacesConflict("AI", "ai")
	assert.True(t, conflict)
}

func Test_NamespacesConflict_PrefixConflict(t *testing.T) {
	t.Parallel()
	conflict, reason := namespacesConflict("ai", "ai.agent")
	assert.True(t, conflict)
	assert.Equal(t, "overlapping namespaces", reason)
}

func Test_NamespacesConflict_ReversePrefixConflict(t *testing.T) {
	t.Parallel()
	conflict, reason := namespacesConflict("ai.agent", "ai")
	assert.True(t, conflict)
	assert.Equal(t, "overlapping namespaces", reason)
}

func Test_NamespacesConflict_NoConflict(t *testing.T) {
	t.Parallel()
	conflict, reason := namespacesConflict("ai", "ml")
	assert.False(t, conflict)
	assert.Equal(t, "", reason)
}

// --- checkNamespaceConflict Tests (additional paths) ---

func Test_CheckNamespaceConflict_EmptyNamespace(t *testing.T) {
	t.Parallel()
	err := checkNamespaceConflict("new-ext", "", map[string]*extensions.Extension{})
	assert.NoError(t, err)
}

func Test_CheckNamespaceConflict_SkipsSelf(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"my-ext": {Namespace: "demo"},
	}
	// Same ID should be skipped (upgrade scenario)
	err := checkNamespaceConflict("my-ext", "demo", installed)
	assert.NoError(t, err)
}

func Test_CheckNamespaceConflict_SkipsEmptyInstalledNamespace(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"other-ext": {Namespace: ""},
	}
	err := checkNamespaceConflict("new-ext", "demo", installed)
	assert.NoError(t, err)
}

func Test_CheckNamespaceConflict_DetectsConflict(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"other-ext": {Namespace: "demo"},
	}
	err := checkNamespaceConflict("new-ext", "demo", installed)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicts with installed extension")
}
