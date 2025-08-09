// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Register MCP commands
func mcpActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("mcp", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:    "mcp",
			Short:  "Manage Model Context Protocol (MCP) server.",
			Hidden: true,
		},
	})

	// azd mcp start
	group.Add("start", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "start",
			Short: "Starts the MCP server.",
			Long: `Starts the Model Context Protocol (MCP) server.

This command starts an MCP server that can be used by MCP clients to access
azd functionality through the Model Context Protocol interface.`,
			Args: cobra.NoArgs,
		},
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newMcpStartAction,
		FlagsResolver:  newMcpStartFlags,
	})

	// azd mcp consent subcommands
	consentGroup := group.Add("consent", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "consent",
			Short: "Manage MCP tool consent.",
			Long:  "Manage consent rules for MCP tool execution.",
		},
	})

	// azd mcp consent list
	consentGroup.Add("list", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "list",
			Short: "List consent rules.",
			Long:  "List all consent rules for MCP tools.",
			Args:  cobra.NoArgs,
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat:  output.TableFormat,
		ActionResolver: newMcpConsentListAction,
		FlagsResolver:  newMcpConsentFlags,
	})

	// azd mcp consent clear
	consentGroup.Add("clear", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "clear",
			Short: "Clear consent rules.",
			Long:  "Clear consent rules for MCP tools.",
			Args:  cobra.NoArgs,
		},
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newMcpConsentClearAction,
		FlagsResolver:  newMcpConsentFlags,
	})

	return group
}

// Flags for MCP start command
type mcpStartFlags struct {
	global *internal.GlobalCommandOptions
}

func newMcpStartFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *mcpStartFlags {
	flags := &mcpStartFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *mcpStartFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
}

// Action for MCP start command
type mcpStartAction struct {
	flags *mcpStartFlags
}

func newMcpStartAction(
	flags *mcpStartFlags,
	userConfigManager config.UserConfigManager,
) actions.Action {
	return &mcpStartAction{
		flags: flags,
	}
}

func (a *mcpStartAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	s := server.NewMCPServer(
		"AZD MCP Server 🚀", "1.0.0",
		server.WithToolCapabilities(true),
	)
	s.EnableSampling()

	allTools := []server.ServerTool{
		tools.NewAzdPlanInitTool(),
		tools.NewAzdDiscoveryAnalysisTool(),
		tools.NewAzdArchitecturePlanningTool(),
		tools.NewAzdAzureYamlGenerationTool(),
		tools.NewAzdDockerGenerationTool(),
		tools.NewAzdInfrastructureGenerationTool(),
		tools.NewAzdIacGenerationRulesTool(),
		tools.NewAzdProjectValidationTool(),
		tools.NewAzdYamlSchemaTool(),
	}

	s.AddTools(allTools...)

	// Start the server using stdio transport
	if err := server.ServeStdio(s); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}

	return nil, nil
}

// Flags for MCP consent commands
type mcpConsentFlags struct {
	global *internal.GlobalCommandOptions
	scope  string
	toolID string
}

func newMcpConsentFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *mcpConsentFlags {
	flags := &mcpConsentFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *mcpConsentFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
	local.StringVar(&f.scope, "scope", "global", "Consent scope (global, project, session)")
	local.StringVar(&f.toolID, "tool-id", "", "Specific tool ID to operate on")
}

// Action for MCP consent list command
type mcpConsentListAction struct {
	flags             *mcpConsentFlags
	formatter         output.Formatter
	writer            io.Writer
	userConfigManager config.UserConfigManager
	consentManager    consent.ConsentManager
}

func newMcpConsentListAction(
	flags *mcpConsentFlags,
	formatter output.Formatter,
	writer io.Writer,
	userConfigManager config.UserConfigManager,
	consentManager consent.ConsentManager,
) actions.Action {
	return &mcpConsentListAction{
		flags:             flags,
		formatter:         formatter,
		writer:            writer,
		userConfigManager: userConfigManager,
		consentManager:    consentManager,
	}
}

func (a *mcpConsentListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	var scope consent.ConsentScope
	switch a.flags.scope {
	case "global":
		scope = consent.ScopeGlobal
	case "project":
		scope = consent.ScopeProject
	case "session":
		scope = consent.ScopeSession
	default:
		return nil, fmt.Errorf("invalid scope: %s", a.flags.scope)
	}

	rules, err := a.consentManager.ListConsents(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("failed to list consent rules: %w", err)
	}

	if len(rules) == 0 {
		fmt.Fprintf(a.writer, "No consent rules found for scope: %s\n", a.flags.scope)
		return nil, nil
	}

	// Format output
	if a.formatter.Kind() == output.JsonFormat {
		return nil, a.formatter.Format(rules, a.writer, nil)
	}

	// Table format
	fmt.Fprintf(a.writer, "Consent Rules (%s scope):\n", a.flags.scope)
	fmt.Fprintf(a.writer, "%-40s %-15s %-20s\n", "Tool ID", "Permission", "Granted At")
	fmt.Fprintf(a.writer, "%s\n", strings.Repeat("-", 75))

	for _, rule := range rules {
		fmt.Fprintf(a.writer, "%-40s %-15s %-20s\n",
			rule.ToolID,
			rule.Permission,
			rule.GrantedAt.Format("2006-01-02 15:04:05"))
	}

	return nil, nil
}

// Action for MCP consent clear command
type mcpConsentClearAction struct {
	flags             *mcpConsentFlags
	console           input.Console
	userConfigManager config.UserConfigManager
	consentManager    consent.ConsentManager
}

func newMcpConsentClearAction(
	flags *mcpConsentFlags,
	console input.Console,
	userConfigManager config.UserConfigManager,
	consentManager consent.ConsentManager,
) actions.Action {
	return &mcpConsentClearAction{
		flags:             flags,
		console:           console,
		userConfigManager: userConfigManager,
		consentManager:    consentManager,
	}
}

func (a *mcpConsentClearAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	var scope consent.ConsentScope
	switch a.flags.scope {
	case "global":
		scope = consent.ScopeGlobal
	case "project":
		scope = consent.ScopeProject
	case "session":
		scope = consent.ScopeSession
	default:
		return nil, fmt.Errorf("invalid scope: %s", a.flags.scope)
	}

	var err error
	if a.flags.toolID != "" {
		// Clear specific tool
		err = a.consentManager.ClearConsentByToolID(ctx, a.flags.toolID, scope)
		if err == nil {
			fmt.Fprintf(a.console.Handles().Stdout, "Cleared consent for tool: %s\n", a.flags.toolID)
		}
	} else {
		// Clear all rules for scope
		confirmed, confirmErr := a.console.Confirm(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf("Are you sure you want to clear all consent rules for scope '%s'?", a.flags.scope),
		})
		if confirmErr != nil {
			return nil, confirmErr
		}

		if !confirmed {
			fmt.Fprintf(a.console.Handles().Stdout, "Operation cancelled.\n")
			return nil, nil
		}

		err = a.consentManager.ClearConsents(ctx, scope)
		if err == nil {
			fmt.Fprintf(a.console.Handles().Stdout, "Cleared all consent rules for scope: %s\n", a.flags.scope)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to clear consent rules: %w", err)
	}

	return nil, nil
}
