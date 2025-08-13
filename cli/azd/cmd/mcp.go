// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"

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
		FlagsResolver:  newMcpConsentListFlags,
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
		FlagsResolver:  newMcpConsentClearFlags,
	})

	// azd mcp consent grant
	consentGroup.Add("grant", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "grant",
			Short: "Grant consent trust rules.",
			Long: `Grant trust rules for MCP tools and servers.

This command creates consent rules that allow MCP tools to execute
without prompting for permission. You can specify different permission
levels and scopes for the rules.

Examples:
  # Grant always permission to all tools globally
  azd mcp consent grant --global --permission always

  # Grant session permission to a specific server
  azd mcp consent grant --server my-server --permission session

  # Grant project permission to a specific tool with read-only scope
  azd mcp consent grant --server my-server --tool my-tool --permission project --scope read-only`,
			Args: cobra.NoArgs,
		},
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newMcpConsentGrantAction,
		FlagsResolver:  newMcpConsentGrantFlags,
	})

	// TODO: Re-implement revoke command with new structure
	// azd mcp consent revoke
	// consentGroup.Add("revoke", &actions.ActionDescriptorOptions{
	// 	Command: &cobra.Command{
	// 		Use:   "revoke",
	// 		Short: "Revoke consent trust rules.",
	// 		Long:  "Revoke specific consent rules for MCP tools and servers.",
	// 		Args:  cobra.NoArgs,
	// 	},
	// 	OutputFormats:  []output.Format{output.NoneFormat},
	// 	DefaultFormat:  output.NoneFormat,
	// 	ActionResolver: newMcpConsentRevokeAction,
	// 	FlagsResolver:  newMcpConsentRevokeFlags,
	// })

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
		tools.NewSamplingTool(),
	}

	s.AddTools(allTools...)

	// Start the server using stdio transport
	if err := server.ServeStdio(s); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}

	return nil, nil
}

// Flags for MCP consent list command
type mcpConsentListFlags struct {
	global    *internal.GlobalCommandOptions
	scope     string
	target    string
	operation string
}

func newMcpConsentListFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *mcpConsentListFlags {
	flags := &mcpConsentListFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *mcpConsentListFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
	local.StringVar(&f.scope, "scope", "global", "Consent scope (global, project)")
	local.StringVar(&f.target, "target", "", "Specific target to operate on (server/tool format)")
	local.StringVar(&f.operation, "operation", "", "Operation to filter by (tool, sampling)")
}

// Flags for MCP consent grant command
type mcpConsentGrantFlags struct {
	global     *internal.GlobalCommandOptions
	tool       string
	server     string
	globalFlag bool
	action     string
	operation  string
	permission string
	ruleScope  string
}

func newMcpConsentGrantFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *mcpConsentGrantFlags {
	flags := &mcpConsentGrantFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *mcpConsentGrantFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
	local.StringVar(&f.tool, "tool", "", "Specific tool name (requires --server)")
	local.StringVar(&f.server, "server", "", "Server name")
	local.BoolVar(&f.globalFlag, "global", false, "Apply globally to all servers")
	local.StringVar(&f.action, "action", "all", "Action type: 'all' or 'readonly'")
	local.StringVar(&f.operation, "operation", "tool", "Operation type: 'tool' or 'sampling'")
	local.StringVar(&f.permission, "permission", "allow", "Permission: 'allow', 'deny', or 'prompt'")
	local.StringVar(&f.ruleScope, "scope", "global", "Rule scope: 'global', or 'project'")
}

// Flags for MCP consent clear command
type mcpConsentClearFlags struct {
	global    *internal.GlobalCommandOptions
	scope     string
	target    string
	operation string
}

func newMcpConsentClearFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *mcpConsentClearFlags {
	flags := &mcpConsentClearFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *mcpConsentClearFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
	local.StringVar(&f.scope, "scope", "global", "Consent scope (global, project)")
	local.StringVar(&f.target, "target", "", "Specific target to operate on (server/tool format)")
	local.StringVar(&f.operation, "operation", "", "Operation to filter by (tool, sampling)")
}

// Action for MCP consent list command
type mcpConsentListAction struct {
	flags             *mcpConsentListFlags
	formatter         output.Formatter
	writer            io.Writer
	console           input.Console
	userConfigManager config.UserConfigManager
	consentManager    consent.ConsentManager
}

func newMcpConsentListAction(
	flags *mcpConsentListFlags,
	formatter output.Formatter,
	writer io.Writer,
	console input.Console,
	userConfigManager config.UserConfigManager,
	consentManager consent.ConsentManager,
) actions.Action {
	return &mcpConsentListAction{
		flags:             flags,
		formatter:         formatter,
		writer:            writer,
		console:           console,
		userConfigManager: userConfigManager,
		consentManager:    consentManager,
	}
}

