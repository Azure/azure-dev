// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/fatih/color"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Register MCP commands
func mcpActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("mcp", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "mcp",
			Short: fmt.Sprintf("Manage Model Context Protocol (MCP) server. %s", output.WithWarningFormat("(Alpha)")),
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupAlpha,
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

	// azd mcp consent revoke
	consentGroup.Add("revoke", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "revoke",
			Short: "Revoke consent rules.",
			Long:  "Revoke consent rules for MCP tools.",
			Args:  cobra.NoArgs,
		},
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newMcpConsentRevokeAction,
		FlagsResolver:  newMcpConsentRevokeFlags,
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

  # Grant project permission to a specific tool with read-only scope
  azd mcp consent grant --server my-server --tool my-tool --permission project --scope read-only`,
			Args: cobra.NoArgs,
		},
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newMcpConsentGrantAction,
		FlagsResolver:  newMcpConsentGrantFlags,
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
	flags            *mcpStartFlags
	extensionManager *extensions.Manager
	grpcServer       *grpcserver.Server
}

func newMcpStartAction(
	flags *mcpStartFlags,
	userConfigManager config.UserConfigManager,
	extensionManager *extensions.Manager,
	grpcServer *grpcserver.Server,
) actions.Action {
	return &mcpStartAction{
		flags:            flags,
		extensionManager: extensionManager,
		grpcServer:       grpcServer,
	}
}

func (a *mcpStartAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	mcpServer := server.NewMCPServer(
		"AZD MCP Server 🚀", "1.0.0",
		server.WithToolCapabilities(true),
	)
	mcpServer.EnableSampling()

	azdTools := []server.ServerTool{
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
		tools.NewAzdErrorTroubleShootingTool(),
	}

	allTools := []server.ServerTool{}
	allTools = append(allTools, azdTools...)

	// Start gRPC server for extension communication
	serverInfo, err := a.grpcServer.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start gRPC server: %w", err)
	}

	defer a.grpcServer.Stop()

	// Load tools from extensions with MCP server capability
	extensionTools, err := a.loadExtensionTools(ctx, serverInfo)
	if err != nil {
		log.Printf("Warning: Failed to load extension tools: %v", err)
		// Continue with built-in tools only
	} else {
		allTools = append(allTools, extensionTools...)
	}

	mcpServer.AddTools(allTools...)

	// Start the server using stdio transport
	if err := server.ServeStdio(mcpServer); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}

	return nil, nil
}

// loadExtensionTools discovers and loads MCP tools from installed extensions
func (a *mcpStartAction) loadExtensionTools(ctx context.Context, serverInfo *grpcserver.ServerInfo) ([]server.ServerTool, error) {
	var allExtensionTools []server.ServerTool

	// Get all installed extensions
	installedExtensions, err := a.extensionManager.ListInstalled()
	if err != nil {
		return nil, fmt.Errorf("failed to get installed extensions: %w", err)
	}

	// Find extensions with MCP server capability
	for _, ext := range installedExtensions {
		if ext.HasCapability(extensions.McpServerCapability) {
			log.Printf("Loading MCP tools from extension: %s", ext.Id)

			extensionTools, err := a.loadToolsFromExtension(ctx, ext, serverInfo)
			if err != nil {
				log.Printf("Failed to load tools from extension %s: %v", ext.Id, err)
				continue // Continue with other extensions
			}

			allExtensionTools = append(allExtensionTools, extensionTools...)
		}
	}

	return allExtensionTools, nil
}

// loadToolsFromExtension connects to a single extension's MCP server and loads its tools
func (a *mcpStartAction) loadToolsFromExtension(ctx context.Context, ext *extensions.Extension, serverInfo *grpcserver.ServerInfo) ([]server.ServerTool, error) {
	// Get extension executable path
	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user config directory: %w", err)
	}

	extensionPath := filepath.Join(userConfigDir, ext.Path)
	if _, err := os.Stat(extensionPath); err != nil {
		return nil, fmt.Errorf("extension path '%s' not found: %w", extensionPath, err)
	}

	// Use convention-over-configuration for MCP args and env
	var args []string
	var configEnv []string

	if ext.McpConfig != nil && len(ext.McpConfig.Serve.Args) > 0 {
		// Use explicitly configured args
		args = append([]string{}, ext.McpConfig.Serve.Args...)
		configEnv = append([]string{}, ext.McpConfig.Serve.Env...)
	} else {
		// Use default convention: ["mcp", "start"]
		args = []string{"mcp", "start"}
		configEnv = []string{}
	}

	// Always layer in AZD extension environment variables
	azdEnv, err := a.getExtensionEnvironment(ctx, ext, serverInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to get AZD extension environment variables")
	}

	env := append(os.Environ(), configEnv...)
	env = append(env, azdEnv...)

	// Create & Start stdio transport for the extension MCP server
	mcpClient, err := client.NewStdioMCPClient(extensionPath, env, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to create or start the stdio MCP client for extension %s: %w", ext.Id, err)
	}

	// Initialize MCP connection
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "Azure Developer CLI (azd)",
		Version: "1.0.1",
	}

	initResult, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		mcpClient.Close()
		return nil, fmt.Errorf("failed to initialize MCP connection for extension %s: %w", ext.Id, err)
	}

	log.Printf("Connected to extension MCP server: %s (%s)", initResult.ServerInfo.Name, initResult.ServerInfo.Version)

	// Get tools from the extension's MCP server
	toolsRequest := mcp.ListToolsRequest{}
	toolsResult, err := mcpClient.ListTools(ctx, toolsRequest)
	if err != nil {
		mcpClient.Close()
		return nil, fmt.Errorf("failed to list tools from extension %s: %w", ext.Id, err)
	}

	// Convert extension ID to snake_case for tool prefixing
	toolPrefix := convertToSnakeCase(ext.Id)

	// Create proxy tools for each tool from the extension
	var extensionTools []server.ServerTool
	for _, mcpTool := range toolsResult.Tools {
		// Create prefixed tool name to avoid conflicts
		prefixedName := fmt.Sprintf("%s_%s", toolPrefix, mcpTool.Name)

		proxyTool := createExtensionProxyTool(prefixedName, mcpTool, mcpClient)
		extensionTools = append(extensionTools, proxyTool)

		log.Printf("Registered extension tool: %s (from %s)", prefixedName, ext.Id)
	}

	return extensionTools, nil
}

// getExtensionEnvironment prepares AZD environment variables for extensions
func (a *mcpStartAction) getExtensionEnvironment(ctx context.Context, ext *extensions.Extension, serverInfo *grpcserver.ServerInfo) ([]string, error) {
	jwtToken, err := grpcserver.GenerateExtensionToken(ext, serverInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to generate extension token: %w", err)
	}

	env := []string{
		fmt.Sprintf("AZD_SERVER=%s", serverInfo.Address),
		fmt.Sprintf("AZD_ACCESS_TOKEN=%s", jwtToken),
	}

	// Add color support if enabled
	if !color.NoColor {
		env = append(env, "FORCE_COLOR=1")
	}

	return env, nil
}

// convertToSnakeCase converts a dot-separated string to snake_case
func convertToSnakeCase(input string) string {
	return strings.ReplaceAll(input, ".", "_")
}

// createExtensionProxyTool creates a proxy tool that forwards calls to the extension's MCP server
func createExtensionProxyTool(toolName string, mcpTool mcp.Tool, mcpClient client.MCPClient) server.ServerTool {
	// Build tool options starting with description
	toolOptions := []mcp.ToolOption{
		mcp.WithDescription(mcpTool.Description),
	}

	// Copy annotations from the original tool if they exist
	if mcpTool.Annotations.ReadOnlyHint != nil {
		toolOptions = append(toolOptions, mcp.WithReadOnlyHintAnnotation(*mcpTool.Annotations.ReadOnlyHint))
	}
	if mcpTool.Annotations.IdempotentHint != nil {
		toolOptions = append(toolOptions, mcp.WithIdempotentHintAnnotation(*mcpTool.Annotations.IdempotentHint))
	}
	if mcpTool.Annotations.DestructiveHint != nil {
		toolOptions = append(toolOptions, mcp.WithDestructiveHintAnnotation(*mcpTool.Annotations.DestructiveHint))
	}
	if mcpTool.Annotations.OpenWorldHint != nil {
		toolOptions = append(toolOptions, mcp.WithOpenWorldHintAnnotation(*mcpTool.Annotations.OpenWorldHint))
	}

	return server.ServerTool{
		Tool: mcp.NewTool(toolName, toolOptions...),
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Forward the tool call to the extension's MCP server
			// Note: We need to use the original tool name when forwarding
			originalRequest := request
			originalRequest.Params.Name = mcpTool.Name

			return mcpClient.CallTool(ctx, originalRequest)
		},
	}
}

// Flags for MCP consent list command
type mcpConsentListFlags struct {
	global     *internal.GlobalCommandOptions
	scope      string
	target     string
	operation  string
	action     string
	permission string
}

func newMcpConsentListFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *mcpConsentListFlags {
	flags := &mcpConsentListFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *mcpConsentListFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
	local.StringVar(
		&f.scope,
		"scope",
		"",
		"Consent scope to filter by (global, project). If not specified, lists rules from all scopes.",
	)
	local.StringVar(&f.target, "target", "", "Specific target to operate on (server/tool format)")
	local.StringVar(&f.operation, "operation", "", "Operation to filter by (tool, sampling)")
	local.StringVar(&f.action, "action", "", "Action type to filter by (readonly, any)")
	local.StringVar(&f.permission, "permission", "", "Permission to filter by (allow, deny, prompt)")
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
	scope      string
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
	local.StringVar(&f.scope, "scope", "global", "Rule scope: 'global', or 'project'")
}

// Flags for MCP consent revoke command
type mcpConsentRevokeFlags struct {
	global     *internal.GlobalCommandOptions
	scope      string
	target     string
	operation  string
	action     string
	permission string
}

func newMcpConsentRevokeFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *mcpConsentRevokeFlags {
	flags := &mcpConsentRevokeFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *mcpConsentRevokeFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
	local.StringVar(
		&f.scope,
		"scope",
		"",
		"Consent scope to filter by (global, project). If not specified, revokes rules from all scopes.",
	)
	local.StringVar(&f.target, "target", "", "Specific target to operate on (server/tool format)")
	local.StringVar(&f.operation, "operation", "", "Operation to filter by (tool, sampling)")
	local.StringVar(&f.action, "action", "", "Action type to filter by (readonly, any)")
	local.StringVar(&f.permission, "permission", "", "Permission to filter by (allow, deny, prompt)")
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
	// Build filter options based on provided flags
	var filterOptions []consent.FilterOption

	// Add scope filter if provided
	if a.flags.scope != "" {
		scope, err := consent.ParseScope(a.flags.scope)
		if err != nil {
			return nil, err
		}
		filterOptions = append(filterOptions, consent.WithScope(scope))
	}

	// Add operation filter if provided
	if a.flags.operation != "" {
		operation, parseErr := consent.ParseOperationType(a.flags.operation)
		if parseErr != nil {
			return nil, parseErr
		}
		filterOptions = append(filterOptions, consent.WithOperation(operation))
	}

	// Add target filter if provided
	if a.flags.target != "" {
		target := consent.Target(a.flags.target)
		filterOptions = append(filterOptions, consent.WithTarget(target))
	}

	// Add action filter if provided
	if a.flags.action != "" {
		action, parseErr := consent.ParseActionType(a.flags.action)
		if parseErr != nil {
			return nil, parseErr
		}
		filterOptions = append(filterOptions, consent.WithAction(action))
	}

	// Add permission filter if provided
	if a.flags.permission != "" {
		permission, parseErr := consent.ParsePermission(a.flags.permission)
		if parseErr != nil {
			return nil, parseErr
		}
		filterOptions = append(filterOptions, consent.WithPermission(permission))
	}

	// Get rules with filters
	rules, err := a.consentManager.ListConsentRules(ctx, filterOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to list consent rules: %w", err)
	}

	if len(rules) == 0 {
		filterDesc := formatConsentDescription(
			a.flags.scope,
			a.flags.action,
			a.flags.operation,
			a.flags.target,
			a.flags.permission,
		)

		if filterDesc != "" {
			fmt.Fprintf(a.writer, "No consent rules found matching filters: %s.\n", filterDesc)
		} else {
			fmt.Fprintf(a.writer, "No consent rules found.\n")
		}
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
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Revoke MCP consent rules (azd mcp consent revoke)",
		TitleNote: "Removes consent rules for MCP tools and servers",
	})

	a.console.Message(ctx, "")

	// Build filter options based on provided flags
	var filterOptions []consent.FilterOption

	// Add scope filter if provided
	if a.flags.scope != "" {
		scope, err := consent.ParseScope(a.flags.scope)
		if err != nil {
			return nil, err
		}
		filterOptions = append(filterOptions, consent.WithScope(scope))
	}

	// Add operation filter if provided
	if a.flags.operation != "" {
		operation, parseErr := consent.ParseOperationType(a.flags.operation)
		if parseErr != nil {
			return nil, parseErr
		}
		filterOptions = append(filterOptions, consent.WithOperation(operation))
	}

	// Add target filter if provided
	if a.flags.target != "" {
		target := consent.Target(a.flags.target)
		filterOptions = append(filterOptions, consent.WithTarget(target))
	}

	// Add action filter if provided
	if a.flags.action != "" {
		action, parseErr := consent.ParseActionType(a.flags.action)
		if parseErr != nil {
			return nil, parseErr
		}
		filterOptions = append(filterOptions, consent.WithAction(action))
	}

	// Add permission filter if provided
	if a.flags.permission != "" {
		permission, parseErr := consent.ParsePermission(a.flags.permission)
		if parseErr != nil {
			return nil, parseErr
		}
		filterOptions = append(filterOptions, consent.WithPermission(permission))
	}

	// Build confirmation message based on filters
	filterDesc := formatConsentDescription(
		a.flags.scope,
		a.flags.action,
		a.flags.operation,
		a.flags.target,
		a.flags.permission,
	)

	var confirmMessage string
	if filterDesc != "" {
		confirmMessage = fmt.Sprintf("Are you sure you want to revoke consent rules for %s?", filterDesc)
	} else {
		confirmMessage = "Are you sure you want to revoke all consent rules?"
	}

	// Get confirmation
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

	// Clear rules with filters
	err := a.consentManager.ClearConsentRules(ctx, filterOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to clear consent rules: %w", err)
	}

	// Success message
	if filterDesc != "" {
		fmt.Fprintf(a.console.Handles().Stdout, "Revoked consent rules for %s.\n", filterDesc)
	} else {
		fmt.Fprintf(a.console.Handles().Stdout, "Revoked all consent rules.\n")
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Consent rules revoked successfully",
		},
	}, nil
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
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Grant MCP consent rules (azd mcp consent grant)",
		TitleNote: "Creates consent rules that allow MCP tools to execute without prompting",
	})

	a.console.Message(ctx, "")

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
	ruleScope, err := consent.ParseScope(a.flags.scope)
	if err != nil {
		return nil, err
	}

	// For sampling context, tool-specific grants are not supported
	if operation == consent.OperationTypeSampling && a.flags.tool != "" {
		return nil, fmt.Errorf("--tool is not supported for sampling rules")
	}

	// Build target
	var target consent.Target

	if a.flags.globalFlag {
		target = consent.NewGlobalTarget()
	} else if a.flags.tool != "" {
		target = consent.NewToolTarget(a.flags.server, a.flags.tool)
	} else {
		target = consent.NewServerTarget(a.flags.server)
	}

	rule := consent.ConsentRule{
		Scope:      ruleScope,
		Target:     target,
		Action:     actionType,
		Operation:  operation,
		Permission: permission,
	}

	// Generate description using helper function
	description := formatConsentRuleDescription(rule)

	if err := a.consentManager.GrantConsent(ctx, rule); err != nil {
		return nil, fmt.Errorf("failed to grant consent: %w", err)
	}

	fmt.Fprintf(a.console.Handles().Stdout, "Granted rule for %s\n", description)

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Consent rule granted successfully",
		},
	}, nil
}

// formatConsentDescription creates a simple description with whatever parts exist
func formatConsentDescription(scope, action, operation, target, permission string) string {
	var parts []string

	if scope != "" {
		parts = append(parts, fmt.Sprintf("Scope: %s", scope))
	}
	if target != "" {
		parts = append(parts, fmt.Sprintf("Target: %s", target))
	}
	if operation != "" {
		parts = append(parts, fmt.Sprintf("Context: %s", operation))
	}
	if action != "" {
		parts = append(parts, fmt.Sprintf("Action: %s", action))
	}
	if permission != "" {
		parts = append(parts, fmt.Sprintf("Permission: %s", permission))
	}

	return strings.Join(parts, ", ")
}

// Legacy wrapper for backward compatibility
func formatConsentRuleDescription(rule consent.ConsentRule) string {
	return formatConsentDescription(
		string(rule.Scope),
		string(rule.Action),
		string(rule.Operation),
		string(rule.Target),
		string(rule.Permission),
	)
}
