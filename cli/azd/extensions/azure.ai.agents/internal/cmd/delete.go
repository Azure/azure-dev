// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type deleteFlags struct {
	name     string
	version  string
	force    bool
	output   string
	noPrompt bool
}

func newDeleteCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &deleteFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a hosted agent.",
		Long: `Delete a hosted agent and all of its versions.

If --version is specified, only that version is deleted (the agent itself remains).

If the agent has active sessions, deletion will fail unless --force is passed.
Use --force to terminate active sessions and delete the agent.

The agent name is resolved from the azd environment when omitted.`,
		Example: `  # Delete agent (auto-resolves name from azure.yaml)
  azd ai agent delete

  # Delete a specific agent by name
  azd ai agent delete my-agent

  # Delete a specific version only
  azd ai agent delete my-agent --version 2

  # Force-delete even if active sessions exist
  azd ai agent delete my-agent --force`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				flags.name = args[0]
			}
			flags.output = extCtx.OutputFormat
			flags.noPrompt = extCtx.NoPrompt

			ctx := azdext.WithAccessToken(cmd.Context())

			action := &DeleteAction{flags: flags}
			return action.Run(ctx)
		},
	}

	cmd.Flags().BoolVar(
		&flags.force, "force", false,
		"Force deletion even if the agent has active sessions",
	)

	cmd.Flags().StringVar(
		&flags.version, "version", "",
		"Delete a specific version only (the agent itself remains)",
	)

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "none"},
		Default:       "none",
	})

	return cmd
}

// DeleteAction implements the agent delete command.
type DeleteAction struct {
	flags *deleteFlags
}

func (a *DeleteAction) Run(ctx context.Context) error {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	info, err := resolveAgentServiceFromProject(ctx, azdClient, a.flags.name, a.flags.noPrompt)
	if err != nil {
		return err
	}

	agentName := info.AgentName
	if agentName == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentName,
			"agent name is required but could not be resolved",
			"ensure the agent has been deployed with 'azd deploy' first, "+
				"or provide the service name as a positional argument",
		)
	}

	// Confirmation prompt (skip in --no-prompt mode)
	if !a.flags.noPrompt {
		var message string
		if a.flags.version != "" && a.flags.force {
			message = fmt.Sprintf(
				"Force-delete version %q of agent %q? This will terminate active sessions on this version.",
				a.flags.version, agentName,
			)
		} else if a.flags.version != "" {
			message = fmt.Sprintf("Delete version %q of agent %q?", a.flags.version, agentName)
		} else if a.flags.force {
			message = fmt.Sprintf(
				"Force-delete agent %q? This will terminate all active sessions.",
				agentName,
			)
		} else {
			message = fmt.Sprintf("Delete agent %q and all its versions?", agentName)
		}
		defaultValue := false
		resp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      message,
				DefaultValue: &defaultValue,
			},
		})
		if promptErr != nil {
			if exterrors.IsCancellation(promptErr) {
				return exterrors.Cancelled("delete cancelled")
			}
			return fmt.Errorf("prompting for confirmation: %w", promptErr)
		}
		if resp.Value == nil || !*resp.Value {
			return exterrors.Cancelled("delete cancelled by user")
		}
	}

	endpoint, err := resolveAgentEndpoint(ctx, "", "")
	if err != nil {
		return err
	}

	credential, err := newAgentCredential()
	if err != nil {
		return err
	}

	client := agent_api.NewAgentClient(endpoint, credential)

	// Branch: delete a specific version vs the entire agent
	if a.flags.version != "" {
		result, err := client.DeleteAgentVersion(ctx, agentName, a.flags.version, DefaultAgentAPIVersion, a.flags.force)
		if err != nil {
			return classifyDeleteError(err, agentName)
		}
		switch a.flags.output {
		case "json":
			data, jsonErr := json.MarshalIndent(result, "", "  ")
			if jsonErr != nil {
				return fmt.Errorf("failed to marshal response: %w", jsonErr)
			}
			fmt.Println(string(data))
		default:
			fmt.Printf("Version %q of agent %q deleted.\n", a.flags.version, agentName)
		}
		return nil
	}

	result, err := client.DeleteAgent(ctx, agentName, DefaultAgentAPIVersion, a.flags.force)
	if err != nil {
		return classifyDeleteError(err, agentName)
	}

	// Best-effort: clean up saved session and conversation IDs (same as postdown hook).
	// Must run before cleanupEnvVars since it reads AGENT_{KEY}_ENDPOINT.
	if envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
		cleanupAgentSessionState(ctx, azdClient, envResp.Environment.Name, info.ServiceName)
	}

	// Best-effort: clear AGENT_{KEY}_NAME, AGENT_{KEY}_VERSION, AGENT_{KEY}_ENDPOINT env vars
	a.cleanupEnvVars(ctx, azdClient, info.ServiceName)

	switch a.flags.output {
	case "json":
		data, jsonErr := json.MarshalIndent(result, "", "  ")
		if jsonErr != nil {
			return fmt.Errorf("failed to marshal response: %w", jsonErr)
		}
		fmt.Println(string(data))
	default:
		fmt.Printf("Agent %q deleted.\n", agentName)
	}

	return nil
}

// cleanupEnvVars removes AGENT_{KEY}_NAME, AGENT_{KEY}_VERSION, and
// AGENT_{KEY}_ENDPOINT from the azd environment after a successful delete.
// The SDK has no DeleteValue API, so we set values to empty string as a workaround.
func (a *DeleteAction) cleanupEnvVars(ctx context.Context, azdClient *azdext.AzdClient, serviceName string) {
	if serviceName == "" {
		return
	}

	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return
	}
	envName := envResp.Environment.Name

	serviceKey := toServiceKey(serviceName)
	keys := []string{
		fmt.Sprintf("AGENT_%s_NAME", serviceKey),
		fmt.Sprintf("AGENT_%s_VERSION", serviceKey),
		fmt.Sprintf("AGENT_%s_ENDPOINT", serviceKey),
	}

	for _, key := range keys {
		if _, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envName,
			Key:     key,
			Value:   "",
		}); err != nil {
			log.Printf("delete: failed to clear env var %s: %v", key, err)
		}
	}
}

// classifyDeleteError maps Azure API errors from the delete operation into
// user-friendly typed errors. Exported for testing.
func classifyDeleteError(err error, agentName string) error {
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		switch respErr.StatusCode {
		case http.StatusNotFound:
			return exterrors.Validation(
				exterrors.CodeAgentNotFound,
				fmt.Sprintf("agent %q not found", agentName),
				"use 'azd ai agent show' to verify the agent exists",
			)
		case http.StatusConflict:
			return exterrors.Validation(
				exterrors.CodeAgentHasActiveSessions,
				fmt.Sprintf(
					"agent %q has active sessions and cannot be deleted",
					agentName,
				),
				"pass --force to terminate active sessions and delete the agent, "+
					"or delete sessions first with 'azd ai agent sessions delete'",
			)
		}
	}
	return exterrors.ServiceFromAzure(err, exterrors.OpDeleteAgent)
}