func (a *mcpConsentListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	scope, err := consent.ParseScope(a.flags.scope)
	if err != nil {
		return nil, err
	}

	var rules []consent.ConsentRule

	// Use operation context-filtered method if context is specified
	if a.flags.operation != "" {
		operation, parseErr := consent.ParseOperationType(a.flags.operation)
		if parseErr != nil {
			return nil, parseErr
		}
		rules, err = a.consentManager.ListConsentsByOperationType(ctx, scope, operation)
	} else {
		rules, err = a.consentManager.ListConsentRules(ctx, scope)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to list consent rules: %w", err)
	}

	if len(rules) == 0 {
		var typeInfo string
		if a.flags.operation != "" {
			typeInfo = fmt.Sprintf(" of context '%s'", a.flags.operation)
		}
		fmt.Fprintf(a.writer, "No consent rules found%s.\n", typeInfo)
		return nil, nil
	}

	// Convert rules to display format
	type ruleDisplay struct {
		Target     string `json:"target"`
		Context    string `json:"context"`
		Action     string `json:"action"`
		Permission string `json:"permission"`
		Scope      string `json:"scope"`
		GrantedAt  string `json:"grantedAt"`
	}

	var displayRules []ruleDisplay
	for _, rule := range rules {
		displayRules = append(displayRules, ruleDisplay{
			Target:     string(rule.Target),
			Context:    string(rule.Operation),
			Action:     string(rule.Action),
			Permission: string(rule.Permission),
			Scope:      string(rule.Scope),
			GrantedAt:  rule.GrantedAt.Format("2006-01-02 15:04:05"),
		})
	}

	if a.formatter.Kind() == output.JsonFormat {
		return nil, a.formatter.Format(displayRules, a.writer, nil)
	}

	// Use table formatter for better output
	if a.formatter.Kind() == output.TableFormat {
		columns := []output.Column{
			{
				Heading:       "Target",
				ValueTemplate: "{{.Target}}",
			},
			{
				Heading:       "Context",
				ValueTemplate: "{{.Context}}",
			},
			{
				Heading:       "Action",
				ValueTemplate: "{{.Action}}",
			},
			{
				Heading:       "Permission",
				ValueTemplate: "{{.Permission}}",
			},
			{
				Heading:       "Scope",
				ValueTemplate: "{{.Scope}}",
			},
			{
				Heading:       "Granted At",
				ValueTemplate: "{{.GrantedAt}}",
			},
		}

		return nil, a.formatter.Format(displayRules, a.writer, output.TableFormatterOptions{
			Columns: columns,
		})
	}

	// Fallback to simple formatting
	return nil, a.formatter.Format(displayRules, a.writer, nil)
}

// Action for MCP consent clear command
type mcpConsentClearAction struct {
	flags             *mcpConsentClearFlags
	console           input.Console
	userConfigManager config.UserConfigManager
	consentManager    consent.ConsentManager
}

