// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azure.ai.docs/internal/helpformat"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:  "doc",
		Use:   "doc <command> [options]",
		Short: "Agent-ready documentation for the Foundry azd extensions. (Preview)",
		// Long is intentionally empty: the styled Description function
		// passed via helpformat.Install below drives the --help
		// preamble (matching what runDocIndex prints for direct
		// invocation). Keeping Long set would either duplicate or
		// conflict with the catalog renderer's output.
	})

	// The root command itself renders the top-level index when invoked
	// with no subcommand. Matches a familiar `skills` catalog shape so
	// agents can discover available docs without first knowing a verb.
	rootCmd.Args = cobra.NoArgs
	rootCmd.RunE = runDocIndex

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions = cobra.CompletionOptions{
		DisableDefaultCmd: true,
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(newAgentCommand())
	rootCmd.AddCommand(newConnectionCommand())
	rootCmd.AddCommand(newToolboxCommand())
	rootCmd.AddCommand(newSkillsCommand(extCtx))
	rootCmd.AddCommand(newVersionCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newMetadataCommand(rootCmd))

	// Wire the catalog renderer into root --help via Description+Footer
	// so `azd ai doc --help` shows the same body+examples runDocIndex
	// emits for direct invocation. cmd.Example MUST stay empty so
	// helpformat.Install's auto-migration does not produce a third
	// Examples block alongside the Footer-supplied one.
	helpformat.Install(rootCmd, helpformat.Options{
		Description: func(*cobra.Command) string { return renderRootBody(docCategories) },
		Footer:      func(*cobra.Command) string { return renderRootExamples(docCategories) },
	})

	// Same wiring for the agent category command. We look it up from
	// rootCmd's children to avoid threading the helpformat dependency
	// down into doc_agent.go (keeps newAgentCommand cobra-only).
	if agentCmd := findChild(rootCmd, "agent"); agentCmd != nil {
		if cat := FindCategory("agent"); cat != nil {
			c := *cat
			helpformat.Install(agentCmd, helpformat.Options{
				Description: func(*cobra.Command) string { return renderCatalogBody(c) },
				Footer:      func(*cobra.Command) string { return renderCatalogExamples(c) },
			})
		}
	}

	// Same wiring for the connection category command. Mirrors the agent
	// block above so `azd ai doc connection --help` shows the same body +
	// examples that runDocIndex (and the bare `connection` invocation)
	// emit. doc_connection.go stays cobra-only.
	if connectionCmd := findChild(rootCmd, "connection"); connectionCmd != nil {
		if cat := FindCategory("connection"); cat != nil {
			c := *cat
			helpformat.Install(connectionCmd, helpformat.Options{
				Description: func(*cobra.Command) string { return renderCatalogBody(c) },
				Footer:      func(*cobra.Command) string { return renderCatalogExamples(c) },
			})
		}
	}

	// Same wiring for the toolbox category command. Mirrors the agent /
	// connection blocks above. doc_toolbox.go stays cobra-only.
	if toolboxCmd := findChild(rootCmd, "toolbox"); toolboxCmd != nil {
		if cat := FindCategory("toolbox"); cat != nil {
			c := *cat
			helpformat.Install(toolboxCmd, helpformat.Options{
				Description: func(*cobra.Command) string { return renderCatalogBody(c) },
				Footer:      func(*cobra.Command) string { return renderCatalogExamples(c) },
			})
		}
	}

	// Walk the rest of the tree (skills, version, metadata) and apply
	// default styling. InstallAll skips already-Installed commands so
	// root and agent (above) keep their custom Description/Footer.
	helpformat.InstallAll(rootCmd)

	return rootCmd
}

// findChild returns the first direct subcommand of parent whose Name()
// matches name. Returns nil when not found.
func findChild(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}
