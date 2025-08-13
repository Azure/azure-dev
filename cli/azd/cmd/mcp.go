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

// Flags for MCP consent commands
type mcpConsentFlags struct {
	global           *internal.GlobalCommandOptions
	scope            string
	target           string
	operationContext string
}

func newMcpConsentFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *mcpConsentFlags {
	flags := &mcpConsentFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *mcpConsentFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
	local.StringVar(&f.scope, "scope", "global", "Consent scope (global, project, session)")
	local.StringVar(&f.target, "target", "", "Specific target to operate on (server/tool format)")
	local.StringVar(&f.operationContext, "context", "", "Operation context to filter by (tool, sampling)")
}

// Flags for MCP consent grant command
type mcpConsentGrantFlags struct {
	globalOptions *internal.GlobalCommandOptions
	tool          string
	server        string
	globalFlag    bool
	action        string
	operation     string
	permission    string
	ruleScope     string
}

func newMcpConsentGrantFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *mcpConsentGrantFlags {
	flags := &mcpConsentGrantFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *mcpConsentGrantFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.globalOptions = global
	local.StringVar(&f.tool, "tool", "", "Specific tool name (requires --server)")
	local.StringVar(&f.server, "server", "", "Server name")
	local.BoolVar(&f.globalFlag, "global", false, "Apply globally to all servers")
	local.StringVar(&f.action, "action", "all", "Action type: 'all' or 'readonly'")
	local.StringVar(&f.operation, "operation", "tool", "Operation type: 'tool' or 'sampling'")
	local.StringVar(&f.permission, "permission", "allow", "Permission: 'allow', 'deny', or 'prompt'")
	local.StringVar(&f.ruleScope, "scope", "global", "Rule scope: 'session', 'project', or 'global'")
}

// Action for MCP consent list command
type mcpConsentListAction struct {
	flags             *mcpConsentFlags
	formatter         output.Formatter
	writer            io.Writer
	console           input.Console
	userConfigManager config.UserConfigManager
	consentManager    consent.ConsentManager
}

func newMcpConsentListAction(
	flags *mcpConsentFlags,
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
	var scope consent.Scope
	switch a.flags.scope {
	case "global":
		scope = consent.ScopeGlobal
	case "project":
		scope = consent.ScopeProject
	case "session":
		scope = consent.ScopeSession
	default:
		return nil, fmt.Errorf("invalid scope: %s (allowed: global, project, session)", a.flags.scope)
	}

	var rules []consent.ConsentRule
	var err error

	// Use operation context-filtered method if context is specified
	if a.flags.operationContext != "" {
		operationContext, parseErr := consent.ParseOperationContext(a.flags.operationContext)
		if parseErr != nil {
			return nil, parseErr
		}
		rules, err = a.consentManager.ListConsentsByOperationContext(ctx, scope, operationContext)
	} else {
		rules, err = a.consentManager.ListConsents(ctx, scope)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to list consent rules: %w", err)
	}

	if len(rules) == 0 {
		var typeInfo string
		if a.flags.operationContext != "" {
			typeInfo = fmt.Sprintf(" of context '%s'", a.flags.operationContext)
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
	// Command heading
	fmt.Fprintf(a.console.Handles().Stdout, "Clearing MCP consent rules...\n\n")

	var scope consent.Scope
	switch a.flags.scope {
	case "global":
		scope = consent.ScopeGlobal
	case "project":
		scope = consent.ScopeProject
	case "session":
		scope = consent.ScopeSession
	default:
		return nil, fmt.Errorf("invalid scope: %s (allowed: global, project, session)", a.flags.scope)
	}

	var err error
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
		if a.flags.operationContext != "" {
			confirmMessage = fmt.Sprintf(
				"Are you sure you want to clear all %s consent rules for scope '%s'?",
				a.flags.operationContext,
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

		if a.flags.operationContext != "" {
			// Context-specific clearing using the new consent manager method
			operationContext, parseErr := consent.ParseOperationContext(a.flags.operationContext)
			if parseErr != nil {
				return nil, parseErr
			}

			err = a.consentManager.ClearConsentsByOperationContext(ctx, scope, operationContext)
			if err == nil {
				fmt.Fprintf(
					a.console.Handles().Stdout,
					"Cleared all %s consent rules for scope: %s\n",
					a.flags.operationContext,
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
	var actionType consent.ActionType
	switch a.flags.action {
	case "readonly":
		actionType = consent.ActionReadOnly
	case "all":
		actionType = consent.ActionAny
	default:
		return nil, fmt.Errorf("--action must be 'readonly' or 'all'")
	}

	// Validate operation context
	var operationContext consent.OperationType
	switch a.flags.operation {
	case "tool":
		operationContext = consent.OperationTypeTool
	case "sampling":
		operationContext = consent.OperationTypeSampling
	default:
		return nil, fmt.Errorf("--context must be 'tool' or 'sampling'")
	}

	// Validate permission
	var permission consent.Permission
	switch a.flags.permission {
	case "allow":
		permission = consent.PermissionAllow
	case "deny":
		permission = consent.PermissionDeny
	case "prompt":
		permission = consent.PermissionPrompt
	default:
		return nil, fmt.Errorf("--decision must be 'allow', 'deny', or 'prompt'")
	}

	// Validate rule scope
	var ruleScope consent.Scope
	switch a.flags.ruleScope {
	case "session":
		ruleScope = consent.ScopeSession
	case "project":
		ruleScope = consent.ScopeProject
	case "global":
		ruleScope = consent.ScopeGlobal
	default:
		return nil, fmt.Errorf("--scope must be 'session', 'project', or 'global'")
	}

	// For sampling context, tool-specific grants are not supported
	if operationContext == consent.OperationTypeSampling && a.flags.tool != "" {
		return nil, fmt.Errorf("--tool is not supported for sampling rules")
	}

	// Build target
	var target consent.Target
	var description string

	if a.flags.globalFlag {
		target = consent.NewGlobalTarget()
		if operationContext == consent.OperationTypeSampling {
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
		if operationContext == consent.OperationTypeSampling {
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
		Operation:  operationContext,
		Permission: permission,
	}

	if err := a.consentManager.GrantConsent(ctx, rule, ruleScope); err != nil {
		return nil, fmt.Errorf("failed to grant consent: %w", err)
	}

	fmt.Fprintf(a.console.Handles().Stdout, "Granted rule for %s\n", description)

	return nil, nil
}
