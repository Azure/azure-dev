// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	// added for MCP server functionality
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

func newStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Get the context of the AZD project & environment.",
		RunE: func(cmd *cobra.Command, args []string) error {
			mcpServer := server.NewMCPServer("azd", "0.0.1",
				server.WithInstructions(
					"Provides tools to dynamically run the AZD (Azure Developer CLI) commands."+
						"If a tool accepts a 'cwd', send the current working directory as the 'cwd' argument.",
				),
				server.WithLogging(),
				server.WithHooks(&server.Hooks{
					OnBeforeCallTool: []server.OnBeforeCallToolFunc{onBeforeTool},
				}),
			)

			registerTools(mcpServer)

			log.Println("Starting MCP server...")

			if err := server.ServeStdio(mcpServer); err != nil {
				return err
			}

			return nil
		},
	}
}

func registerTools(s *server.MCPServer) {
	initTool := mcp.NewTool("azd-init",
		mcp.WithDescription("Initializes a new azd project"),
		mcp.WithString("subscription", mcp.Description("The Azure subscription ID to use for provisioning and deployment.")),
		mcp.WithString("location", mcp.Description("The primary Azure location to use for the infrastructure.")),
		mcp.WithString("template", mcp.Description("The azd template or git repository to use")),
		mcp.WithString("cwd",
			mcp.Description("The azd project directory"),
			mcp.Required(),
			mcp.DefaultString("."),
		),
		mcp.WithString("environment", mcp.Description("The azd environment to use")),
	)

	provisionTool := mcp.NewTool("azd-provision",
		mcp.WithDescription(
			"Provisions infrastructure the resources for the azd project. "+
				"If the environment does not contain a location and subscription we'll need to set those first."),
		mcp.WithBoolean("preview", mcp.DefaultBool(false)),
		mcp.WithBoolean("skipState", mcp.DefaultBool(false)),
		mcp.WithString("cwd",
			mcp.Description("The azd project directory"),
			mcp.Required(),
			mcp.DefaultString("."),
		),
		mcp.WithString("environment", mcp.Description("The azd environment to use")),
	)

	envListTool := mcp.NewTool("azd-env-list",
		mcp.WithDescription("Lists the azd environments"),
		mcp.WithString("cwd",
			mcp.Description("The azd project directory"),
			mcp.Required(),
			mcp.DefaultString("."),
		),
		mcp.WithString("environment", mcp.Description("The azd environment to use")),
	)

	newEnvTool := mcp.NewTool("azd-env-new",
		mcp.WithDescription("Creates a new azd environment"),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("The name of the azd environment to create"),
		),
		mcp.WithString("cwd",
			mcp.Description("The azd project directory"),
			mcp.Required(),
			mcp.DefaultString("."),
		),
	)

	envGetValuesTool := mcp.NewTool("azd-env-get-values",
		mcp.WithDescription("Gets all the values of the azd environment"),
		mcp.WithString("cwd",
			mcp.Description("The azd project directory"),
			mcp.Required(),
			mcp.DefaultString("."),
		),
		mcp.WithString("environment", mcp.Description("The azd environment to use")),
	)

	envSetTool := mcp.NewTool("azd-env-set",
		mcp.WithDescription("Sets a key value pair for the current azd environment."),
		mcp.WithString("cwd",
			mcp.Description("The azd project directory"),
			mcp.Required(),
			mcp.DefaultString("."),
		),
		mcp.WithString("environment", mcp.Description("The azd environment to use")),
		mcp.WithString("value",
			mcp.Required(),
			mcp.Description("The value of the azd environment to set"),
		),
		mcp.WithString("key",
			mcp.Required(),
			mcp.Description("The key of the azd environment to set"),
		),
	)

	deployTool := mcp.NewTool("azd-deploy",
		mcp.WithDescription("Deploys the azd project. If the project was not provisioned, provision will need to happen first."),
		mcp.WithString("cwd",
			mcp.Description("The azd project directory"),
			mcp.Required(),
			mcp.DefaultString("."),
		),
		mcp.WithString("environment", mcp.Description("The azd environment to use")),
	)

	showTool := mcp.NewTool("azd-show",
		mcp.WithDescription("Shows the azd project configuration"),
		mcp.WithString("cwd",
			mcp.Description("The azd project directory"),
			mcp.Required(),
			mcp.DefaultString("."),
		),
		mcp.WithString("environment", mcp.Description("The azd environment to use")),
	)

	configShowTool := mcp.NewTool("azd-global-config",
		mcp.WithDescription("Shows the current azd global / user configuration"),
	)

	s.AddTool(initTool, invokeInit)
	s.AddTool(showTool, invokeShow)
	s.AddTool(provisionTool, invokeProvision)
	s.AddTool(deployTool, invokeDeploy)
	s.AddTool(configShowTool, invokeGlobalConfig)
	s.AddTool(envListTool, invokeEnvList)
	s.AddTool(newEnvTool, invokeNewEnv)
	s.AddTool(envGetValuesTool, invokeGetEnvValues)
	s.AddTool(envSetTool, invokeSetEnvValue)
}

func onBeforeTool(id any, request *mcp.CallToolRequest) {
	// This function is called before each tool is executed.
	// You can add any pre-processing logic here if needed.
	// For example, you can log the request or modify it.
	fmt.Printf("Preparing to call tool: %s\n", id)
	fmt.Printf("Request: %+v\n", request)
}

func invokeGetEnvValues(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !projectExists(request) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent("azd project not found. Run 'azd init' to create a new azd project."),
			},
			IsError: true,
		}, errors.New("azd project not found")
	}

	args := []string{"env", "get-values"}

	args = appendGlobalFlags(args, request)
	cmdResult, err := exec.Command("azd", args...).CombinedOutput()
	if err != nil {
		return nil, err
	}

	return mcp.NewToolResultText(string(cmdResult)), nil
}

