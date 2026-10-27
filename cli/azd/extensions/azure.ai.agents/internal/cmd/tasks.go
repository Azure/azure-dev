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
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type tasksFlags struct {
	projectEndpoint string
	agentName       string
	environment     string
	noPrompt        bool
	output          string
	page            agent_api.TaskListOptions
	hasError        bool
	leaseExpired    bool
	omitValues      bool
}

type tasksAPI interface {
	ListTasks(context.Context, agent_api.TaskListOptions) (json.RawMessage, error)
	GetTask(context.Context, string) (json.RawMessage, error)
}

type tasksAction struct {
	api    tasksAPI
	target *AgentContext
	flags  *tasksFlags
	writer io.Writer
}

type tasksActionFactory func(context.Context, *tasksFlags, bool) (*tasksAction, func(), error)

func newTasksCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	return newTasksCommandWithFactory(extCtx, newTasksAction)
}

func newTasksCommandWithFactory(extCtx *azdext.ExtensionContext, factory tasksActionFactory) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	cmd := &cobra.Command{
		Use: "tasks", Short: "Inspect hosted-agent Tasks (Preview).",
		Long:    "Inspect existing hosted-agent Tasks without creating, changing, or selecting a Task.",
		Example: "  azd ai agent tasks list\n  azd ai agent tasks show <task-id>",
	}
	previewCommand(cmd, featureflags.LongRunning)
	for _, operation := range []string{"list", "show"} {
		cmd.AddCommand(newTasksOperationCommand(extCtx, operation, factory))
	}
	return cmd
}

func newTasksAction(ctx context.Context, flags *tasksFlags, list bool) (*tasksAction, func(), error) {
	host, err := azdext.NewAzdClient()
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { host.Close() }
	target, err := resolveTasksTarget(ctx, host, tasksTargetOptions{
		projectEndpoint: flags.projectEndpoint, agentName: flags.agentName, environment: flags.environment,
		noPrompt: flags.noPrompt, list: list,
	})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	client, err := target.NewClient()
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return &tasksAction{api: client, target: target, flags: flags}, cleanup, nil
}

func newTasksOperationCommand(
	extCtx *azdext.ExtensionContext, operation string, factory tasksActionFactory,
) *cobra.Command {
	flags := &tasksFlags{}
	validate := func(cmd *cobra.Command, args []string) error {
		flags.environment, flags.noPrompt, flags.output = extCtx.Environment, extCtx.NoPrompt, extCtx.OutputFormat
		return validateTasksFlags(cmd, flags, operation, args)
	}
	cmd := &cobra.Command{
		Use: "list", Short: "List one page of Tasks for a hosted agent.", Args: cobra.NoArgs,
		Example: "  azd ai agent tasks list --limit 20 --order desc",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validate(cmd, args); err != nil {
				return err
			}
			ctx := azdext.WithAccessToken(cmd.Context())
			action, cleanup, err := factory(ctx, flags, operation == "list")
			if err != nil {
				return err
			}
			defer cleanup()
			action.flags, action.writer = flags, cmd.OutOrStdout()
			return action.run(ctx, operation, args)
		},
	}
	if operation == "show" {
		cmd.Use, cmd.Short, cmd.Args = "show <task-id>", "Show a Task by its exact ID.", cobra.ExactArgs(1)
		cmd.Example = "  azd ai agent tasks show <task-id> --project-endpoint <project-endpoint>"
	}
	cmd.Long = cmd.Short + " JSON is the default output; table output shows only safe summary metadata."
	cmd.Flags().StringVarP(&flags.projectEndpoint, "project-endpoint", "p", "", "Foundry project endpoint URL")
	if operation == "list" {
		cmd.Flags().StringVarP(&flags.agentName, "agent-name", "n", "",
			"Deployed agent name or local service name (resolved or selected when omitted)")
		cmd.Long += `

Returns one page only. When has_more is true, use --output json to read last_id
and pass it unchanged to --after. Keep the same filters, --order, and --limit.
The cursor is opaque: do not derive it from a Task ID or encode it yourself.
Table output always omits attachment values, even with --omit-attachment-values=false.`
		cmd.Flags().IntVar(&flags.page.Limit, "limit", 20, "Maximum results per page (1-100)")
		cmd.Flags().StringVar(&flags.page.Order, "order", "desc", "Service-defined order: asc or desc")
		cmd.Flags().StringVar(&flags.page.After, "after", "", "Opaque last_id from the previous JSON response")
		cmd.Flags().StringVar(&flags.page.SessionID, "session-id", "", "Filter by exact session ID")
		cmd.Flags().StringVar(&flags.page.Status, "status", "", "Filter: pending, in_progress, suspended, or completed")
		cmd.Flags().StringVar(&flags.page.SourceType, "source-type", "", "Filter by source type")
		cmd.Flags().StringVar(&flags.page.LeaseOwner, "lease-owner", "", "Filter by lease owner")
		cmd.Flags().BoolVar(&flags.hasError, "has-error", false, "Filter by error presence (true or false)")
		cmd.Flags().BoolVar(&flags.leaseExpired, "lease-expired", false, "Filter by expired lease (true or false)")
		cmd.Flags().StringArrayVar(&flags.page.Tags, "tag", nil, "Repeatable key:value tag equality (split at first colon)")
		cmd.Flags().BoolVar(&flags.omitValues, "omit-attachment-values", false, "Omit attachment values in JSON output")
	}
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "json",
	})
	validateBeforePreview(cmd, validate)
	return cmd
}

