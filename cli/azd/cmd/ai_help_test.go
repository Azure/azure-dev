// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/braydonk/yaml"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func newAIHelpTestRoot(t *testing.T) *cobra.Command {
	t.Helper()
	root := newTestRoot()
	manifests, err := filepath.Glob(filepath.Join("..", "extensions", "*", "extension.yaml"))
	require.NoError(t, err)
	count := 0
	for _, manifest := range manifests {
		data, err := os.ReadFile(manifest)
		require.NoError(t, err)
		var entry struct {
			ID          string `yaml:"id"`
			Namespace   string `yaml:"namespace"`
			Description string `yaml:"description"`
		}
		require.NoError(t, yaml.Unmarshal(data, &entry))
		if !strings.HasPrefix(entry.Namespace, "ai.") {
			continue
		}
		require.NoError(t, bindExtension(root, &extensions.Extension{
			Id: entry.ID, Namespace: entry.Namespace, Description: entry.Description,
		}))
		count++
	}
	require.Equal(t, 14, count, "update help coverage when AI extensions are added")
	require.NoError(t, bindExtension(root, &extensions.Extension{
		Id: "test.deep", Namespace: "ai.nested.example", Description: "Nested test extension.",
	}))
	require.NoError(t, bindExtension(root, &extensions.Extension{
		Id: "test.other", Namespace: "other.example", Description: "Unrelated extension.",
	}))
	command, err := NewCobraBuilder(ioc.NewNestedContainer(nil)).BuildCommand(root)
	require.NoError(t, err)
	command.CompletionOptions.DisableDefaultCmd = true
	return command
}

func TestAIHelp(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	previous := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = previous })
	for _, path := range [][]string{{"ai"}, {"ai", "nested"}} {
		t.Run(strings.Join(path, "_"), func(t *testing.T) {
			root := newAIHelpTestRoot(t)
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			root.SetArgs(append(path, "--help"))
			require.NoError(t, root.Execute())
			text := buf.String()
			require.Contains(t, text, "\nUsage\n  azd "+strings.Join(path, " "))
			require.Contains(t, text, "\nExamples\n")
			require.NotContains(t, text, "\x1b")
			require.NotContains(t, text, "\nUsage:")
			if len(path) == 1 {
				require.Contains(t, text, "Environments & Configuration\n")
			} else {
				require.NotContains(t, text, "Environments & Configuration")
			}
			snapshot.SnapshotT(t, strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n")))
		})
	}
}

func TestAIHelpColorAndIsolation(t *testing.T) {
	previous := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = previous })
	root := newAIHelpTestRoot(t)
	ai, _, err := root.Find([]string{"ai"})
	require.NoError(t, err)
	other, _, err := root.Find([]string{"other"})
	require.NoError(t, err)
	require.NotEqual(t, ai.HelpTemplate(), other.HelpTemplate())
	require.NotEqual(t, ai.HelpTemplate(), root.HelpTemplate())
	color.NoColor = false
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"ai", "--help"})
	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), output.WithBold("%s", output.WithUnderline("Usage")))
	require.Contains(t, buf.String(), output.WithHighLightFormat("%s", "--help"))
}
