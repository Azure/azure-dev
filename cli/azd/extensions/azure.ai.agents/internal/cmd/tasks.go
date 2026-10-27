// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/spf13/cobra"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/featureflags"
	"azureaiagent/internal/pkg/agents/agent_api"
)

// Commands and actions.

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

// Target resolution.

type tasksTargetOptions struct {
	projectEndpoint string
	agentName       string
	environment     string
	noPrompt        bool
	list            bool
}

// tasksProjectEndpoint applies stricter path and URL rules than the shared
// validator, which deliberately tolerates non-project paths and drops URL parts.
func tasksProjectEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid project endpoint: %w", err)
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", fmt.Errorf("project endpoint must not contain credentials, query, or fragment")
	}
	normalized, _, err := validateProjectEndpoint(raw)
	if err != nil {
		return "", err
	}
	// Inspect the decoded path as well, rejecting escaped separators and dot
	// segments rather than allowing them to change the resource being addressed.
	path := strings.TrimSuffix(u.Path, "/")
	project, found := strings.CutPrefix(path, projectEndpointPathPrefix)
	if !found || project == "" || project == "." || project == ".." || strings.ContainsAny(project, "/\\") {
		return "", fmt.Errorf("project endpoint must have path /api/projects/<project-name>")
	}
	canonical, _ := url.Parse(normalized)
	canonical.Path = projectEndpointPathPrefix + project
	canonical.RawPath = ""
	return canonical.String(), nil
}

func tasksAgentNameRequired(message string) error {
	return exterrors.Validation(exterrors.CodeInvalidAgentName, message,
		"deploy the agent service, or supply --agent-name <deployed-agent-name> and --project-endpoint <url>")
}

func tasksDeploymentEndpoint(values map[string]string, service string) string {
	prefix := "AGENT_" + toServiceKey(service)
	if endpoint := strings.TrimSpace(values[prefix+"_PROJECT_ENDPOINT"]); endpoint != "" {
		return endpoint
	}
	if endpoint, _, found := strings.Cut(values[prefix+"_ENDPOINT"], "/agents/"); found {
		return endpoint
	}
	return strings.TrimSpace(values["FOUNDRY_PROJECT_ENDPOINT"])
}

