// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// skills.go is the parent command for `azd ai doc skills ...`. Today it
// hosts a single subcommand (install), but the design keeps "skills" as
// its own subtree so future verbs (list, uninstall, update) slot in
// without resurfacing the install ergonomics.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newSkillsCommand returns the `azd ai doc skills` parent command. The
// parent has no RunE -- it just hangs the install subcommand off the
// tree. Help text doubles as the index of available verbs.
func newSkillsCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills <command> [options]",
		Short: "Install agent-friendly skill packs into your project.",
		Long: `Install agent-friendly skill packs (SKILL.md and supporting files)
into your project so a coding agent (Claude Code, Codex, Gemini CLI,
GitHub Copilot, Opencode, or a custom integration) can follow them.

Skill packs are read from this extension's embedded content; installing
copies them into a tool-specific path under the current project (e.g.
.claude/skills/azd-ai-skill/ for Claude Code).`,
		Example: `  # Install the AZD AI skill for GitHub Copilot
  azd ai doc skills install --target copilot

  # Install for Claude Code (writes .claude/skills/azd-ai-skill/)
  azd ai doc skills install --target claude

  # Install to a custom directory
  azd ai doc skills install --target custom --path .my-tool/skills/azd-ai`,
	}

	cmd.AddCommand(newSkillInstallCommand(extCtx))

	return cmd
}
