// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// install.go is the parent command for `azd ai doc install ...`. Today it
// hosts a single child (`skill`, which installs the embedded azd-ai-skill
// coding-agent pack into the user's project), but the design keeps
// "install" as its own subtree so future installable artifacts (e.g.
// `install hooks`, `install workflows`) slot in without resurfacing the
// install ergonomics.
//
// Note: the embedded pack installed by `azd ai doc install skill` is the
// coding-agent skill consumed by tools like Claude Code / GitHub Copilot.
// It is intentionally distinct from the Foundry Skill resource managed by
// the `azure.ai.skills` extension (see `azd ai doc skill` for those
// docs).

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newInstallCommand returns the `azd ai doc install` parent command. The
// parent has no RunE -- it just hangs the skill subcommand off the tree.
// Help text doubles as the index of available verbs.
func newInstallCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install <command> [options]",
		Short: "Install agent-friendly packs into your project.",
		Long: `Install agent-friendly packs into your project so a coding agent
(Claude Code, Codex, Gemini CLI, GitHub Copilot, Opencode, or a custom
integration) can follow them.

Packs are read from this extension's embedded content; installing copies
them into a tool-specific path under the current project (e.g.
.claude/skills/azd-ai-skill/ for Claude Code).`,
		Example: `  # Install the AZD AI skill pack for GitHub Copilot
  azd ai doc install skill --target copilot

  # Install for Claude Code (writes .claude/skills/azd-ai-skill/)
  azd ai doc install skill --target claude

  # Install to a custom directory
  azd ai doc install skill --target custom --path .my-tool/skills/azd-ai`,
	}

	cmd.AddCommand(newInstallSkillCommand(extCtx))

	return cmd
}
