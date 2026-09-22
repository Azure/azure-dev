// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type stateStoreFlags struct {
	agentName     string
	agentEndpoint string
	environment   string
	store         string
	page          agent_api.StateStoreListOptions
	value         string
	valueFile     string
	tags          []string
	ifMatch       string
	yes           bool
	noPrompt      bool
	output        string
}

type stateStoreAPI interface {
	ListStateStores(context.Context, string, agent_api.StateStoreListOptions) (
		*agent_api.StateStorePage[agent_api.StateStore], error)
	GetStateStore(context.Context, string, string) (*agent_api.StateStore, error)
	ListStateStoreItemKeys(context.Context, string, string, agent_api.StateStoreListOptions) (
		*agent_api.StateStorePage[agent_api.StateStoreItem], error)
	GetStateStoreItem(context.Context, string, string, string) (*agent_api.StateStoreItem, error)
	SetStateStoreItem(context.Context, string, string, string, agent_api.SetStateStoreItemRequest, string) (
		*agent_api.StateStoreItem, error)
	DeleteStateStoreItem(context.Context, string, string, string, string) (*agent_api.DeletedStateStoreItem, error)
}

type stateStoreAction struct {
	api     stateStoreAPI
	host    *azdext.AzdClient
	prompt  azdext.PromptServiceClient
	target  *stateStoreTarget
	flags   *stateStoreFlags
	request agent_api.SetStateStoreItemRequest
	writer  io.Writer
}

func newStateStoresCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	cmd := &cobra.Command{
		Use: "state-stores", Short: "Inspect existing agent State Stores and manage their items.",
		Long: `Inspect existing Foundry State Stores and manage JSON object items.

Select a store once, or pass --store on individual item commands. Store selection
is saved per project endpoint and agent, independently of protocol and version.`,
		Example: "  azd ai agent state-stores list\n  azd ai agent state-stores select checkpoints/run-42",
	}
	for _, operation := range []string{"list", "select", "show"} {
		cmd.AddCommand(newStateStoreOperationCommand(extCtx, operation))
	}
	items := &cobra.Command{
		Use: "items", Short: "Read, replace, and delete items in an existing State Store.",
		Example: "  azd ai agent state-stores items list\n  azd ai agent state-stores items show task-123",
	}
	for _, operation := range []string{"list", "show", "set", "delete"} {
		items.AddCommand(newStateStoreOperationCommand(extCtx, "items "+operation))
	}
	cmd.AddCommand(items)
	return cmd
}

type stateStoreActionFactory func(context.Context, *stateStoreFlags) (*stateStoreAction, func(), error)

func newStateStoreAction(ctx context.Context, flags *stateStoreFlags) (*stateStoreAction, func(), error) {
	host, err := azdext.NewAzdClient()
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { host.Close() }
	target, err := resolveStateStoreTarget(ctx, host, flags)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	client, err := target.NewClient()
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return &stateStoreAction{api: client, host: host, prompt: host.Prompt(), target: target, flags: flags}, cleanup, nil
}

func newStateStoreOperationCommand(extCtx *azdext.ExtensionContext, operation string) *cobra.Command {
	return newStateStoreCommandWithFactory(extCtx, operation, newStateStoreAction)
}

