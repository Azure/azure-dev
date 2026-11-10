// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/featureflags"
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
	noPrompt      bool
	output        string
}

// newInvocationsCommand creates the shared invocation lifecycle command group.
func newInvocationsCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	cmd := &cobra.Command{
		Use:   "invocations",
		Short: "Inspect and manage work created by invoking an agent.",
		Long: `Inspect and manage work created by 'azd ai agent invoke'.

The protocol comes from --agent-endpoint or the latest saved invocation for
the selected target. Without saved work, use deployment routing. Older saved
state containing multiple protocols requires --protocol. Available operations
and service results depend on the protocol; unsupported operations are rejected.

When --id is omitted, use the latest ID saved for the selected agent and
protocol. Explicit IDs do not change the saved selection.`,
		Example: `  # Show the current invocation result
  azd ai agent invocations show`,
	}
	cmd.AddCommand(newInvocationsShowCommand(extCtx))
	cmd.AddCommand(newInvocationsFollowCommand(extCtx))
	cmd.AddCommand(newInvocationsCancelCommand(extCtx))
	// The group is hidden from help while long-running invocations are off.
	// Its subcommands keep running so existing work can be inspected.
	discoverCommand(cmd, featureflags.LongRunning)
	return cmd
}

// newInvocationsShowCommand creates the snapshot command.
func newInvocationsShowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newInvocationOperationCommand(extCtx, invocationShow, "Show the service result for an invocation.")
}

// newInvocationsFollowCommand creates the output-following command.
func newInvocationsFollowCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newInvocationOperationCommand(extCtx, invocationFollow, "Replay and follow invocation output.")
}

// newInvocationsCancelCommand creates the cancellation command.
func newInvocationsCancelCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newInvocationOperationCommand(extCtx, invocationCancel, "Request cancellation of an invocation.")
}

// newInvocationOperationCommand wires common flags and lifecycle dispatch.
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
		"Protocol to use: responses, invocations, or a2a "+
			"(inferred from current invocation or saved deployment; operation support varies)")
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

// validateInvocationCommandFlags rejects empty values and conflicting selectors.
func validateInvocationCommandFlags(cmd *cobra.Command, flags *invocationCommandFlags) error {
	for _, name := range []string{"id", "protocol", "agent-name", "agent-endpoint"} {
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
		for _, name := range []string{"agent-name", "protocol"} {
			if cmd.Flags().Changed(name) {
				return exterrors.Validation(exterrors.CodeConflictingArguments,
					fmt.Sprintf("--agent-endpoint cannot be combined with --%s", name),
					"the endpoint identifies the agent and protocol; remove the conflicting flag")
			}
		}
	}
	return nil
}

// supportsInvocationOperation describes implemented CLI operations, not the
// capabilities of every deployed agent. The service may still reject a request.
func supportsInvocationOperation(protocol agent_api.AgentProtocol, operation invocationOperation) bool {
	switch protocol {
	case agent_api.AgentProtocolResponses:
		return operation == invocationShow || operation == invocationFollow || operation == invocationCancel
	case agent_api.AgentProtocolInvocations:
		return operation == invocationShow || operation == invocationCancel
	default:
		return false
	}
}