func resolveTasksTarget(
	ctx context.Context, host *azdext.AzdClient, opts tasksTargetOptions,
) (*AgentContext, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	explicit := opts.projectEndpoint != ""
	endpoint := ""
	var err error
	if explicit {
		endpoint, err = tasksProjectEndpoint(opts.projectEndpoint)
		if err != nil {
			return nil, err
		}
		// Task IDs are project-scoped. Show never needs local service discovery.
		if !opts.list {
			return &AgentContext{ProjectEndpoint: endpoint}, nil
		}
	}

	// Read only the selected environment; never use workspace defaults, global
	// configuration, or process environment as an endpoint fallback.
	values := map[string]string{}
	var env *azdext.EnvironmentResponse
	if host != nil {
		if opts.environment != "" {
			env, err = host.Environment().Get(ctx, &azdext.GetEnvironmentRequest{Name: opts.environment})
		} else {
			env, err = host.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		}
		if err == nil && env != nil && env.Environment != nil && env.Environment.Name != "" {
			values, err = getAgentEnvironmentValues(ctx, host, env.Environment.Name)
		} else if err == nil {
			err = fmt.Errorf("selected azd environment is not available")
		}
	} else {
		err = fmt.Errorf("selected azd environment is not available")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		// Explicit endpoint + name is intentionally usable outside a checkout.
		if !explicit {
			return nil, fmt.Errorf("reading selected azd environment %q: %w", opts.environment, err)
		}
		if opts.agentName == "" {
			return nil, tasksAgentNameRequired("no selected environment is available to discover deployed agents")
		}
		return &AgentContext{ProjectEndpoint: endpoint, Name: opts.agentName}, nil
	}
	if !opts.list && strings.TrimSpace(values["FOUNDRY_PROJECT_ENDPOINT"]) != "" {
		endpoint, err = tasksProjectEndpoint(values["FOUNDRY_PROJECT_ENDPOINT"])
		if err != nil {
			return nil, fmt.Errorf("no valid Foundry project endpoint in selected environment; "+
				"deploy an agent service or supply --project-endpoint: %w", err)
		}
		return &AgentContext{ProjectEndpoint: endpoint}, nil
	}

	var project *azdext.ProjectConfig
	if host != nil {
		response, projectErr := host.Project().Get(ctx, &azdext.EmptyRequest{})
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if projectErr == nil && response != nil {
			project = response.Project
		}
	}
	// Build candidates using deployed outputs in this environment, not inline
	// names or the shared deployed-name lookup (which uses the current env).
	services := map[string]*azdext.ServiceConfig{}
	if project != nil {
		for _, service := range project.Services {
			if service != nil && service.Host == AiAgentHost {
				services[service.Name] = service
			}
		}
	}
	if !opts.list {
		projects := map[string]bool{}
		for _, service := range slices.Sorted(maps.Keys(services)) {
			raw := tasksDeploymentEndpoint(values, service)
			if raw == "" {
				continue
			}
			deploymentEndpoint, endpointErr := tasksProjectEndpoint(raw)
			if endpointErr != nil {
				return nil, fmt.Errorf("invalid deployed project endpoint for service %q; "+
					"deploy the service or supply --project-endpoint: %w", service, endpointErr)
			}
			projects[deploymentEndpoint] = true
		}
		if len(projects) > 1 {
			return nil, fmt.Errorf("multiple deployed Foundry projects in selected environment; supply --project-endpoint")
		}
		for projectEndpoint := range projects {
			return &AgentContext{ProjectEndpoint: projectEndpoint}, nil
		}
		return nil, fmt.Errorf("no valid Foundry project endpoint in selected environment; " +
			"deploy an agent service or supply --project-endpoint")
	}
	name := opts.agentName
	selected := ""
	if service := services[name]; name != "" && service != nil {
		deployed := strings.TrimSpace(values["AGENT_"+toServiceKey(name)+"_NAME"])
		deploymentEndpoint, endpointErr := tasksProjectEndpoint(tasksDeploymentEndpoint(values, name))
		if !explicit || (endpointErr == nil && deploymentEndpoint == endpoint) {
			if deployed == "" {
				return nil, tasksAgentNameRequired("the selected service has no deployed agent name")
			}
			name, selected = deployed, service.Name
		}
	}
	if name == "" {
		candidates := []string{}
		for service := range services {
			if strings.TrimSpace(values["AGENT_"+toServiceKey(service)+"_NAME"]) == "" {
				continue
			}
			deploymentEndpoint, endpointErr := tasksProjectEndpoint(tasksDeploymentEndpoint(values, service))
			if explicit && (endpointErr != nil || deploymentEndpoint != endpoint) {
				continue
			}
			candidates = append(candidates, service)
		}
		slices.Sort(candidates)
		if len(candidates) == 0 {
			return nil, tasksAgentNameRequired("no deployed agent services found for this project")
		}
		index := 0
		if len(candidates) > 1 {
			if opts.noPrompt {
				return nil, tasksAgentNameRequired("multiple deployed agent services found with --no-prompt")
			}
			choices := make([]*azdext.SelectChoice, len(candidates))
			for i, service := range candidates {
				choices[i] = &azdext.SelectChoice{Label: service, Value: service}
			}
			response, err := host.Prompt().Select(ctx, &azdext.SelectRequest{Options: &azdext.SelectOptions{
				Message: "Select an agent service", Choices: choices,
			}})
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err != nil {
				return nil, exterrors.FromPrompt(err, "selecting an agent service")
			}
			if response == nil || response.Value == nil {
				return nil, exterrors.Cancelled("agent selection cancelled")
			}
			index = int(*response.Value)
			if index < 0 || index >= len(candidates) {
				return nil, fmt.Errorf("invalid agent service selection index")
			}
		}
		selected = candidates[index]
		name = strings.TrimSpace(values["AGENT_"+toServiceKey(selected)+"_NAME"])
	}
	// Raw deployed names may also identify a local service in the selected env.
	if selected == "" && !explicit {
		projects := map[string]bool{}
		for _, service := range slices.Sorted(maps.Keys(services)) {
			if strings.TrimSpace(values["AGENT_"+toServiceKey(service)+"_NAME"]) != name {
				continue
			}
			deploymentEndpoint, endpointErr := tasksProjectEndpoint(tasksDeploymentEndpoint(values, service))
			if endpointErr != nil {
				return nil, fmt.Errorf("no valid Foundry project endpoint for service %q; "+
					"deploy the service or supply --project-endpoint: %w", service, endpointErr)
			}
			projects[deploymentEndpoint] = true
			if selected == "" {
				selected = service
			}
		}
		if len(projects) > 1 {
			return nil, fmt.Errorf("agent name %q matches multiple deployed Foundry projects; "+
				"supply --project-endpoint or use a service alias with --agent-name", name)
		}
	}
	if selected != "" {
		if err := requireSelectedServiceFeatures(ctx, services[selected], project.Path); err != nil {
			return nil, err
		}
	}
	if !explicit {
		endpoint, err = tasksProjectEndpoint(tasksDeploymentEndpoint(values, selected))
		if err != nil {
			return nil, fmt.Errorf("no valid Foundry project endpoint in selected environment; "+
				"deploy an agent service or supply --project-endpoint: %w", err)
		}
	}
	return &AgentContext{ProjectEndpoint: endpoint, Name: name}, ctx.Err()
}

// Output projections and formatting.

// These projections are used only for human-readable output. JSON output always
// uses the original RawMessage so new fields and arbitrary-precision numbers survive.
type taskTableProjection struct {
	ID        string          `json:"id"`
	Status    string          `json:"status"`
	AgentName string          `json:"agent_name"`
	SessionID string          `json:"session_id"`
	UpdatedAt json.RawMessage `json:"updated_at"`
}