func newStateStoreCommandWithFactory(
	extCtx *azdext.ExtensionContext, operation string, factory stateStoreActionFactory,
) *cobra.Command {
	flags := &stateStoreFlags{page: agent_api.StateStoreListOptions{Limit: 20, Order: "desc"}}
	use, short, example := stateStoreCommandHelp(operation)
	cmd := &cobra.Command{
		Use: use, Short: short, Long: short, Example: example,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.noPrompt, flags.output = extCtx.NoPrompt, extCtx.OutputFormat
			flags.environment = extCtx.Environment
			if err := validateStateStoreFlags(cmd, flags, operation, args); err != nil {
				return err
			}
			var request agent_api.SetStateStoreItemRequest
			if operation == "items set" {
				var err error
				request, err = readStateStoreValue(cmd.Context(), flags, cmd.InOrStdin())
				if err != nil {
					return err
				}
			}
			ctx := azdext.WithAccessToken(cmd.Context())
			action, cleanup, err := factory(ctx, flags)
			if err != nil {
				return err
			}
			defer cleanup()
			action.request, action.writer = request, cmd.OutOrStdout()
			return action.run(ctx, operation, args)
		},
	}
	if operation == "show" || operation == "select" {
		cmd.Args = cobra.MaximumNArgs(1)
	} else if strings.HasPrefix(operation, "items ") && operation != "items list" {
		cmd.Args = cobra.ExactArgs(1)
	}
	cmd.Flags().StringVarP(&flags.agentName, "agent-name", "n", "", "Agent service name in azure.yaml")
	cmd.Flags().StringVar(&flags.agentEndpoint, "agent-endpoint", "", "Full protocol endpoint URL of a deployed agent")
	cmd.MarkFlagsMutuallyExclusive("agent-name", "agent-endpoint")
	if strings.HasPrefix(operation, "items ") {
		cmd.Flags().StringVar(&flags.store, "store", "",
			"Store name (defaults to the active store; does not change selection)")
	}
	if operation == "list" || operation == "items list" {
		cmd.Long += `

Returns at most --limit results in the selected --order. If has_more is true,
pass last_id from the JSON response to --after for the next page. Keep the same
--order and --limit. Omit --after to start from the beginning.

Pass the returned cursor unchanged; do not use an entry's id or base64url-encode it.`
		cmd.Flags().IntVar(&flags.page.Limit, "limit", 20, "Maximum results per page (1-100)")
		cmd.Flags().StringVar(&flags.page.Order, "order", "desc", "Service-defined order: asc or desc")
		cmd.Flags().StringVar(&flags.page.After, "after", "", "Continue after last_id from the previous JSON response")
	}
	if operation == "items set" {
		cmd.Long += "\n\nCreates a missing item or replaces its entire value and tags. Omitting --tag clears existing tags."
		cmd.Long += fmt.Sprintf("\nThe preview value limit is %d bytes (1 MiB) of serialized JSON.",
			agent_api.MaxStateStoreValueBytes)
		cmd.Long += "\nRaw input uses the same limit, including whitespace; compact larger formatted input first."
		cmd.Flags().StringVar(&flags.value, "value", "", "JSON object value (not a REST request envelope)")
		cmd.Flags().StringVar(&flags.valueFile, "value-file", "", "Read the JSON object from a file; - reads stdin")
		cmd.Flags().StringArrayVar(&flags.tags, "tag", nil,
			"Replacement tag as key=value (repeatable; omission clears tags)")
		cmd.MarkFlagsMutuallyExclusive("value", "value-file")
		cmd.MarkFlagsOneRequired("value", "value-file")
	}
	if operation == "items set" || operation == "items delete" {
		cmd.Flags().StringVar(&flags.ifMatch, "if-match", "", "Only write if the current ETag matches (preserve its quotes)")
	}
	if operation == "items delete" {
		cmd.Flags().BoolVarP(&flags.yes, "yes", "y", false, "Skip delete confirmation (required with --no-prompt)")
	}
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "json",
	})
	return cmd
}

func stateStoreCommandHelp(operation string) (string, string, string) {
	prefix := "  azd ai agent state-stores "
	switch operation {
	case "list", "items list":
		short := "List one page of existing State Stores."
		if operation == "items list" {
			short = "List one page of item keys and metadata, without values."
		}
		example := "  # First page\n" + prefix + operation + " --limit 2 --order asc\n\n" +
			"  # Next page: copy last_id from the response when has_more is true\n" +
			prefix + operation + " --limit 2 --order asc --after \"<last_id>\""
		return "list", short, example
	case "select":
		return "select [store-name]", "Validate and save the active store, or choose one interactively.",
			prefix + "select checkpoints/run-42"
	case "show":
		return "show [store-name]", "Show the named store or the active store.", prefix + "show"
	case "items show":
		return "show <key>", "Show an item's JSON value, tags, and ETag.", prefix + "items show task-123"
	case "items set":
		return "set <key>", "Create or replace a JSON object item in an existing store.",
			prefix + "items set task-123 --value-file checkpoint.json --tag kind=checkpoint"
	default:
		return "delete <key>", "Delete an item from an existing store.", prefix + "items delete task-123 --yes"
	}
}