// resolveInvocationCommand resolves a protocol before reading its current ID.
// It uses saved current-work selection, not live capabilities, and never creates
// a session/conversation or changes the current selection.
func resolveInvocationCommand(
	ctx context.Context,
	flags *invocationCommandFlags,
	operation invocationOperation,
) (*InvokeAction, *remoteContext, string, error) {
	invokeFlags := &invokeFlags{
		name: flags.agentName, agentEndpoint: flags.agentEndpoint, protocol: flags.protocol,
		userIdentityFlags: flags.userIdentityFlags,
	}
	action := &InvokeAction{flags: invokeFlags, noPrompt: flags.noPrompt}
	if flags.agentEndpoint != "" {
		parsed, err := parseAgentEndpoint(flags.agentEndpoint)
		if err != nil {
			return nil, nil, "", err
		}
		action.endpoint = parsed
		invokeFlags.name = parsed.AgentName
		invokeFlags.protocol = string(parsed.Protocol)
	}
	protocol := agent_api.AgentProtocol(invokeFlags.protocol)
	inferProtocol := protocol == ""
	var currentID string
	if inferProtocol {
		rc, err := action.resolveRemoteContextForInvoke(ctx)
		if err != nil {
			return nil, nil, "", err
		}
		action.resolvedRemoteContext = rc
		protocol, currentID, err = action.resolveSavedInvocation(ctx, rc)
		if err != nil {
			action.closeResolvedRemoteContextClient()
			return nil, nil, "", err
		}
	}
	if !supportsInvocationOperation(protocol, operation) {
		action.closeResolvedRemoteContextClient()
		return nil, nil, "", exterrors.Validation(exterrors.CodeInvalidParameter,
			fmt.Sprintf("invocations %s is not supported with the %s protocol", operation, protocol),
			"select a protocol that supports this operation")
	}
	action.flags.protocol = string(protocol)
	rc, err := action.resolveRemoteContextForInvoke(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	id := currentID
	if flags.id != "" || !inferProtocol {
		id, err = resolveCurrentInvocationID(ctx, rc, protocol, flags.id)
	} else if id == "" {
		err = noCurrentInvocationError(protocol)
	}
	if err != nil {
		if rc.azdClient != nil {
			rc.azdClient.Close()
		}
		return nil, nil, "", err
	}
	return action, rc, id, nil
}

// resolveSavedInvocation reads protocol and ID from the same saved-state snapshot.
func (a *InvokeAction) resolveSavedInvocation(
	ctx context.Context,
	rc *remoteContext,
) (agent_api.AgentProtocol, string, error) {
	if rc.azdClient != nil && rc.agentKey != "" {
		record, err := readSavedInvocationState(ctx, rc, "")
		if err != nil {
			return "", "", classifyInvocationStateReadError(err)
		}
		if record != nil {
			protocol := record.CurrentInvocationProtocol
			if protocol == "" {
				if record.Response != nil && record.Response.ResponseID != "" {
					protocol = agent_api.AgentProtocolResponses
				}
				if record.Invocation != nil && record.Invocation.InvocationID != "" {
					if protocol != "" {
						return "", "", exterrors.Validation(
							exterrors.CodeInvalidParameter,
							"saved invocation state contains multiple protocols without a current selection",
							"pass --protocol explicitly to select the saved invocation",
						)
					}
					protocol = agent_api.AgentProtocolInvocations
				}
			}
			switch {
			case protocol == agent_api.AgentProtocolResponses && record.Response != nil:
				return protocol, record.Response.ResponseID, nil
			case protocol == agent_api.AgentProtocolInvocations && record.Invocation != nil:
				return protocol, record.Invocation.InvocationID, nil
			case protocol != "":
				return protocol, "", nil
			}
		}
	}
	protocol, err := a.resolveDeployedProtocol(ctx, rc)
	return protocol, "", err
}

// readSavedInvocationState prefers target-level work, retaining older version-keyed selections.
func readSavedInvocationState(
	ctx context.Context,
	rc *remoteContext,
	protocol agent_api.AgentProtocol,
) (*agentStateRecord, error) {
	store, err := newAgentStateStoreForClient(rc.azdClient)
	if err != nil {
		return nil, err
	}
	currentKey := buildInvocationStateKey(rc.agentKey)
	keys := []string{currentKey}
	if currentKey != rc.agentKey {
		keys = append(keys, rc.agentKey)
	}
	for _, key := range keys {
		record, err := store.get(ctx, key)
		if err != nil {
			return nil, err
		}
		if record == nil {
			continue
		}
		switch protocol {
		case agent_api.AgentProtocolResponses:
			if record.Response != nil || record.CurrentInvocationProtocol == protocol {
				return record, nil
			}
		case agent_api.AgentProtocolInvocations:
			if record.Invocation != nil || record.CurrentInvocationProtocol == protocol {
				return record, nil
			}
		default:
			if record.CurrentInvocationProtocol != "" || record.Response != nil || record.Invocation != nil {
				return record, nil
			}
		}
	}
	return nil, nil
}

// resolveCurrentInvocationID uses an explicit ID or the selected protocol's saved ID.
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
	record, err := readSavedInvocationState(ctx, rc, protocol)
	if err != nil {
		if protocol == agent_api.AgentProtocolResponses {
			return "", classifyResponseStateReadError(err)
		}
		return "", classifyInvocationStateReadError(err)
	}
	var id string
	if record != nil {
		switch protocol {
		case agent_api.AgentProtocolResponses:
			if record.Response != nil {
				id = record.Response.ResponseID
			}
		case agent_api.AgentProtocolInvocations:
			if record.Invocation != nil {
				id = record.Invocation.InvocationID
			}
		}
	}
	if id == "" {
		return "", noCurrentInvocationError(protocol)
	}
	return id, nil
}

func noCurrentInvocationError(protocol agent_api.AgentProtocol) error {
	return exterrors.Validation(exterrors.CodeInvalidParameter,
		fmt.Sprintf("no current invocation is saved for the selected agent and %s protocol", protocol),
		"invoke the agent first or provide --id")
}

// runInvocationOperation resolves the target and executes its lifecycle operation.
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

// runInvocationOperation dispatches lifecycle work to the selected protocol.
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
	case agent_api.AgentProtocolInvocations:
		return a.runInvocationsProtocolOperation(ctx, rc, id, operation, format, writer)
	default:
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			"unsupported invocation protocol", "select a protocol that supports this operation")
	}
}