func invokeSetEnvValue(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !projectExists(request) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent("azd project not found. Run 'azd init' to create a new azd project."),
			},
			IsError: true,
		}, errors.New("azd project not found")
	}

	args := []string{"env", "set"}

	key, hasKey := request.Params.Arguments["key"]
	if hasKey {
		args = append(args, key.(string))
	}

	value, hasValue := request.Params.Arguments["value"]
	if hasValue {
		args = append(args, value.(string))
	}

	args = appendGlobalFlags(args, request)
	cmdResult, err := exec.Command("azd", args...).CombinedOutput()
	if err != nil {
		return nil, err
	}

	return mcp.NewToolResultText(string(cmdResult)), nil
}

func invokeInit(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := []string{"init"}

	location, hasLocation := request.Params.Arguments["location"]
	if hasLocation {
		args = append(args, "--location", location.(string))
	}

	subscription, hasSubscription := request.Params.Arguments["subscription"]
	if hasSubscription {
		args = append(args, "--subscription", subscription.(string))
	}

	template, hasTemplate := request.Params.Arguments["template"]
	if hasTemplate {
		args = append(args, "--template", template.(string))
	}

	args = appendGlobalFlags(args, request)
	cmdResult, err := exec.Command("azd", args...).CombinedOutput()
	if err != nil {
		return nil, err
	}

	return mcp.NewToolResultText(string(cmdResult)), nil
}

func projectExists(request mcp.CallToolRequest) bool {
	cwd := "."
	cwdArg, hasCwd := request.Params.Arguments["cwd"]
	if hasCwd {
		cwd = cwdArg.(string)
	}

	projectFile := filepath.Join(cwd, "azure.yaml")
	if _, err := os.Stat(projectFile); err != nil {
		return false
	}

	return true
}

func invokeEnvList(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !projectExists(request) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent("azd project not found. Run 'azd init' to create a new azd project."),
			},
			IsError: true,
		}, errors.New("azd project not found")
	}

	args := []string{"env", "list"}

	args = appendGlobalFlags(args, request)
	cmdResult, err := exec.Command("azd", args...).CombinedOutput()
	if err != nil {
		return nil, err
	}

	return mcp.NewToolResultText(string(cmdResult)), nil
}

func invokeNewEnv(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !projectExists(request) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent("azd project not found. Run 'azd init' to create a new azd project."),
			},
			IsError: true,
		}, errors.New("azd project not found")
	}

	args := []string{"env", "new"}

	name, hasName := request.Params.Arguments["name"]
	if hasName {
		args = append(args, name.(string))
	}

	args = appendGlobalFlags(args, request)
	cmdResult, err := exec.Command("azd", args...).CombinedOutput()
	if err != nil {
		return nil, err
	}

	return mcp.NewToolResultText(string(cmdResult)), nil
}

func invokeShow(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !projectExists(request) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent("azd project not found. Run 'azd init' to create a new azd project."),
			},
			IsError: true,
		}, errors.New("azd project not found")
	}

	args := []string{"show"}

	args = appendGlobalFlags(args, request)
	cmdResult, err := exec.Command("azd", args...).CombinedOutput()
	if err != nil {
		return nil, err
	}

	return mcp.NewToolResultText(string(cmdResult)), nil
}

func invokeGlobalConfig(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := []string{"config", "show"}

	args = appendGlobalFlags(args, request)
	cmdResult, err := exec.Command("azd", args...).CombinedOutput()
	if err != nil {
		return nil, err
	}

	return mcp.NewToolResultText(string(cmdResult)), nil
}

func invokeProvision(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !projectExists(request) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent("azd project not found. Run 'azd init' to create a new azd project."),
			},
			IsError: true,
		}, errors.New("azd project not found")
	}

	args := []string{"provision"}

	preview, hasPreview := request.Params.Arguments["preview"]
	if hasPreview && preview.(bool) {
		args = append(args, "--preview")
	}

	skipState, hasSkipState := request.Params.Arguments["skipState"]
	if hasSkipState && skipState.(bool) {
		args = append(args, "--no-state")
	}

	args = appendGlobalFlags(args, request)
	cmdResult, err := exec.Command("azd", args...).CombinedOutput()
	if err != nil {
		return nil, err
	}

	return mcp.NewToolResultText(string(cmdResult)), nil
}

func invokeDeploy(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !projectExists(request) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent("azd project not found. Run 'azd init' to create a new azd project."),
			},
			IsError: true,
		}, errors.New("azd project not found")
	}

	args := []string{"deploy"}

	serviceName, hasServiceName := request.Params.Arguments["serviceName"]
	if hasServiceName {
		args = append(args, serviceName.(string))
	}

	args = appendGlobalFlags(args, request)
	cmdResult, err := exec.Command("azd", args...).CombinedOutput()
	if err != nil {
		return nil, err
	}

	return mcp.NewToolResultText(string(cmdResult)), nil
}

func appendGlobalFlags(args []string, request mcp.CallToolRequest) []string {
	cwd, hasCwd := request.Params.Arguments["cwd"]
	if hasCwd {
		args = append(args, "--cwd", fmt.Sprintf("%s", cwd.(string)))
	}

	environment, hasEnvironment := request.Params.Arguments["environment"]
	if hasEnvironment {
		args = append(args, "-e", fmt.Sprintf("%s", environment.(string)))
	}

	if request.Params.Arguments["debug"] != nil {
		args = append(args, "--debug")
	}

	return args
}
