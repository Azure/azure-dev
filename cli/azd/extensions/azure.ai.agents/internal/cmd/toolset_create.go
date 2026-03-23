// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func newToolsetCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <path-to-toolset.json>",
		Short: "Create a toolset in the Foundry project.",
		Long: `Create a new toolset from a JSON payload file.

The payload file must contain a JSON object with at least "name" and "tools" fields.
If a toolset with the same name already exists, you will be prompted to confirm
before overwriting (use --no-prompt to auto-confirm).`,
		Example: `  # Create a toolset from a JSON file
  azd ai agent toolset create toolset.json

  # Create with auto-confirm for scripting
  azd ai agent toolset create toolset.json --no-prompt`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			if len(args) == 0 {
				return exterrors.Validation(
					exterrors.CodeInvalidToolsetPayload,
					"missing required payload file path",
					"Provide a path to a toolset JSON file, for example:\n"+
						"  azd ai agent toolset create path/to/toolset.json",
				)
			}

			payloadPath := args[0]

			// Read and parse the payload file
			data, err := os.ReadFile(payloadPath) //nolint:gosec // G304: path is from user CLI arg, validated below
			if err != nil {
				return exterrors.Validation(
					exterrors.CodeInvalidToolsetPayload,
					fmt.Sprintf("failed to read payload file '%s': %s", payloadPath, err),
					"Check that the file path is correct and the file is readable",
				)
			}

			var createReq agent_api.CreateToolsetRequest
			if err := json.Unmarshal(data, &createReq); err != nil {
				return exterrors.Validation(
					exterrors.CodeInvalidToolsetPayload,
					fmt.Sprintf("failed to parse payload file '%s': %s", payloadPath, err),
					"Ensure the file contains valid JSON with 'name' and 'tools' fields",
				)
			}

			if createReq.Name == "" {
				return exterrors.Validation(
					exterrors.CodeInvalidToolsetPayload,
					"toolset payload is missing required 'name' field",
					"Add a 'name' field to the JSON payload",
				)
			}
			if len(createReq.Tools) == 0 {
				return exterrors.Validation(
					exterrors.CodeInvalidToolsetPayload,
					"toolset payload is missing required 'tools' field or tools array is empty",
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

			// Check if toolset already exists
			existing, err := client.GetToolset(ctx, createReq.Name, agent_api.ToolsetAPIVersion)
			if err == nil && existing != nil {
				// Toolset exists — prompt for overwrite confirmation
				if !rootFlags.NoPrompt {
					azdClient, azdErr := azdext.NewAzdClient()
					if azdErr != nil {
						return fmt.Errorf("failed to create azd client for prompting: %w", azdErr)
					}
					defer azdClient.Close()

					resp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
						Options: &azdext.ConfirmOptions{
							Message: fmt.Sprintf(
								"Toolset '%s' already exists with %d tool(s). Overwrite?",
								existing.Name, len(existing.Tools),
							),
						},
					})
					if promptErr != nil {
						if exterrors.IsCancellation(promptErr) {
							return exterrors.Cancelled("toolset creation cancelled")
						}
						return fmt.Errorf("failed to prompt for confirmation: %w", promptErr)
					}
					if !*resp.Value {
						fmt.Println("Toolset creation cancelled.")
						return nil
					}
				}

				// Update the existing toolset
				updateReq := &agent_api.UpdateToolsetRequest{
					Description: createReq.Description,
					Metadata:    createReq.Metadata,
					Tools:       createReq.Tools,
				}

				toolset, updateErr := client.UpdateToolset(ctx, createReq.Name, updateReq, agent_api.ToolsetAPIVersion)
				if updateErr != nil {
					return exterrors.ServiceFromAzure(updateErr, exterrors.OpUpdateToolset)
				}

				mcpEndpoint := fmt.Sprintf("%s/toolsets/%s/mcp", endpoint, toolset.Name)
				fmt.Printf("Toolset '%s' updated successfully (%d tool(s)).\n", toolset.Name, len(toolset.Tools))
				fmt.Printf("MCP Endpoint: %s\n", mcpEndpoint)
				printMcpEnvTip(toolset.Name, mcpEndpoint)
				return nil
			}

			// Check if the error is a 404 (not found) — proceed with create
			var respErr *azcore.ResponseError
			if err != nil && !(errors.As(err, &respErr) && respErr.StatusCode == 404) {
				return exterrors.ServiceFromAzure(err, exterrors.OpGetToolset)
			}

			// Create new toolset
			toolset, createErr := client.CreateToolset(ctx, &createReq, agent_api.ToolsetAPIVersion)
			if createErr != nil {
				return exterrors.ServiceFromAzure(createErr, exterrors.OpCreateToolset)
			}

			mcpEndpoint := fmt.Sprintf("%s/toolsets/%s/mcp", endpoint, toolset.Name)
			fmt.Printf("Toolset '%s' created successfully (%d tool(s)).\n", toolset.Name, len(toolset.Tools))
			fmt.Printf("MCP Endpoint: %s\n", mcpEndpoint)
			printMcpEnvTip(toolset.Name, mcpEndpoint)
			return nil
		},
	}

	return cmd
}

// toolsetNameToEnvVar converts a toolset name to an environment variable name
// by upper-casing and replacing non-alphanumeric characters with underscores.
func toolsetNameToEnvVar(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func printMcpEnvTip(toolsetName, mcpEndpoint string) {
	envVar := toolsetNameToEnvVar(toolsetName) + "_MCP_ENDPOINT"
	fmt.Println()
	fmt.Println(output.WithHintFormat(
		"Hint: Store the endpoint in your azd environment so your agent code can reference it:"))
	fmt.Printf("  %s\n", output.WithHighLightFormat(
		"azd env set %s %s", envVar, mcpEndpoint))
}
