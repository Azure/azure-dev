// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type invocationOperation string

const (
	invocationShow   invocationOperation = "show"
	invocationFollow invocationOperation = "follow"
	invocationCancel invocationOperation = "cancel"
)

type invocationCommandFlags struct {
	userIdentityFlags
	agentName     string
	agentEndpoint string
	id            string
	protocol      string
	version       string
	clientHeaders []string
	noPrompt      bool
	output        string
}

func newInvocationsCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	cmd := &cobra.Command{
		Use:   "invocations",
		Short: "Inspect and manage work created by invoking an agent.",
		Long: `Inspect and manage work created by 'azd ai agent invoke'.

The protocol is inferred from the selected agent or --agent-endpoint.
Use --protocol when the agent supports multiple protocols. Available operations
and service results depend on the protocol; unsupported operations are rejected.

When --id is omitted, use the latest ID saved for the selected agent and
protocol. Explicit IDs do not change the saved selection.`,
	}
	cmd.AddCommand(newInvocationsShowCommand(extCtx))
	cmd.AddCommand(newInvocationsFollowCommand(extCtx))
	cmd.AddCommand(newInvocationsCancelCommand(extCtx))
	return cmd
}

func newInvocationsShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newInvocationOperationCommand(extCtx, invocationShow, "Show the service result for an invocation.")
}

func newInvocationsFollowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newInvocationOperationCommand(extCtx, invocationFollow, "Replay and follow invocation output.")
}

func newInvocationsCancelCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newInvocationOperationCommand(extCtx, invocationCancel, "Request cancellation of an invocation.")
}

func newInvocationOperationCommand(
	extCtx *azdext.ExtensionContext,
	operation invocationOperation,
	short string,
) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &invocationCommandFlags{}
	cmd := &cobra.Command{
		Use:   string(operation),
		Short: short,
		Args:  cobra.NoArgs,
		Example: fmt.Sprintf(`  # Use the current ID for the selected agent and protocol
  azd ai agent invocations %s

  # Target a Responses resource explicitly
  azd ai agent invocations %s --protocol responses --id <id>`, operation, operation),
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.noPrompt = extCtx.NoPrompt
			flags.output = extCtx.OutputFormat
			if err := validateInvocationCommandFlags(cmd, flags); err != nil {
				return err
			}
			return runInvocationOperation(azdext.WithAccessToken(cmd.Context()), flags, operation, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVarP(&flags.agentName, "agent-name", "n", "",
		"Agent name (matches azure.yaml service name; auto-detected when only one exists)")
	cmd.Flags().StringVar(&flags.agentEndpoint, "agent-endpoint", "", "Full protocol endpoint URL of a deployed agent")
	cmd.Flags().StringVar(&flags.id, "id", "", "Service-assigned ID; defaults to the current ID for the agent and protocol")
	cmd.Flags().StringVarP(&flags.protocol, "protocol", "p", "",
		"Protocol to use: responses, invocations, or a2a (inferred from agent; operation support varies)")
	cmd.Flags().StringVar(&flags.version, "version", "", "Agent version used to select saved invocation state")
	cmd.Flags().StringArrayVar(&flags.clientHeaders, "client-header", nil,
		`Custom x-client-* request header in "Name: Value" format (repeatable)`)
	addUserIdentityFlag(cmd, &flags.userIdentityFlags)

	formats, defaultFormat := []string{outputDefault}, outputDefault
	if operation == invocationShow {
		formats, defaultFormat = []string{"json", "table"}, "json"
	}
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: formats, Default: defaultFormat,
	})
	return cmd
}

func validateInvocationCommandFlags(cmd *cobra.Command, flags *invocationCommandFlags) error {
	for _, name := range []string{"id", "protocol", "agent-name", "agent-endpoint", "version"} {
		value, err := cmd.Flags().GetString(name)
		if err != nil {
			return err
		}
		if cmd.Flags().Changed(name) && strings.TrimSpace(value) == "" {
			return exterrors.Validation(exterrors.CodeInvalidParameter,
				fmt.Sprintf("--%s requires a non-empty value", name), "provide a value or omit the flag")
		}
	}
	if flags.agentEndpoint != "" {
		for _, name := range []string{"agent-name", "protocol", "version"} {
			if cmd.Flags().Changed(name) {
				return exterrors.Validation(exterrors.CodeConflictingArguments,
					fmt.Sprintf("--agent-endpoint cannot be combined with --%s", name),
					"the endpoint identifies the agent and protocol; remove the conflicting flag")
			}
		}
	}
	if flags.version != "" {
		if err := validateInvokeVersionValue(flags.version); err != nil {
			return exterrors.Validation(exterrors.CodeInvalidAgentVersion, err.Error(), "provide a valid agent version")
		}
	}
	return nil
}

