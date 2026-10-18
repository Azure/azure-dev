// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestFindExistingAgentYaml(t *testing.T) {
	t.Parallel()

	t.Run("empty dir returns no match", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		got, err := findExistingAgentYaml(dir)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("finds agent.yaml", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeReuseTestFile(t, dir, "agent.yaml", "kind: hosted\nname: foo\n")

		got, err := findExistingAgentYaml(dir)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dir, "agent.yaml"), got)
	})

	t.Run("agent.manifest.yaml takes priority over agent.yaml", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeReuseTestFile(t, dir, "agent.yaml", "kind: hosted\nname: foo\n")
		writeReuseTestFile(t, dir, "agent.manifest.yaml", "template:\n  kind: hosted\n  name: foo\n")

		got, err := findExistingAgentYaml(dir)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dir, "agent.manifest.yaml"), got)
	})

	t.Run("directory entries are ignored", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "agent.yaml"), 0o750))

		got, err := findExistingAgentYaml(dir)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("scan does not recurse into subdirectories", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		nested := filepath.Join(dir, "src")
		require.NoError(t, os.Mkdir(nested, 0o750))
		writeReuseTestFile(t, nested, "agent.yaml", "kind: hosted\nname: foo\n")

		got, err := findExistingAgentYaml(dir)
		require.NoError(t, err)
		require.Empty(t, got, "shallow scan only; nested agent.yaml must be ignored")
	})

	t.Run("content is not parsed", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := writeReuseTestFile(t, dir, "agent.yaml", "name: : :\nmodel: [unterminated\n")

		got, err := findExistingAgentYaml(dir)
		require.NoError(t, err)
		require.Equal(t, path, got)
	})
}

func TestLegacyInitSourceError(t *testing.T) {
	t.Parallel()

	err := legacyInitSourceError(filepath.Join("project", "agent.manifest.yaml"))
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Contains(t, localErr.Message, "agent.manifest.yaml")
	require.Contains(t, localErr.Suggestion, "azure.yaml")
}

func TestValidateExplicitInitSource(t *testing.T) {
	t.Run("matching valid service allows stale legacy file", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "src", "chat")
		require.NoError(t, os.MkdirAll(source, 0o750))
		writeReuseTestFile(t, source, "agent.yaml", "not: [valid")
		service := inlineAgentService(t, "chat", "chat-agent")
		service.RelativePath = "src/chat"
		client := newHelpersTestAzdClient(t,
			&helpersProjectServer{project: &azdext.ProjectConfig{
				Path: root,
				Services: map[string]*azdext.ServiceConfig{
					"chat": service,
				},
			}},
			&helpersPromptServer{},
		)

		require.NoError(t, validateExplicitInitSource(t.Context(), client, source))
	})

	t.Run("unmatched legacy subdirectory is rejected", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "src", "other")
		require.NoError(t, os.MkdirAll(source, 0o750))
		writeReuseTestFile(t, source, "agent.manifest.yaml", "not: [valid")
		service := inlineAgentService(t, "chat", "chat-agent")
		service.RelativePath = "src/chat"
		client := newHelpersTestAzdClient(t,
			&helpersProjectServer{project: &azdext.ProjectConfig{
				Path: root,
				Services: map[string]*azdext.ServiceConfig{
					"chat": service,
				},
			}},
			&helpersPromptServer{},
		)

		err := validateExplicitInitSource(t.Context(), client, source)
		require.ErrorContains(t, err, "agent.manifest.yaml")
	})

	t.Run("project without agent service can add clean source", func(t *testing.T) {
		source := t.TempDir()
		client := newHelpersTestAzdClient(t,
			&helpersProjectServer{project: &azdext.ProjectConfig{
				Path: source,
				Services: map[string]*azdext.ServiceConfig{
					"web": {Name: "web", Host: "containerapp"},
				},
			}},
			&helpersPromptServer{},
		)

		require.NoError(t, validateExplicitInitSource(t.Context(), client, source))
	})

	t.Run("invalid configured service blocks clean source", func(t *testing.T) {
		root := t.TempDir()
		client := newHelpersTestAzdClient(t,
			&helpersProjectServer{project: &azdext.ProjectConfig{
				Path: root,
				Services: map[string]*azdext.ServiceConfig{
					"agent": {Name: "agent", Host: AiAgentHost},
				},
			}},
			&helpersPromptServer{},
		)

		err := validateExplicitInitSource(t.Context(), client, filepath.Join(root, "new-code"))
		require.ErrorContains(t, err, "agent definition not found")
	})
}

func writeReuseTestFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}