func newMcpConsentClearAction(
	flags *mcpConsentClearFlags,
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
	// Command heading
	fmt.Fprintf(a.console.Handles().Stdout, "Clearing MCP consent rules...\n\n")

	scope, err := consent.ParseScope(a.flags.scope)
	if err != nil {
		return nil, err
	}

	if a.flags.target != "" {
		// Clear specific target
		target := consent.Target(a.flags.target)
		err = a.consentManager.ClearConsentByTarget(ctx, target, scope)
		if err == nil {
			fmt.Fprintf(a.console.Handles().Stdout, "Cleared consent for target: %s\n", a.flags.target)
		}
	} else {
		// Get confirmation message based on context filter
		confirmMessage := fmt.Sprintf("Are you sure you want to clear all consent rules for scope '%s'?", a.flags.scope)
		if a.flags.operation != "" {
			confirmMessage = fmt.Sprintf(
				"Are you sure you want to clear all %s consent rules for scope '%s'?",
				a.flags.operation,
				a.flags.scope,
			)
		}

		// Clear all rules for scope (with optional context filtering)
		confirmed, confirmErr := a.console.Confirm(ctx, input.ConsoleOptions{
			Message: confirmMessage,
		})
		if confirmErr != nil {
			return nil, confirmErr
		}

		if !confirmed {
			fmt.Fprintf(a.console.Handles().Stdout, "Operation cancelled.\n")
			return nil, nil
		}

		if a.flags.operation != "" {
			// Context-specific clearing using the new consent manager method
			operation, parseErr := consent.ParseOperationType(a.flags.operation)
			if parseErr != nil {
				return nil, parseErr
			}

			err = a.consentManager.ClearConsentsByOperationType(ctx, scope, operation)
			if err == nil {
				fmt.Fprintf(
					a.console.Handles().Stdout,
					"Cleared all %s consent rules for scope: %s\n",
					a.flags.operation,
					a.flags.scope,
				)
			}
		} else {
			// Clear all rules for scope
			err = a.consentManager.ClearConsents(ctx, scope)
			if err == nil {
				fmt.Fprintf(a.console.Handles().Stdout, "Cleared all consent rules for scope: %s\n", a.flags.scope)
			}
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to clear consent rules: %w", err)
	}

	return nil, nil
}

// Action for MCP consent grant command
type mcpConsentGrantAction struct {
	flags             *mcpConsentGrantFlags
	console           input.Console
	userConfigManager config.UserConfigManager
	consentManager    consent.ConsentManager
}

func newMcpConsentGrantAction(
	flags *mcpConsentGrantFlags,
	console input.Console,
	userConfigManager config.UserConfigManager,
	consentManager consent.ConsentManager,
) actions.Action {
	return &mcpConsentGrantAction{
		flags:             flags,
		console:           console,
		userConfigManager: userConfigManager,
		consentManager:    consentManager,
	}
}

func (a *mcpConsentGrantAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Command heading
	fmt.Fprintf(a.console.Handles().Stdout, "Granting MCP consent rules...\n\n")

	// Validate flag combinations
	if a.flags.tool != "" && a.flags.server == "" {
		return nil, fmt.Errorf("--tool requires --server")
	}

	if a.flags.globalFlag && (a.flags.server != "" || a.flags.tool != "") {
		return nil, fmt.Errorf("--global cannot be combined with --server or --tool")
	}

	if !a.flags.globalFlag && a.flags.server == "" {
		return nil, fmt.Errorf("specify either --global or --server")
	}

	// Validate action type
	actionType, err := consent.ParseActionType(a.flags.action)
	if err != nil {
		return nil, err
	}

	// Validate operation context
	operation, err := consent.ParseOperationType(a.flags.operation)
	if err != nil {
		return nil, err
	}

	// Validate permission
	permission, err := consent.ParsePermission(a.flags.permission)
	if err != nil {
		return nil, err
	}

	// Validate rule scope
	ruleScope, err := consent.ParseScope(a.flags.ruleScope)
	if err != nil {
		return nil, err
	}

	// For sampling context, tool-specific grants are not supported
	if operation == consent.OperationTypeSampling && a.flags.tool != "" {
		return nil, fmt.Errorf("--tool is not supported for sampling rules")
	}

	// Build target
	var target consent.Target
	var description string

	if a.flags.globalFlag {
		target = consent.NewGlobalTarget()
		if operation == consent.OperationTypeSampling {
			if actionType == consent.ActionReadOnly {
				description = fmt.Sprintf("all read-only sampling globally (%s)", permission)
			} else {
				description = fmt.Sprintf("all sampling globally (%s)", permission)
			}
		} else {
			if actionType == consent.ActionReadOnly {
				description = fmt.Sprintf("all read-only tools globally (%s)", permission)
			} else {
				description = fmt.Sprintf("all tools globally (%s)", permission)
			}
		}
	} else if a.flags.tool != "" {
		target = consent.NewToolTarget(a.flags.server, a.flags.tool)
		if actionType == consent.ActionReadOnly {
			description = fmt.Sprintf("read-only tool %s from server %s (%s)", a.flags.tool, a.flags.server, permission)
		} else {
			description = fmt.Sprintf("tool %s from server %s (%s)", a.flags.tool, a.flags.server, permission)
		}
	} else {
		target = consent.NewServerTarget(a.flags.server)
		if operation == consent.OperationTypeSampling {
			if actionType == consent.ActionReadOnly {
				description = fmt.Sprintf("read-only sampling from server %s (%s)", a.flags.server, permission)
			} else {
				description = fmt.Sprintf("all sampling from server %s (%s)", a.flags.server, permission)
			}
		} else {
			if actionType == consent.ActionReadOnly {
				description = fmt.Sprintf("read-only tools from server %s (%s)", a.flags.server, permission)
			} else {
				description = fmt.Sprintf("all tools from server %s (%s)", a.flags.server, permission)
			}
		}
	}

	rule := consent.ConsentRule{
		Scope:      ruleScope,
		Target:     target,
		Action:     actionType,
		Operation:  operation,
		Permission: permission,
	}

	if err := a.consentManager.GrantConsent(ctx, rule, ruleScope); err != nil {
		return nil, fmt.Errorf("failed to grant consent: %w", err)
	}

	fmt.Fprintf(a.console.Handles().Stdout, "Granted rule for %s\n", description)

	return nil, nil
}