func validateStateStoreFlags(cmd *cobra.Command, flags *stateStoreFlags, operation string, args []string) error {
	invalid := func(message string) error {
		return exterrors.Validation(exterrors.CodeInvalidParameter, message, "see command help for valid arguments")
	}
	for _, name := range []string{"agent-name", "agent-endpoint", "store", "value-file", "if-match", "after"} {
		if cmd.Flags().Changed(name) {
			value, _ := cmd.Flags().GetString(name)
			if value == "" {
				return invalid("--" + name + " requires a non-empty value")
			}
		}
	}
	if flags.agentName != "" && flags.agentEndpoint != "" {
		return invalid("--agent-name and --agent-endpoint cannot be combined")
	}
	if slices.Contains(args, "") {
		return invalid("store names and item keys must not be empty")
	}
	if flags.page.Limit < 1 || flags.page.Limit > 100 {
		return invalid("--limit must be between 1 and 100")
	}
	if flags.page.Order != "asc" && flags.page.Order != "desc" {
		return invalid("--order must be asc or desc")
	}
	if flags.output != "" && flags.output != "json" && flags.output != "table" {
		return invalid("--output must be json or table")
	}
	if operation == "items set" && cmd.Flags().Changed("value") == cmd.Flags().Changed("value-file") {
		return invalid("provide exactly one of --value or --value-file")
	}
	if operation == "select" && len(args) == 0 && flags.noPrompt {
		return invalid("provide a store name when using --no-prompt")
	}
	if operation == "items delete" && flags.noPrompt && !flags.yes {
		return invalid("--yes is required to delete an item with --no-prompt")
	}
	return nil
}

func (a *stateStoreAction) run(ctx context.Context, operation string, args []string) error {
	result, err := a.execute(ctx, operation, args)
	if err != nil {
		if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
			suggestion := ""
			switch respErr.StatusCode {
			case http.StatusBadRequest:
				suggestion = "check the JSON value, tag constraints, and command arguments"
			case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
				suggestion = "check the selected agent, store, and your access permissions"
			case http.StatusPreconditionFailed:
				suggestion = "read the item again and reconcile your changes before retrying with its current ETag"
			case http.StatusTooManyRequests:
				suggestion = "wait before retrying and reduce the request rate"
			default:
				if respErr.StatusCode >= 500 && respErr.StatusCode < 600 {
					suggestion = "the service encountered an error; retry later"
					if operation == "items set" || operation == "items delete" {
						suggestion = "the service encountered an error; check the item's current state before retrying the write"
					}
				}
			}
			return &azdext.ServiceError{
				Message: fmt.Sprintf("State Store %s failed: HTTP %d (%s)",
					operation, respErr.StatusCode, http.StatusText(respErr.StatusCode)),
				StatusCode: respErr.StatusCode, ServiceName: "foundry", Suggestion: suggestion,
			}
		}
		classified := exterrors.ServiceFromAzure(err, exterrors.CodeStateStoreOperation)
		if operation == "items set" || operation == "items delete" {
			if _, ok := errors.AsType[*agent_api.StateStoreWriteOutcomeUnknownError](err); ok {
				if local, ok := errors.AsType[*azdext.LocalError](classified); ok &&
					local.Category == azdext.LocalErrorCategoryInternal {
					local.Suggestion = "the write outcome could not be confirmed; check the item's current state " +
						"and reconcile your changes before retrying the write"
				}
			}
		}
		return classified
	}
	if a.flags.output == "table" {
		return writeStateStoreTable(a.writer, result)
	}
	encoder := json.NewEncoder(a.writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func (a *stateStoreAction) execute(ctx context.Context, operation string, args []string) (any, error) {
	switch operation {
	case "list":
		return a.api.ListStateStores(ctx, a.target.Name, a.flags.page)
	case "select":
		return a.selectStore(ctx, firstStateStoreArg(args))
	case "show":
		store, err := a.resolveStore(ctx, firstStateStoreArg(args))
		if err != nil {
			return nil, err
		}
		return a.api.GetStateStore(ctx, a.target.Name, store)
	default:
		return a.executeItem(ctx, operation, firstStateStoreArg(args))
	}
}

func firstStateStoreArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
