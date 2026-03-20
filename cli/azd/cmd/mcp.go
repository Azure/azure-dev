// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/internal/mcp"
	"github.com/azure/azure-dev/cli/azd/internal/mcp/tools"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/drone/envsubst"
	"github.com/fatih/color"
	mmcp "github.com/mark3labs/mcp-go/mcp"
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
	// Start gRPC server for extension communication
	serverInfo, err := a.grpcServer.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start gRPC server: %w", err)
	}

	defer a.grpcServer.Stop()

	mcpHost, err := a.createMcpHost(ctx, serverInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to start MCP host: %w", err)
	}

	defer mcpHost.Stop()

	ts := telemetry.GetTelemetrySystem()
	if ts != nil {
		uploadTelemetry := func() {
			ticker := time.NewTicker(5 * time.Second)
			for {
				err := ts.RunBackgroundUpload(ctx, false)
				if err != nil {
					log.Printf("telemetry upload failed: %v", err)
				}

				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}
		go uploadTelemetry()
	}

	mcpServer := server.NewMCPServer(
		"azd MCP Server 🚀", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithElicitation(),
		server.WithHooks(mcpHost.Hooks()),
		server.WithToolHandlerMiddleware(func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
			return func(ctx context.Context, request mmcp.CallToolRequest) (result *mmcp.CallToolResult, err error) {
				ctx, span := tracing.Start(ctx, events.McpEventPrefix+request.Params.Name)
				if session := server.ClientSessionFromContext(ctx); session != nil {
					if sessionWithClientInfo, ok := session.(server.SessionWithClientInfo); ok {
						clientInfo := sessionWithClientInfo.GetClientInfo()
						span.SetAttributes(fields.McpClientName.String(clientInfo.Name))
						span.SetAttributes(fields.McpClientVersion.String(clientInfo.Version))
					}
				}

				result, err = next(ctx, request)
				span.EndWithStatus(err)

				return result, err
			}
		}),
	)

	mcpServer.EnableSampling()

	azdTools := []server.ServerTool{
		tools.NewAzdYamlSchemaTool(),
		tools.NewAzdErrorTroubleShootingTool(),
		tools.NewAzdProvisionCommonErrorTool(),
	}

	allTools := []server.ServerTool{}
	allTools = append(allTools, azdTools...)

	extensionTools, err := mcpHost.AllTools(ctx)
	if err != nil {
		return nil, &internal.ErrorWithSuggestion{
			Err:        internal.ErrMcpToolsLoadFailed,
			Suggestion: "Check that MCP extensions are installed and configured correctly.",
		}
	}

	allTools = append(allTools, extensionTools...)
	mcpServer.AddTools(allTools...)
	mcpHost.SetProxyServer(mcpServer)

	// Start the server using stdio transport
	if err := server.ServeStdio(mcpServer); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}

	return nil, nil
}

// createMcpHost creates an instance of the MCP host with registered extension servers
func (a *mcpStartAction) createMcpHost(ctx context.Context, serverInfo *grpcserver.ServerInfo) (*mcp.McpHost, error) {
	extensionServers, err := a.getExtensionServers(ctx, serverInfo)
	if err != nil {
		return nil, err
	}

	mcpHostOptions := []mcp.McpHostOption{
		mcp.WithServers(extensionServers),
		mcp.WithCapabilities(mcp.Capabilities{
			Sampling:    mcp.NewProxySamplingHandler(),
			Elicitation: mcp.NewProxyElicitationHandler(),
		}),
	}

	mcpHost := mcp.NewMcpHost(mcpHostOptions...)
	if err := mcpHost.Start(ctx); err != nil {
		return nil, err
	}

	return mcpHost, nil
}

// getExtensionServers gets the MCP server configuration for azd extensions that declare MCP server capabilities
func (a *mcpStartAction) getExtensionServers(
	ctx context.Context,
	serverInfo *grpcserver.ServerInfo,
) (map[string]*mcp.ServerConfig, error) {
	// Get all installed extensions
	installedExtensions, err := a.extensionManager.ListInstalled()
	if err != nil {
		return nil, fmt.Errorf("failed to get installed extensions: %w", err)
	}

	servers := map[string]*mcp.ServerConfig{}

	// Find extensions with MCP server capability
	for _, ext := range installedExtensions {
		if !ext.HasCapability(extensions.McpServerCapability) {
			continue
		}

		log.Printf("Loading tools from extension: %s", ext.Id)

		serverConfig, err := a.getExtensionServerConfig(ctx, ext, serverInfo)
		if err != nil {
			log.Printf("failed to get MCP server config for extension %s: %v", ext.Id, err)
		}

		serverName := fmt.Sprintf("azd_%s", strings.ReplaceAll(ext.Namespace, ".", "_"))
		servers[serverName] = serverConfig
	}

	return servers, nil
}

// loadToolsFromExtension connects to a single extension's MCP server and loads its tools
func (a *mcpStartAction) getExtensionServerConfig(
	ctx context.Context,
	ext *extensions.Extension,
	serverInfo *grpcserver.ServerInfo,
) (*mcp.ServerConfig, error) {
	// Get extension executable path
	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user config directory: %w", err)
	}

	extensionPath := filepath.Join(userConfigDir, ext.Path)
	if _, err := os.Stat(extensionPath); err != nil {
		return nil, fmt.Errorf("extension path '%s' not found: %w", extensionPath, err)
	}

	// Use convention-over-configuration for MCP args
	// Default to ["mcp", "start"] unless explicitly configured
	args := []string{"mcp", "start"}

	if ext.McpConfig != nil && len(ext.McpConfig.Server.Args) > 0 {
		args = ext.McpConfig.Server.Args
	}

	// Get all environment variables (custom + AZD)
	env, err := a.getExtensionEnvironment(ext, serverInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to get extension environment variables: %w", err)
	}

	return &mcp.ServerConfig{
		Type:    "stdio",
		Command: extensionPath,
		Args:    args,
		Env:     env,
	}, nil

}

// getExtensionEnvironment prepares environment variables for extensions
// This includes both custom environment variables from extension configuration
// and azd environment variables needed for the extension framework.
func (a *mcpStartAction) getExtensionEnvironment(
	ext *extensions.Extension,
	serverInfo *grpcserver.ServerInfo,
) ([]string, error) {
	var env []string

	// Process custom environment variables from extension configuration with expansion
	if ext.McpConfig != nil {
		for _, envVar := range ext.McpConfig.Server.Env {
			expandedVar, err := envsubst.Eval(envVar, os.Getenv)
			if err != nil {
				log.Printf("Warning: failed to expand environment variable '%s': %v", envVar, err)
				// Use the original value if expansion fails
				env = append(env, envVar)
			} else {
				env = append(env, expandedVar)
			}
		}
	}

	// Generate azd extension framework environment variables
	jwtToken, err := grpcserver.GenerateExtensionToken(ext, serverInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to generate extension token: %w", err)
	}

	azdEnv := []string{
		fmt.Sprintf("AZD_SERVER=%s", serverInfo.Address),
		fmt.Sprintf("AZD_ACCESS_TOKEN=%s", jwtToken),
	}

	// Add color support if enabled
	if !color.NoColor {
		azdEnv = append(azdEnv, "FORCE_COLOR=1")
	}

	// Combine custom environment variables with azd environment variables
	env = append(env, azdEnv...)

	return env, nil
}
