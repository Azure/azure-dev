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

	// azd mcp consent grant
	consentGroup.Add("grant", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "grant",
			Short: "Grant consent trust rules.",
			Long:  "Grant trust rules for MCP tools and servers.",
			Args:  cobra.NoArgs,
		},
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newMcpConsentGrantAction,
		FlagsResolver:  newMcpConsentGrantFlags,
	})

	// azd mcp consent revoke
	consentGroup.Add("revoke", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "revoke",
			Short: "Revoke consent trust rules.",
			Long:  "Revoke specific consent rules for MCP tools and servers.",
			Args:  cobra.NoArgs,
		},
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newMcpConsentRevokeAction,
		FlagsResolver:  newMcpConsentRevokeFlags,
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
	global   *internal.GlobalCommandOptions
	scope    string
	toolID   string
	ruleType string
}

func newMcpConsentFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *mcpConsentFlags {
	flags := &mcpConsentFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *mcpConsentFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
	local.StringVar(&f.scope, "scope", "global", "Consent scope (global, project)")
	local.StringVar(&f.toolID, "tool-id", "", "Specific tool ID to operate on")
	local.StringVar(&f.ruleType, "type", "", "Rule type to filter by (tool, sampling)")
}

// Flags for MCP consent grant command
type mcpConsentGrantFlags struct {
	globalOptions *internal.GlobalCommandOptions
	tool          string
	server        string
	globalFlag    bool
	scope         string
	ruleType      string
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
	local.StringVar(&f.scope, "scope", "all", "Scope of the rule: 'all' or 'read-only'")
	local.StringVar(&f.ruleType, "type", "tool", "Type of rule: 'tool' or 'sampling'")
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
	// Command heading
	fmt.Fprintf(a.writer, "Listing MCP consent rules...\n\n")

	var scope consent.ConsentScope
	switch a.flags.scope {
	case "global":
		scope = consent.ScopeGlobal
	case "project":
		scope = consent.ScopeProject
	default:
		return nil, fmt.Errorf("invalid scope: %s (allowed: global, project)", a.flags.scope)
	}

	var rules []consent.ConsentRule
	var err error

	// Use type-filtered method if type is specified
	if a.flags.ruleType != "" {
		ruleType, parseErr := consent.ParseConsentRuleType(a.flags.ruleType)
		if parseErr != nil {
			return nil, parseErr
		}
		rules, err = a.consentManager.ListConsentsByType(ctx, scope, ruleType)
	} else {
		rules, err = a.consentManager.ListConsents(ctx, scope)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to list consent rules: %w", err)
	}

	if len(rules) == 0 {
		typeInfo := ""
		if a.flags.ruleType != "" {
			typeInfo = fmt.Sprintf(" of type '%s'", a.flags.ruleType)
		}
		fmt.Fprintf(a.writer, "No consent rules found for scope: %s%s\n", a.flags.scope, typeInfo)
		return nil, nil
	}

	// Format output
	if a.formatter.Kind() == output.JsonFormat {
		return nil, a.formatter.Format(rules, a.writer, nil)
	}

	// Table format
	fmt.Fprintf(a.writer, "Consent Rules (%s scope):\n", a.flags.scope)
	fmt.Fprintf(a.writer, "%-10s %-35s %-15s %-20s\n", "Type", "Tool ID", "Permission", "Granted At")
	fmt.Fprintf(a.writer, "%s\n", strings.Repeat("-", 80))

	for _, rule := range rules {
		fmt.Fprintf(a.writer, "%-10s %-35s %-15s %-20s\n",
			rule.Type,
			rule.ToolID,
			rule.Permission,
			rule.GrantedAt.Format("2006-01-02 15:04:05"))
	}

	fmt.Fprintf(a.writer, "\nListed %d consent rule(s)\n", len(rules))

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
	// Command heading
	fmt.Fprintf(a.console.Handles().Stdout, "Clearing MCP consent rules...\n\n")

	var scope consent.ConsentScope
	switch a.flags.scope {
	case "global":
		scope = consent.ScopeGlobal
	case "project":
		scope = consent.ScopeProject
	default:
		return nil, fmt.Errorf("invalid scope: %s (allowed: global, project)", a.flags.scope)
	}

	var err error
	if a.flags.toolID != "" {
		// Clear specific tool
		err = a.consentManager.ClearConsentByToolID(ctx, a.flags.toolID, scope)
		if err == nil {
			fmt.Fprintf(a.console.Handles().Stdout, "Cleared consent for tool: %s\n", a.flags.toolID)
		}
	} else {
		// Get confirmation message based on type filter
		confirmMessage := fmt.Sprintf("Are you sure you want to clear all consent rules for scope '%s'?", a.flags.scope)
		if a.flags.ruleType != "" {
			confirmMessage = fmt.Sprintf(
				"Are you sure you want to clear all %s consent rules for scope '%s'?",
				a.flags.ruleType,
				a.flags.scope,
			)
		}

		// Clear all rules for scope (with optional type filtering)
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

		if a.flags.ruleType != "" {
			// Type-specific clearing using the new consent manager method
			ruleType, parseErr := consent.ParseConsentRuleType(a.flags.ruleType)
			if parseErr != nil {
				return nil, parseErr
			}

			err = a.consentManager.ClearConsentsByType(ctx, scope, ruleType)
			if err == nil {
				fmt.Fprintf(
					a.console.Handles().Stdout,
					"Cleared all %s consent rules for scope: %s\n",
					a.flags.ruleType,
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

	// Validate scope
	if a.flags.scope != "all" && a.flags.scope != "read-only" {
		return nil, fmt.Errorf("--scope must be 'all' or 'read-only'")
	}

	// Validate type using the new parser
	ruleType, err := consent.ParseConsentRuleType(a.flags.ruleType)
	if err != nil {
		return nil, err
	}

	// For sampling type, tool-specific grants are not supported
	if ruleType == consent.ConsentRuleTypeSampling && a.flags.tool != "" {
		return nil, fmt.Errorf("--tool is not supported for sampling rules")
	}

	// Build rule
	var toolID string
	var ruleScope consent.RuleScope
	var description string

	if a.flags.scope == "read-only" {
		ruleScope = consent.RuleScopeReadOnly
	} else {
		ruleScope = consent.RuleScopeAll
	}

	if a.flags.globalFlag {
		toolID = "*"
		if ruleType == consent.ConsentRuleTypeSampling {
			if a.flags.scope == "read-only" {
				description = "all read-only sampling globally"
			} else {
				description = "all sampling globally"
			}
		} else {
			if a.flags.scope == "read-only" {
				description = "all read-only tools globally"
			} else {
				description = "all tools globally"
			}
		}
	} else if a.flags.tool != "" {
		toolID = fmt.Sprintf("%s/%s", a.flags.server, a.flags.tool)
		if a.flags.scope == "read-only" {
			description = fmt.Sprintf("read-only tool %s from server %s", a.flags.tool, a.flags.server)
		} else {
			description = fmt.Sprintf("tool %s from server %s", a.flags.tool, a.flags.server)
		}
	} else {
		toolID = fmt.Sprintf("%s/*", a.flags.server)
		if ruleType == consent.ConsentRuleTypeSampling {
			if a.flags.scope == "read-only" {
				description = fmt.Sprintf("read-only sampling from server %s", a.flags.server)
			} else {
				description = fmt.Sprintf("all sampling from server %s", a.flags.server)
			}
		} else {
			if a.flags.scope == "read-only" {
				description = fmt.Sprintf("read-only tools from server %s", a.flags.server)
			} else {
				description = fmt.Sprintf("all tools from server %s", a.flags.server)
			}
		}
	}

	rule := consent.ConsentRule{
		Type:       ruleType,
		ToolID:     toolID,
		Permission: consent.ConsentAlways,
		RuleScope:  ruleScope,
	}

	if err := a.consentManager.GrantConsent(ctx, rule, consent.ScopeGlobal); err != nil {
		return nil, fmt.Errorf("failed to grant consent: %w", err)
	}

	fmt.Fprintf(a.console.Handles().Stdout, "Granted trust for %s\n", description)

	return nil, nil
}

// Flags for MCP consent revoke command
type mcpConsentRevokeFlags struct {
	globalOptions *internal.GlobalCommandOptions
	tool          string
	server        string
	globalFlag    bool
	scope         string
	toolPattern   string
	ruleType      string
}

func newMcpConsentRevokeFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *mcpConsentRevokeFlags {
	flags := &mcpConsentRevokeFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *mcpConsentRevokeFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.globalOptions = global
	local.StringVar(&f.tool, "tool", "", "Specific tool name (requires --server)")
	local.StringVar(&f.server, "server", "", "Server name")
	local.BoolVar(&f.globalFlag, "global", false, "Apply globally to all servers")
	local.StringVar(&f.scope, "scope", "all", "Scope of the rule: 'all' or 'read-only'")
	local.StringVar(
		&f.toolPattern,
		"rule-pattern",
		"",
		"Revoke trust for a specific rule pattern (e.g., 'server/tool' or 'server/*')",
	)
	local.StringVar(&f.ruleType, "type", "", "Type of rule to revoke: 'tool' or 'sampling' (default: all types)")
}

// Action for MCP consent revoke command
type mcpConsentRevokeAction struct {
	flags             *mcpConsentRevokeFlags
	console           input.Console
	userConfigManager config.UserConfigManager
	consentManager    consent.ConsentManager
}

func newMcpConsentRevokeAction(
	flags *mcpConsentRevokeFlags,
	console input.Console,
	userConfigManager config.UserConfigManager,
	consentManager consent.ConsentManager,
) actions.Action {
	return &mcpConsentRevokeAction{
		flags:             flags,
		console:           console,
		userConfigManager: userConfigManager,
		consentManager:    consentManager,
	}
}

func (a *mcpConsentRevokeAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Command heading
	fmt.Fprintf(a.console.Handles().Stdout, "Revoking MCP consent rules...\n\n")

	// Count options set
	optionsSet := 0
	if a.flags.globalFlag {
		optionsSet++
	}
	if a.flags.server != "" {
		optionsSet++
	}
	if a.flags.toolPattern != "" {
		optionsSet++
	}

	if optionsSet == 0 {
		return nil, fmt.Errorf("specify one of: --global, --server, or --rule-pattern")
	}

	if optionsSet > 1 {
		return nil, fmt.Errorf("specify only one option at a time")
	}

	// Validate flag combinations for new structure
	if a.flags.tool != "" && a.flags.server == "" {
		return nil, fmt.Errorf("--tool requires --server")
	}

	if a.flags.globalFlag && (a.flags.server != "" || a.flags.tool != "") {
		return nil, fmt.Errorf("--global cannot be combined with --server or --tool")
	}

	// Validate scope
	if a.flags.scope != "all" && a.flags.scope != "read-only" {
		return nil, fmt.Errorf("--scope must be 'all' or 'read-only'")
	}

	// Validate type if specified
	var ruleType consent.ConsentRuleType
	if a.flags.ruleType != "" {
		var err error
		ruleType, err = consent.ParseConsentRuleType(a.flags.ruleType)
		if err != nil {
			return nil, err
		}

		// For sampling type, tool-specific revocations are not supported
		if ruleType == consent.ConsentRuleTypeSampling && a.flags.tool != "" {
			return nil, fmt.Errorf("--tool is not supported for sampling rules")
		}
	}

	var toolID string
	var description string

	if a.flags.toolPattern != "" {
		toolID = a.flags.toolPattern
		if a.flags.ruleType != "" {
			description = fmt.Sprintf("%s trust for pattern: %s", a.flags.ruleType, a.flags.toolPattern)
		} else {
			description = fmt.Sprintf("trust for pattern: %s", a.flags.toolPattern)
		}
	} else if a.flags.globalFlag {
		toolID = "*"
		if a.flags.ruleType == "sampling" {
			if a.flags.scope == "read-only" {
				description = "global read-only sampling trust"
			} else {
				description = "global sampling trust"
			}
		} else if a.flags.ruleType == "tool" {
			if a.flags.scope == "read-only" {
				description = "global read-only tool trust"
			} else {
				description = "global tool trust"
			}
		} else {
			if a.flags.scope == "read-only" {
				description = "global read-only trust"
			} else {
				description = "global trust"
			}
		}
	} else if a.flags.tool != "" {
		toolID = fmt.Sprintf("%s/%s", a.flags.server, a.flags.tool)
		if a.flags.scope == "read-only" {
			description = fmt.Sprintf("read-only trust for tool %s from server %s", a.flags.tool, a.flags.server)
		} else {
			description = fmt.Sprintf("trust for tool %s from server %s", a.flags.tool, a.flags.server)
		}
	} else {
		toolID = fmt.Sprintf("%s/*", a.flags.server)
		if a.flags.ruleType == "sampling" {
			if a.flags.scope == "read-only" {
				description = fmt.Sprintf("read-only sampling trust for server: %s", a.flags.server)
			} else {
				description = fmt.Sprintf("sampling trust for server: %s", a.flags.server)
			}
		} else if a.flags.ruleType == "tool" {
			if a.flags.scope == "read-only" {
				description = fmt.Sprintf("read-only tool trust for server: %s", a.flags.server)
			} else {
				description = fmt.Sprintf("tool trust for server: %s", a.flags.server)
			}
		} else {
			if a.flags.scope == "read-only" {
				description = fmt.Sprintf("read-only trust for server: %s", a.flags.server)
			} else {
				description = fmt.Sprintf("trust for server: %s", a.flags.server)
			}
		}
	}

	// If type filtering is requested, use the new consent manager method
	if a.flags.ruleType != "" {
		rules, err := a.consentManager.ListConsentsByType(ctx, consent.ScopeGlobal, ruleType)
		if err != nil {
			return nil, fmt.Errorf("failed to list consent rules: %w", err)
		}

		rulesToClear := make([]consent.ConsentRule, 0)
		for _, rule := range rules {
			if ruleMatchesPattern(rule.ToolID, toolID) {
				rulesToClear = append(rulesToClear, rule)
			}
		}

		if len(rulesToClear) == 0 {
			fmt.Fprintf(a.console.Handles().Stdout, "No matching %s rules found\n", a.flags.ruleType)
			return nil, nil
		}

		for _, rule := range rulesToClear {
			if err := a.consentManager.ClearConsentByToolID(ctx, rule.ToolID, consent.ScopeGlobal); err != nil {
				return nil, fmt.Errorf("failed to revoke consent for %s: %w", rule.ToolID, err)
			}
		}
	} else {
		if err := a.consentManager.ClearConsentByToolID(ctx, toolID, consent.ScopeGlobal); err != nil {
			return nil, fmt.Errorf("failed to revoke consent: %w", err)
		}
	}

	fmt.Fprintf(a.console.Handles().Stdout, "Revoked %s\n", description)

	return nil, nil
}

// ruleMatchesPattern checks if a rule's toolID matches the given pattern
func ruleMatchesPattern(ruleToolID, pattern string) bool {
	if pattern == "*" {
		return true
	}
	if pattern == ruleToolID {
		return true
	}
	// Handle server/* patterns
	if strings.HasSuffix(pattern, "/*") {
		serverPattern := strings.TrimSuffix(pattern, "/*")
		return strings.HasPrefix(ruleToolID, serverPattern+"/") || ruleToolID == serverPattern+"/*"
	}
	return false
}