// supportsInvocationOperation describes implemented CLI operations, not the
// capabilities of every deployed agent. The service may still reject a request.
func supportsInvocationOperation(protocol agent_api.AgentProtocol, operation invocationOperation) bool {
	return protocol == agent_api.AgentProtocolResponses &&
		(operation == invocationShow || operation == invocationFollow || operation == invocationCancel)
}

// resolveInvocationCommand resolves a protocol before reading its current ID.
// It never creates a session/conversation or changes the current selection.
func resolveInvocationCommand(
	ctx context.Context,
	flags *invocationCommandFlags,
	operation invocationOperation,
) (*InvokeAction, *remoteContext, string, error) {
	headers, err := parseCustomHeaders(flags.clientHeaders)
	if err != nil {
		return nil, nil, "", err
	}
	invokeFlags := &invokeFlags{
		name: flags.agentName, agentEndpoint: flags.agentEndpoint, version: flags.version, protocol: flags.protocol,
		userIdentityFlags: flags.userIdentityFlags,
	}
	action := &InvokeAction{flags: invokeFlags, noPrompt: flags.noPrompt, clientHeaders: headers}
	if flags.agentEndpoint != "" {
		parsed, err := parseAgentEndpoint(flags.agentEndpoint)
		if err != nil {
			return nil, nil, "", err
		}
		action.endpoint = parsed
		invokeFlags.name = parsed.AgentName
		invokeFlags.protocol = string(parsed.Protocol)
	}
	protocol, err := action.resolveProtocol(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	if !supportsInvocationOperation(protocol, operation) {
		return nil, nil, "", exterrors.Validation(exterrors.CodeInvalidParameter,
			fmt.Sprintf("invocations %s is not supported with the %s protocol", operation, protocol),
			"select a protocol that supports this operation")
	}
	action.flags.protocol = string(protocol)
	rc, err := action.resolveRemoteContext(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	id, err := resolveCurrentInvocationID(ctx, rc, protocol, flags.id)
	if err != nil {
		if rc.azdClient != nil {
			rc.azdClient.Close()
		}
		return nil, nil, "", err
	}
	return action, rc, id, nil
}

func resolveCurrentInvocationID(
	ctx context.Context,
	rc *remoteContext,
	protocol agent_api.AgentProtocol,
	explicitID string,
) (string, error) {
	if explicitID != "" {
		return explicitID, nil
	}
	if rc.azdClient == nil || rc.agentKey == "" {
		return "", exterrors.Validation(exterrors.CodeInvalidParameter,
			"current invocation state is unavailable", "provide --id or run through azd after invoking the agent")
	}
	var id string
	switch protocol {
	case agent_api.AgentProtocolResponses:
		record, err := newUserConfigResponseStateStore(rc.azdClient).Get(ctx, rc.agentKey)
		if err != nil {
			return "", classifyResponseStateReadError(err)
		}
		if record != nil {
			id = record.ResponseID
		}
	}
	if id == "" {
		return "", exterrors.Validation(exterrors.CodeInvalidParameter,
			fmt.Sprintf("no current invocation is saved for the selected agent and %s protocol", protocol),
			"invoke the agent first or provide --id")
	}
	return id, nil
}

func runInvocationOperation(
	ctx context.Context,
	flags *invocationCommandFlags,
	operation invocationOperation,
	writer io.Writer,
) error {
	action, rc, id, err := resolveInvocationCommand(ctx, flags, operation)
	if err != nil {
		return err
	}
	if rc.azdClient != nil {
		defer rc.azdClient.Close()
	}
	return action.runInvocationOperation(ctx, rc, id, operation, flags.output, writer)
}

func (a *InvokeAction) runInvocationOperation(
	ctx context.Context,
	rc *remoteContext,
	id string,
	operation invocationOperation,
	format string,
	writer io.Writer,
) error {
	switch agent_api.AgentProtocol(a.flags.protocol) {
	case agent_api.AgentProtocolResponses:
		return a.runResponseOperation(ctx, rc, id, operation, format, writer)
	default:
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			"unsupported invocation protocol", "select a protocol that supports this operation")
	}
}
