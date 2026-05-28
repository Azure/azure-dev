// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newToolboxSkillCommand returns the `azd ai toolbox skill` parent.
func newToolboxSkillCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage skill references attached to a toolbox.",
		Long: `Manage skill references attached to a toolbox.

Each add/remove publishes a new immutable version; the toolbox's default
version is unchanged. Use 'azd ai toolbox update --default-version' to
promote a version.`,
	}
	cmd.AddCommand(newToolboxSkillAddCommand(extCtx))
	cmd.AddCommand(newToolboxSkillRemoveCommand(extCtx))
	cmd.AddCommand(newToolboxSkillListCommand(extCtx))
	return cmd
}

// findSkillEntry returns the index of the first entry in skills[] whose name
// matches, or -1 if absent.
func findSkillEntry(skills []map[string]any, name string) int {
	for i, s := range skills {
		if n, ok := s["name"].(string); ok && n == name {
			return i
		}
	}
	return -1
}

// filterOutSkill returns skills[] with the first matching entry stripped.
func filterOutSkill(skills []map[string]any, name string) (result []map[string]any, removed bool) {
	idx := findSkillEntry(skills, name)
	if idx < 0 {
		return skills, false
	}
	result = make([]map[string]any, 0, len(skills)-1)
	result = append(result, skills[:idx]...)
	result = append(result, skills[idx+1:]...)
	return result, true
}