type taskDetailProjection struct {
	taskTableProjection
	CreatedAt   json.RawMessage `json:"created_at"`
	StartedAt   json.RawMessage `json:"started_at"`
	CompletedAt json.RawMessage `json:"completed_at"`
	Lease       *struct {
		Owner     string          `json:"owner"`
		ExpiresAt json.RawMessage `json:"expires_at"`
	} `json:"lease"`
	Error json.RawMessage `json:"error"`
}

type taskTableRow struct {
	ID, Status, Agent, Session, Updated string
}

func taskDisplayText(value string) string {
	// Escape controls, including C1 and bidi/format controls, rather than allowing
	// service strings to issue terminal commands or forge extra output rows.
	var safe strings.Builder
	for _, r := range value {
		if safe.Len() >= 256 {
			safe.WriteString("...")
			break
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			quoted := strconv.QuoteRuneToASCII(r)
			safe.WriteString(quoted[1 : len(quoted)-1])
		} else {
			safe.WriteRune(r)
		}
	}
	return safe.String()
}

func taskTableTimestamp(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return taskDisplayText(text)
	}
	var seconds int64
	if json.Unmarshal(raw, &seconds) == nil {
		if seconds == 0 {
			return ""
		}
		// Avoid unbounded dates or converting unknown numeric timestamp formats.
		if seconds > 0 && seconds <= 253402300799 {
			return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func writeTasksTable(writer io.Writer, raw json.RawMessage, list bool) error {
	return writeTasksTableWithFormatter(writer, raw, list, &output.PrettyTableFormatter{})
}

func writeTasksTableWithFormatter(
	writer io.Writer, raw json.RawMessage, list bool, formatter *output.PrettyTableFormatter,
) error {
	var tasks []taskTableProjection
	var detail taskDetailProjection
	var hasMore bool
	if list {
		var page struct {
			Data    []taskTableProjection `json:"data"`
			HasMore bool                  `json:"has_more"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return fmt.Errorf("invalid Tasks table response")
		}
		tasks, hasMore = page.Data, page.HasMore
	} else {
		if err := json.Unmarshal(raw, &detail); err != nil {
			return fmt.Errorf("invalid Task table response")
		}
		tasks = []taskTableProjection{detail.taskTableProjection}
	}
	rows := make([]taskTableRow, 0, len(tasks))
	for _, task := range tasks {
		rows = append(rows, taskTableRow{
			ID: taskDisplayText(task.ID), Status: taskDisplayText(task.Status),
			Agent: taskDisplayText(task.AgentName), Session: taskDisplayText(task.SessionID),
			Updated: taskTableTimestamp(task.UpdatedAt),
		})
	}
	if len(rows) == 0 {
		if _, err := fmt.Fprintln(writer, "No Tasks on this page."); err != nil {
			return err
		}
	} else {
		if err := formatter.Format(rows, writer, output.PrettyTableFormatterOptions{
			Columns: []output.PrettyColumn{
				{Column: output.Column{Heading: "ID", ValueTemplate: "{{if .ID}}{{.ID}}{{else}}-{{end}}"},
					CardTitle: true, Truncatable: true},
				{Column: output.Column{Heading: "STATUS", ValueTemplate: "{{if .Status}}{{.Status}}{{else}}-{{end}}"},
					Priority: 1, Truncatable: true},
				{Column: output.Column{Heading: "AGENT", ValueTemplate: "{{if .Agent}}{{.Agent}}{{else}}-{{end}}"},
					Priority: 2, Truncatable: true},
				{Column: output.Column{Heading: "SESSION", ValueTemplate: "{{if .Session}}{{.Session}}{{else}}-{{end}}"},
					Priority: 3, Truncatable: true},
				{Column: output.Column{Heading: "UPDATED", ValueTemplate: "{{if .Updated}}{{.Updated}}{{else}}-{{end}}"},
					Priority: 2, Truncatable: true},
			}, ResponsiveColumnHint: true, ForceCards: !list,
		}); err != nil {
			return err
		}
	}
	if !list {
		task := detail
		var leaseOwner, leaseExpiry string
		if task.Lease != nil {
			leaseOwner = taskDisplayText(task.Lease.Owner)
			leaseExpiry = taskTableTimestamp(task.Lease.ExpiresAt)
		}
		for _, field := range []struct{ label, value string }{
			{"Created", taskTableTimestamp(task.CreatedAt)},
			{"Started", taskTableTimestamp(task.StartedAt)},
			{"Completed", taskTableTimestamp(task.CompletedAt)},
			{"Lease owner", leaseOwner},
			{"Lease expiry", leaseExpiry},
		} {
			value := field.value
			if value == "" {
				value = "-"
			}
			if _, err := fmt.Fprintf(writer, "%s: %s\n", field.label, value); err != nil {
				return err
			}
		}
		hasError := len(task.Error) > 0 && string(task.Error) != "null"
		if _, err := fmt.Fprintf(writer, "Has error: %t\n", hasError); err != nil {
			return err
		}
	}
	if hasMore {
		_, err := fmt.Fprintln(writer, "\nMore Tasks are available. Use --output json to read opaque last_id, "+
			"then pass it unchanged to --after. Keep the same filters, --order, and --limit.")
		return err
	}
	return nil
}
