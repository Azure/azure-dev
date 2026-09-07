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
	"net/url"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/project"

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
Use --force to terminate active sessions and delete the agent. In no-prompt
mode, --force is also required as explicit consent for deletion.

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
		"Force deletion even if the agent has active sessions; required as consent in no-prompt mode",
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

	if err := a.confirmDelete(ctx, azdClient, agentName); err != nil {
		return err
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
		a.clearDeletedVersionMarker(ctx, azdClient, info.ServiceName, a.flags.version, endpoint)
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

	// Best-effort: clean up saved session, conversation, and background Response state (same as postdown hook).
	// Must run before cleanupEnvVars since it reads AGENT_{KEY}_ENDPOINT.
	if envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
		cleanupAgentState(ctx, azdClient, envResp.Environment.Name, info.ServiceName)
	}

	// Best-effort: clear readiness and endpoint state after a successful delete.
	a.cleanupEnvVars(ctx, azdClient, info.ServiceName, endpoint)

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

func (a *DeleteAction) confirmDelete(ctx context.Context, azdClient *azdext.AzdClient, agentName string) error {
	if a.flags.noPrompt {
		if a.flags.force {
			return nil
		}
		return exterrors.Validation(
			exterrors.CodeDeleteRequiresForce,
			fmt.Sprintf("deleting agent %q requires explicit consent in no-prompt mode", agentName),
			"re-run with --force to confirm deletion",
		)
	}

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

	resp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      message,
			DefaultValue: new(false),
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

	return nil
}

// cleanupEnvVars removes agent readiness and endpoint values after a successful delete.
// The SDK has no DeleteValue API, so we set values to empty string as a workaround.
func (a *DeleteAction) cleanupEnvVars(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	serviceName string,
	deletedProjectEndpoint string,
) {
	if serviceName == "" {
		return
	}
	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || !agentMarkersBelongToProject(
		ctx, azdClient, envResp.Environment.Name, serviceName, deletedProjectEndpoint,
	) {
		return
	}
	serviceKey := toServiceKey(serviceName)
	keys := []string{
		fmt.Sprintf("AGENT_%s_NAME", serviceKey),
		fmt.Sprintf("AGENT_%s_VERSION", serviceKey),
		fmt.Sprintf("AGENT_%s_ENDPOINT", serviceKey),
		envkey.AgentProjectEndpoint(serviceName),
	}
	for _, protocol := range project.DisplayableProtocolEnvSuffixes() {
		keys = append(keys, fmt.Sprintf("AGENT_%s_%s_ENDPOINT", serviceKey, protocol.Suffix))
	}
	a.clearEnvVars(ctx, azdClient, serviceName, keys)
}

func (a *DeleteAction) clearDeletedVersionMarker(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	serviceName string,
	deletedVersion string,
	deletedProjectEndpoint string,
) {
	if serviceName == "" {
		return
	}
	versionKey := fmt.Sprintf("AGENT_%s_VERSION", toServiceKey(serviceName))
	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return
	}
	resp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envResp.Environment.Name,
		Key:     versionKey,
	})
	if err != nil || resp.Value != deletedVersion {
		return
	}
	if !agentMarkersBelongToProject(
		ctx, azdClient, envResp.Environment.Name, serviceName, deletedProjectEndpoint,
	) {
		return
	}
	serviceKey := toServiceKey(serviceName)
	keys := []string{
		versionKey,
		fmt.Sprintf("AGENT_%s_ENDPOINT", serviceKey),
	}
	for _, protocol := range project.DisplayableProtocolEnvSuffixes() {
		keys = append(keys, fmt.Sprintf("AGENT_%s_%s_ENDPOINT", serviceKey, protocol.Suffix))
	}
	a.clearEnvVars(ctx, azdClient, serviceName, keys)
}

func agentMarkersBelongToProject(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	serviceName string,
	deletedProjectEndpoint string,
) bool {
	projectResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     envkey.AgentProjectEndpoint(serviceName),
	})
	if err != nil {
		return false
	}
	markerEndpoint := projectResp.Value
	if strings.TrimSpace(markerEndpoint) == "" {
		endpointResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: envName,
			Key:     fmt.Sprintf("AGENT_%s_ENDPOINT", toServiceKey(serviceName)),
		})
		if err != nil {
			return false
		}
		markerEndpoint = endpointResp.Value
	}
	return sameAgentProjectEndpoint(markerEndpoint, deletedProjectEndpoint)
}

func sameAgentProjectEndpoint(a, b string) bool {
	if strings.EqualFold(
		strings.TrimRight(strings.TrimSpace(a), "/"),
		strings.TrimRight(strings.TrimSpace(b), "/"),
	) {
		return true
	}
	aHost, aProject := agentProjectIdentity(a)
	bHost, bProject := agentProjectIdentity(b)
	return aHost != "" && aProject != "" &&
		strings.EqualFold(aHost, bHost) && strings.EqualFold(aProject, bProject)
}

func agentProjectIdentity(endpoint string) (string, string) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Hostname() == "" {
		return "", ""
	}
	const segment = "/projects/"
	index := strings.Index(strings.ToLower(u.Path), segment)
	if index < 0 {
		return "", ""
	}
	projectName := strings.Split(strings.Trim(u.Path[index+len(segment):], "/"), "/")[0]
	return u.Hostname(), projectName
}

func (a *DeleteAction) clearEnvVars(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	serviceName string,
	keys []string,
) {
	if serviceName == "" {
		return
	}
	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return
	}
	envName := envResp.Environment.Name

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
