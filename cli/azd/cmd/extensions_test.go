// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"slices"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// findChildByName returns the child action descriptor with the given name, or nil if not found.
func findChildByName(parent *actions.ActionDescriptor, name string) *actions.ActionDescriptor {
	idx := slices.IndexFunc(parent.Children(), func(child *actions.ActionDescriptor) bool {
		return child.Name == name
	})
	if idx == -1 {
		return nil
	}
	return parent.Children()[idx]
}

// newTestRoot creates a new root action descriptor for testing.
func newTestRoot() *actions.ActionDescriptor {
	return actions.NewActionDescriptor("azd", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{Use: "azd", Short: "Azure Developer CLI"},
	})
}

func TestBindExtension_SharedNamespacePrefix(t *testing.T) {
	tests := []struct {
		name                      string
		extensions                []*extensions.Extension
		expectedIntermediateDesc  string
		expectedIntermediateNames []string
	}{
		{
			name: "two extensions share 'ai' prefix",
			extensions: []*extensions.Extension{
				{
					Id:          "azure.ai.agents",
					Namespace:   "ai.agents",
					DisplayName: "AI Agents Extension",
					Description: "Extension for the Foundry Agent Service. (Preview)",
				},
				{
					Id:          "azure.ai.finetune",
					Namespace:   "ai.finetune",
					DisplayName: "AI Fine Tune Extension",
					Description: "Extension for Foundry Fine Tuning. (Preview)",
				},
			},
			expectedIntermediateDesc:  "Commands for the ai extension namespace.",
			expectedIntermediateNames: []string{"ai"},
		},
		{
			name: "single extension with nested namespace",
			extensions: []*extensions.Extension{
				{
					Id:          "azure.ai.agents",
					Namespace:   "ai.agents",
					DisplayName: "AI Agents Extension",
					Description: "Extension for the Foundry Agent Service. (Preview)",
				},
			},
			expectedIntermediateDesc:  "Commands for the ai extension namespace.",
			expectedIntermediateNames: []string{"ai"},
		},
		{
			name: "extension with simple namespace (no intermediate)",
			extensions: []*extensions.Extension{
				{
					Id:          "microsoft.azd.demo",
					Namespace:   "demo",
					DisplayName: "Demo Extension",
					Description: "This extension provides examples of the AZD extension framework.",
				},
			},
			expectedIntermediateDesc:  "",
			expectedIntermediateNames: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newTestRoot()

			for _, ext := range tt.extensions {
				require.NoError(t, bindExtension(root, ext))
			}

			for _, intermediateName := range tt.expectedIntermediateNames {
				intermediateCmd := findChildByName(root, intermediateName)
				require.NotNil(t, intermediateCmd, "intermediate command %s should exist", intermediateName)
				require.Equal(t, tt.expectedIntermediateDesc, intermediateCmd.Options.Command.Short,
					"intermediate namespace command should have generic description")
			}
		})
	}
}

func TestBindExtension_DeterministicOrder(t *testing.T) {
	ext1 := &extensions.Extension{
		Id:          "azure.ai.agents",
		Namespace:   "ai.agents",
		DisplayName: "AI Agents Extension",
		Description: "Extension for the Foundry Agent Service. (Preview)",
	}

	ext2 := &extensions.Extension{
		Id:          "azure.ai.finetune",
		Namespace:   "ai.finetune",
		DisplayName: "AI Fine Tune Extension",
		Description: "Extension for Foundry Fine Tuning. (Preview)",
	}

	// Test order 1: agents first
	root1 := newTestRoot()
	require.NoError(t, bindExtension(root1, ext1))
	require.NoError(t, bindExtension(root1, ext2))

	// Test order 2: finetune first
	root2 := newTestRoot()
	require.NoError(t, bindExtension(root2, ext2))
	require.NoError(t, bindExtension(root2, ext1))

	aiCmd1 := findChildByName(root1, "ai")
	aiCmd2 := findChildByName(root2, "ai")

	require.NotNil(t, aiCmd1)
	require.NotNil(t, aiCmd2)
	require.Equal(t, aiCmd1.Options.Command.Short, aiCmd2.Options.Command.Short,
		"intermediate namespace description should be consistent regardless of binding order")
	require.Equal(t, "Commands for the ai extension namespace.", aiCmd1.Options.Command.Short)
}

func TestBindExtension_DeeplyNestedNamespace(t *testing.T) {
	ext1 := &extensions.Extension{
		Id:          "azure.ai.models.finetune",
		Namespace:   "ai.models.finetune",
		DisplayName: "AI Models Fine Tune",
		Description: "Extension for fine tuning AI models.",
	}

	ext2 := &extensions.Extension{
		Id:          "azure.ai.models.eval",
		Namespace:   "ai.models.eval",
		DisplayName: "AI Models Eval",
		Description: "Extension for evaluating AI models.",
	}

	root := newTestRoot()
	require.NoError(t, bindExtension(root, ext1))
	require.NoError(t, bindExtension(root, ext2))

	// Verify "ai" intermediate command
	aiCmd := findChildByName(root, "ai")
	require.NotNil(t, aiCmd)
	require.Equal(t, "Commands for the ai extension namespace.", aiCmd.Options.Command.Short)

	// Verify "models" intermediate command under "ai"
	modelsCmd := findChildByName(aiCmd, "models")
	require.NotNil(t, modelsCmd)
	require.Equal(t, "Commands for the ai.models extension namespace.", modelsCmd.Options.Command.Short)

	// Verify leaf commands exist and have correct descriptions
	finetuneCmd := findChildByName(modelsCmd, "finetune")
	evalCmd := findChildByName(modelsCmd, "eval")

	require.NotNil(t, finetuneCmd)
	require.NotNil(t, evalCmd)
	require.Equal(t, "Extension for fine tuning AI models.", finetuneCmd.Options.Command.Short)
	require.Equal(t, "Extension for evaluating AI models.", evalCmd.Options.Command.Short)
}