func validateTasksFlags(cmd *cobra.Command, flags *tasksFlags, operation string, args []string) error {
	invalid := func(message string) error {
		return exterrors.Validation(exterrors.CodeInvalidParameter, message, "see command help for valid arguments")
	}
	if operation == "show" {
		if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
			return invalid("provide exactly one non-empty task ID")
		}
	} else if len(args) != 0 {
		return invalid("tasks list does not accept positional arguments")
	}
	for _, name := range []string{
		"project-endpoint", "agent-name", "environment", "after", "session-id", "status", "source-type", "lease-owner",
	} {
		if cmd.Flags().Changed(name) {
			value, _ := cmd.Flags().GetString(name)
			if strings.TrimSpace(value) == "" {
				return invalid("--" + name + " requires a non-empty value")
			}
		}
	}
	if flags.projectEndpoint != "" {
		if _, err := tasksProjectEndpoint(flags.projectEndpoint); err != nil {
			return invalid("--project-endpoint must be an HTTPS Foundry project URL ending in /api/projects/<project> " +
				"without credentials, query, or fragment")
		}
	}
	if flags.output != "" && flags.output != "json" && flags.output != "table" {
		return invalid("--output must be json or table")
	}
	if operation != "list" {
		return nil
	}
	if flags.page.Limit < 1 || flags.page.Limit > 100 {
		return invalid("--limit must be between 1 and 100")
	}
	if flags.page.Order != "asc" && flags.page.Order != "desc" {
		return invalid("--order must be asc or desc")
	}
	switch flags.page.Status {
	case "", "pending", "in_progress", "suspended", "completed":
	default:
		return invalid("--status must be pending, in_progress, suspended, or completed")
	}
	for _, tag := range flags.page.Tags {
		key, value, found := strings.Cut(tag, ":")
		if !found || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return invalid("--tag must use key:value with non-empty keys and values")
		}
	}
	if cmd.Flags().Changed("has-error") {
		flags.page.HasError = new(flags.hasError)
	}
	if cmd.Flags().Changed("lease-expired") {
		flags.page.LeaseExpired = new(flags.leaseExpired)
	}
	if cmd.Flags().Changed("omit-attachment-values") {
		flags.page.OmitAttachmentValues = new(flags.omitValues)
	}
	if flags.output == "table" {
		flags.page.OmitAttachmentValues = new(true)
	}
	return nil
}

func (a *tasksAction) run(ctx context.Context, operation string, args []string) error {
	var result json.RawMessage
	var err error
	if operation == "list" {
		options := a.flags.page
		options.AgentName = a.target.Name
		result, err = a.api.ListTasks(ctx, options)
	} else {
		result, err = a.api.GetTask(ctx, args[0])
	}
	if err != nil {
		return tasksOperationError(err, operation)
	}
	if a.flags.output == "table" {
		return writeTasksTable(a.writer, result, operation == "list")
	}
	encoder := json.NewEncoder(a.writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func tasksOperationError(err error, operation string) error {
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok {
		suggestion := "check the project endpoint, agent, and your access permissions"
		message := fmt.Sprintf("Tasks %s failed: HTTP %d (%s)",
			operation, response.StatusCode, http.StatusText(response.StatusCode))
		switch response.StatusCode {
		case http.StatusForbidden:
			suggestion = "verify hosted-agent Tasks preview availability and your project permissions"
		case http.StatusNotFound:
			switch response.ErrorCode {
			case "TaskNotFound", "task_not_found":
				message = "Task not found"
				suggestion = "verify the exact task ID and project endpoint; the framework may have cleaned up the record"
			default:
				suggestion = "verify the project endpoint, exact task ID (for show), and Tasks preview route availability"
			}
		case http.StatusBadRequest:
			suggestion = "check the filters and opaque cursor; keep the same filters and order when paging"
		case http.StatusTooManyRequests:
			suggestion = "wait before retrying and reduce the request rate"
		default:
			if response.StatusCode >= 500 {
				suggestion = "the service encountered an error; retry later"
			}
		}
		if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusNotFound {
			switch response.ErrorCode {
			case "UnsupportedApiVersion", "InvalidApiVersionParameter", "ApiVersionNotSupported",
				"unsupported_api_version", "invalid_api_version", "api_version_not_supported":
				message = "Tasks route or API version is unavailable"
				suggestion = "verify the project endpoint and hosted-agent Tasks preview availability"
			}
		}
		return &azdext.ServiceError{
			Message: message, StatusCode: response.StatusCode, ServiceName: "foundry", Suggestion: suggestion,
		}
	}
	// The API sanitizes transport errors. Keep cancellation recognizable without
	// exposing arbitrary error bodies from other implementations of this boundary.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return errors.New("Tasks request failed; verify authentication, project endpoint, and network connectivity")
}
