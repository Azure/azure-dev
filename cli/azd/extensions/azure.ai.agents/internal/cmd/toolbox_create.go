// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func newToolboxCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <path-to-toolbox.json>",
		Short: "Create a toolbox in the Foundry project.",
		Long: `Create a new toolbox from a JSON payload file.

The payload file must contain a JSON object with at least "name" and "tools" fields.
If a toolbox with the same name already exists, you will be prompted to confirm
before overwriting (use --no-prompt to auto-confirm).`,
		Example: `  # Create a toolbox from a JSON file
  azd ai agent toolbox create toolbox.json

  # Create with auto-confirm for scripting
  azd ai agent toolbox create toolbox.json --no-prompt`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			if len(args) == 0 {
				return exterrors.Validation(
					exterrors.CodeInvalidToolboxPayload,
					"missing required payload file path",
					"Provide a path to a toolbox JSON file, for example:\n"+
						"  azd ai agent toolbox create path/to/toolbox.json",
				)
			}

			payloadPath := args[0]

			// Read and parse the payload file
			data, err := os.ReadFile(payloadPath) //nolint:gosec // G304: path is from user CLI arg, validated below
			if err != nil {
				return exterrors.Validation(
					exterrors.CodeInvalidToolboxPayload,
					fmt.Sprintf("failed to read payload file '%s': %s", payloadPath, err),
					"Check that the file path is correct and the file is readable",
				)
			}

			var createReq agent_api.CreateToolboxRequest
			if err := json.Unmarshal(data, &createReq); err != nil {
				return exterrors.Validation(
					exterrors.CodeInvalidToolboxPayload,
					fmt.Sprintf("failed to parse payload file '%s': %s", payloadPath, err),
					"Ensure the file contains valid JSON with 'name' and 'tools' fields",
				)
			}

			if createReq.Name == "" {
				return exterrors.Validation(
					exterrors.CodeInvalidToolboxPayload,
					"toolbox payload is missing required 'name' field",
					"Add a 'name' field to the JSON payload",
				)
			}
			if len(createReq.Tools) == 0 {
				return exterrors.Validation(
					exterrors.CodeInvalidToolboxPayload,
					"toolbox payload is missing required 'tools' field or tools array is empty",
					"Add a 'tools' array with at least one tool definition",
				)
			}

			endpoint, err := resolveAgentEndpoint(ctx, "", "")
			if err != nil {
				return err
			}

			credential, err := newAgentCredential()
			if err != nil {
				return exterrors.Auth(
					exterrors.CodeCredentialCreationFailed,
					fmt.Sprintf("failed to create credential: %s", err),
					"Run 'azd auth login' to authenticate",
				)
			}

			client := agent_api.NewAgentClient(endpoint, credential)

			// Check if toolbox already exists
			existing, err := client.GetToolbox(ctx, createReq.Name, agent_api.ToolboxAPIVersion)
			if err == nil && existing != nil {
				// Toolbox exists — prompt for overwrite confirmation
				if !rootFlags.NoPrompt {
					azdClient, azdErr := azdext.NewAzdClient()
					if azdErr != nil {
						return fmt.Errorf("failed to create azd client for prompting: %w", azdErr)
					}
					defer azdClient.Close()

					resp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
						Options: &azdext.ConfirmOptions{
							Message: fmt.Sprintf(
								"Toolbox '%s' already exists with %d tool(s). Overwrite?",
								existing.Name, len(existing.Tools),
							),
						},
					})
					if promptErr != nil {
						if exterrors.IsCancellation(promptErr) {
							return exterrors.Cancelled("toolbox creation cancelled")
						}
						return fmt.Errorf("failed to prompt for confirmation: %w", promptErr)
					}
					if resp == nil || resp.Value == nil || !*resp.Value {
						fmt.Println("toolbox creation cancelled.")
						return nil
					}
				}

				// Update the existing toolbox
				updateReq := &agent_api.UpdateToolboxRequest{
					Description: createReq.Description,
					Metadata:    createReq.Metadata,
					Tools:       createReq.Tools,
				}

				toolbox, updateErr := client.UpdateToolbox(ctx, createReq.Name, updateReq, agent_api.ToolboxAPIVersion)
				if updateErr != nil {
					return exterrors.ServiceFromAzure(updateErr, exterrors.OpUpdateToolbox)
				}

				mcpEndpoint := project.ToolboxMcpEndpoint(endpoint, toolbox.Name)
				fmt.Printf("Toolbox '%s' updated successfully (%d tool(s)).\n", toolbox.Name, len(toolbox.Tools))
				fmt.Printf("MCP Endpoint: %s\n", mcpEndpoint)
				printMcpEnvTip(toolbox.Name, mcpEndpoint)
				return nil
			}

			// Check if the error is a 404 (not found) — proceed with create
			var respErr *azcore.ResponseError
			if err != nil && !(errors.As(err, &respErr) && respErr.StatusCode == 404) {
				return exterrors.ServiceFromAzure(err, exterrors.OpGetToolbox)
			}

			// Create new toolbox
			toolbox, createErr := client.CreateToolbox(ctx, &createReq, agent_api.ToolboxAPIVersion)
			if createErr != nil {
				return exterrors.ServiceFromAzure(createErr, exterrors.OpCreateToolbox)
			}

			mcpEndpoint := project.ToolboxMcpEndpoint(endpoint, toolbox.Name)
			fmt.Printf("Toolbox '%s' created successfully (%d tool(s)).\n", toolbox.Name, len(toolbox.Tools))
			fmt.Printf("MCP Endpoint: %s\n", mcpEndpoint)
			printMcpEnvTip(toolbox.Name, mcpEndpoint)
			return nil
		},
	}

	return cmd
}

func printMcpEnvTip(toolboxName, mcpEndpoint string) {
	envVar := project.ToolboxEnvVar(toolboxName)
	fmt.Println()
	fmt.Println(output.WithHintFormat(
		"Hint: Store the endpoint in your azd environment so your agent code can reference it:"))
	fmt.Printf("  %s\n", output.WithHighLightFormat(
		"azd env set %s %s", envVar, mcpEndpoint))
}
