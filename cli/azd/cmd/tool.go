// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/tool"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
)

// singleResultCommonAttrs returns the usage attributes shared by single-target
// `azd tool install` and `azd tool upgrade`: success, tool.id, and the
// installation strategy. Callers append upgrade-specific version attrs
// (tool.upgrade.{from,to}_version) on top.
//
// Returns nil if r is nil so callers can safely pass through results without
// pre-validating the slice element.
func singleResultCommonAttrs(r *tool.InstallResult) []attribute.KeyValue {
	if r == nil {
		return nil
	}
	attrs := []attribute.KeyValue{
		fields.ToolInstallSuccessKey.Bool(r.Success),
	}
	if r.Tool != nil {
		attrs = append(attrs, fields.ToolIdKey.String(r.Tool.Id))
	}
	if r.Strategy != "" {
		attrs = append(attrs, fields.ToolInstallStrategyKey.String(r.Strategy))
	}
	return attrs
}

// emitToolInstallTelemetry emits aggregate telemetry attributes for a batch
// install or upgrade operation. When the batch contains exactly one tool the
// caller is responsible for also emitting tool.id, tool.install.strategy, and
// tool.install.success (and, for upgrades, tool.upgrade.{from,to}_version).
//
// When the batch infrastructure itself fails (opErr != nil and results is
// empty) every requested tool is counted as a failure and its ID is added to
// failed_ids. This preserves the invariant
// success_count + failure_count == len(requested) and prevents a hard
// operation error from being indistinguishable from a no-op.
func emitToolInstallTelemetry(
	results []*tool.InstallResult,
	elapsed time.Duration,
	opErr error,
	requested []*tool.ToolDefinition,
) {
	requestedIDs := make([]string, 0, len(requested))
	for _, t := range requested {
		if t != nil {
			requestedIDs = append(requestedIDs, t.Id)
		}
	}

	successCount, failureCount, sortedFailedIDs := tool.AggregateInstallResults(results, opErr, requestedIDs)

	attrs := []attribute.KeyValue{
		fields.ToolInstallSuccessCountKey.Int(successCount),
		fields.ToolInstallFailureCountKey.Int(failureCount),
		fields.ToolInstallDurationMsKey.Int64(elapsed.Milliseconds()),
	}
	if len(sortedFailedIDs) > 0 {
		attrs = append(attrs,
			fields.ToolInstallFailedIdsKey.String(strings.Join(sortedFailedIDs, ",")),
		)
	}
	tracing.SetUsageAttributes(attrs...)
}

// toolActions registers the "azd tool" command group and all of its subcommands.
func toolActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	toolCmd := &cobra.Command{
		Use:   "tool",
		Short: "Manage Azure development tools.",
		Long:  "Discover, install, upgrade, and check status of Azure development tools.",
	}

	group := root.Add("tool", &actions.ActionDescriptorOptions{
		Command: toolCmd,
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupManage,
		},
		ActionResolver: newToolAction,
	})

	// azd tool list
	group.Add("list", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "list",
			Short: "List all tools with status.",
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat:  output.TableFormat,
		ActionResolver: newToolListAction,
	})

	// azd tool install [tool-name...]
	group.Add("install", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "install [tool-name...]",
			Short: "Install specified tools.",
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newToolInstallAction,
		FlagsResolver:  newToolInstallFlags,
	})

	// azd tool upgrade [tool-name...]
	group.Add("upgrade", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "upgrade [tool-name...]",
			Short: "Upgrade installed tools.",
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newToolUpgradeAction,
		FlagsResolver:  newToolUpgradeFlags,
	})

	// azd tool uninstall [tool-name...]
	group.Add("uninstall", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "uninstall [tool-name...]",
			Short: "Uninstall installed tools.",
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newToolUninstallAction,
		FlagsResolver:  newToolUninstallFlags,
	})

	// azd tool check
	group.Add("check", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "check",
			Short: "Check for tool upgrades.",
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat:  output.TableFormat,
		ActionResolver: newToolCheckAction,
	})

	// azd tool show <tool-name>
	group.Add("show", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "show <tool-name>",
			Short: "Show details for a specific tool.",
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newToolShowAction,
	})

	return group
}

// ---------------------------------------------------------------------------
// azd tool (bare command) — interactive flow
// ---------------------------------------------------------------------------

type toolAction struct {
	manager *tool.Manager
	console input.Console
}

func newToolAction(
	manager *tool.Manager,
	console input.Console,
) actions.Action {
	return &toolAction{
		manager: manager,
		console: console,
	}
}

func (a *toolAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Azure Development Tools (azd tool)",
		TitleNote: "Discover and install tools for Azure development",
	})

	// 1. Detect all tools.
	var statuses []*tool.ToolStatus
	spinner := uxlib.NewSpinner(&uxlib.SpinnerOptions{
		Text:        "Detecting tools...",
		ClearOnStop: true,
	})
	if err := spinner.Run(ctx, func(ctx context.Context) error {
		var detectErr error
		statuses, detectErr = a.manager.DetectAll(ctx)
		return detectErr
	}); err != nil {
		return nil, fmt.Errorf("detecting tools: %w", err)
	}

	// 2. Display current status.
	a.console.Message(ctx, "")
	for _, s := range statuses {
		if s.Installed {
			version := s.InstalledVersion
			if version == "" {
				version = "unknown"
			}
			a.console.Message(ctx, fmt.Sprintf(
				"  %s %s %s",
				output.WithSuccessFormat("(✔)"),
				s.Tool.Name,
				output.WithGrayFormat("(%s)", version),
			))
		} else if s.Tool.Priority == tool.ToolPriorityRecommended {
			a.console.Message(ctx, fmt.Sprintf(
				"  %s %s %s",
				output.WithWarningFormat("(○)"),
				s.Tool.Name,
				output.WithWarningFormat("[recommended]"),
			))
		} else {
			a.console.Message(ctx, fmt.Sprintf(
				"  %s %s %s",
				output.WithGrayFormat("(○)"),
				s.Tool.Name,
				output.WithGrayFormat("[optional]"),
			))
		}
	}
	a.console.Message(ctx, "")

	// 3. Collect uninstalled tools for interactive selection.
	var uninstalled []*tool.ToolStatus
	for _, s := range statuses {
		if !s.Installed {
			uninstalled = append(uninstalled, s)
		}
	}

	if len(uninstalled) == 0 {
		a.console.Message(ctx, output.WithSuccessFormat("All tools are installed!"))
		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header: "All tools are already installed",
			},
		}, nil
	}

	// 4. MultiSelect uninstalled tools.
	choices := make([]*uxlib.MultiSelectChoice, len(uninstalled))
	for i, s := range uninstalled {
		choices[i] = &uxlib.MultiSelectChoice{
			Value:    s.Tool.Id,
			Label:    s.Tool.Name,
			Selected: s.Tool.Priority == tool.ToolPriorityRecommended,
		}
	}

	multiSelect := uxlib.NewMultiSelect(&uxlib.MultiSelectOptions{
		Writer:  a.console.Handles().Stdout,
		Reader:  a.console.Handles().Stdin,
		Message: "Select tools to install",
		Choices: choices,
	})

	selected, err := multiSelect.Ask(ctx)
	if err != nil {
		return nil, fmt.Errorf("selecting tools: %w", err)
	}

	// 5. Install selected tools using TaskList.
	var ids []string
	for _, choice := range selected {
		if choice.Selected {
			ids = append(ids, choice.Value)
		}
	}

	if len(ids) == 0 {
		return nil, nil
	}

	tools := make([]*tool.ToolDefinition, 0, len(ids))
	for _, id := range ids {
		toolDef, findErr := a.manager.FindTool(id)
		if findErr != nil {
			return nil, findErr
		}
		tools = append(tools, toolDef)
	}

	operationFn := func(ctx context.Context, allIDs []string) ([]*tool.InstallResult, error) {
		return a.manager.InstallTools(ctx, allIDs)
	}

	_ = runToolOperation(ctx, tools, operationFn, "Installing", "install", a.console, false)
	// runToolOperation already displayed warnings; we intentionally
	// discard the outcome here — child tasks have surfaced what the user
	// needs to see, and this caller does not propagate the task error.

	header := "Your tool is installed."
	if len(tools) > 1 {
		header = "Your tools are installed."
	}
	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: header,
		},
	}, nil
}

// ---------------------------------------------------------------------------
// azd tool list
// ---------------------------------------------------------------------------

type toolListItem struct {
	Id   string `json:"id"`
	Name string `json:"name"`
	// Agent is the agent CLI a skill row is installed through (e.g.
	// "copilot"), empty for non-skill tools.
	Agent    string `json:"agent,omitempty"`
	Category string `json:"category"`
	Priority string `json:"priority"`
	Status   string `json:"status"`
	Version  string `json:"version"`
	// DisplayName is the NAME cell shown in the table: a skill row is
	// prefixed with its agent label (e.g. "[Copilot] Azure Skills"), other
	// rows use the plain name. Excluded from JSON, which carries Name + Agent.
	DisplayName string `json:"-"`
}

type toolListAction struct {
	manager   *tool.Manager
	console   input.Console
	formatter output.Formatter
	writer    io.Writer
}

func newToolListAction(
	manager *tool.Manager,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
) actions.Action {
	return &toolListAction{
		manager:   manager,
		console:   console,
		formatter: formatter,
		writer:    writer,
	}
}

func (a *toolListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	var statuses []*tool.ToolStatus
	if a.formatter.Kind() != output.JsonFormat {
		spinner := uxlib.NewSpinner(&uxlib.SpinnerOptions{
			Text:        "Checking tool status...",
			ClearOnStop: true,
		})
		if err := spinner.Run(ctx, func(ctx context.Context) error {
			var detectErr error
			statuses, detectErr = a.manager.DetectAll(ctx)
			return detectErr
		}); err != nil {
			return nil, fmt.Errorf("detecting tools: %w", err)
		}
	} else {
		var err error
		statuses, err = a.manager.DetectAll(ctx)
		if err != nil {
			return nil, fmt.Errorf("detecting tools: %w", err)
		}
	}

	rows := make([]toolListItem, 0, len(statuses))
	for _, s := range statuses {
		// A skill installed on one or more agents expands into one row per
		// agent, each prefixed with the agent label (e.g. "[Copilot] ...").
		if s.Tool.Category == tool.ToolCategorySkill && len(s.SkillAgents) > 0 {
			for _, h := range s.SkillAgents {
				rows = append(rows, toolListItem{
					Id:    s.Tool.Id,
					Name:  s.Tool.Name,
					Agent: h.Agent,
					DisplayName: fmt.Sprintf("[%s] %s",
						skillAgentDisplayName(s.Tool, h.Agent), s.Tool.Name),
					Category: string(s.Tool.Category),
					Priority: string(s.Tool.Priority),
					Status:   "Installed",
					Version:  h.Version,
				})
			}
			continue
		}

		status := "Not installed"
		version := ""
		if s.Installed {
			status = "Installed"
			version = s.InstalledVersion
		}

		rows = append(rows, toolListItem{
			Id:          s.Tool.Id,
			Name:        s.Tool.Name,
			DisplayName: s.Tool.Name,
			Category:    string(s.Tool.Category),
			Priority:    string(s.Tool.Priority),
			Status:      status,
			Version:     version,
		})
	}

	if len(rows) == 0 {
		a.console.Message(ctx, output.WithWarningFormat("No tools found in the registry."))
		return nil, nil
	}

	var formatErr error

	if a.formatter.Kind() == output.TableFormat {
		prettyFormatter := &output.PrettyTableFormatter{}
		columns := []output.PrettyColumn{
			{
				Column:   output.Column{Heading: "ID", ValueTemplate: "{{.Id}}"},
				Priority: 1,
			},
			{
				Column:      output.Column{Heading: "NAME", ValueTemplate: "{{.DisplayName}}"},
				Priority:    2,
				CardTitle:   true,
				Wrappable:   true,
				Truncatable: true,
				ColorFunc:   colorAgentPrefix,
			},
			{
				Column:      output.Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"},
				Priority:    1,
				Truncatable: true,
				ColorFunc:   toolStatusColor,
			},
			{
				Column: output.Column{
					Heading:       "INSTALLED",
					ValueTemplate: `{{if .Version}}{{.Version}}{{else}}-{{end}}`,
				},
				CardValueTemplate: `{{if .Version}}{{.Version}}{{end}}`,
				Priority:          1,
			},
			{
				Column:      output.Column{Heading: "CATEGORY", ValueTemplate: "{{.Category}}"},
				Priority:    3,
				Truncatable: true,
			},
		}

		formatErr = prettyFormatter.Format(rows, a.writer, output.PrettyTableFormatterOptions{
			Columns:              columns,
			CardGroupColumn:      "CATEGORY",
			ResponsiveColumnHint: true,
		})
	} else {
		formatErr = a.formatter.Format(rows, a.writer, nil)
	}

	return nil, formatErr
}

// ---------------------------------------------------------------------------
// azd tool install [tool-name...]
// ---------------------------------------------------------------------------

type toolInstallFlags struct {
	all    bool
	agents []string
	dryRun bool
}

func newToolInstallFlags(cmd *cobra.Command) *toolInstallFlags {
	flags := &toolInstallFlags{}
	cmd.Flags().BoolVar(
		&flags.all, "all", false, "Install all recommended tools",
	)
	cmd.Flags().StringSliceVar(
		&flags.agents, "agent", nil,
		"Install the skill for the specified agent(s): copilot, claude. "+
			"Use --agent all for every detected agent (skill tools only)",
	)
	cmd.Flags().BoolVar(
		&flags.dryRun, "dry-run", false,
		"Preview what would be installed without making changes",
	)
	return flags
}

type toolInstallAction struct {
	args      []string
	flags     *toolInstallFlags
	manager   *tool.Manager
	console   input.Console
	formatter output.Formatter
	writer    io.Writer
}

func newToolInstallAction(
	args []string,
	flags *toolInstallFlags,
	manager *tool.Manager,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
) actions.Action {
	return &toolInstallAction{
		args:      args,
		flags:     flags,
		manager:   manager,
		console:   console,
		formatter: formatter,
		writer:    writer,
	}
}

func (a *toolInstallAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	ids, err := a.resolveToolIds(ctx)
	if err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		if a.formatter.Kind() == output.JsonFormat {
			// Keep a stable array schema for automation instead of a
			// consoleMessage object.
			return nil, a.formatter.Format([]*toolInstallResultItem{}, a.writer, nil)
		}
		a.console.Message(ctx, output.WithSuccessFormat("Nothing to install."))
		return nil, nil
	}

	// --dry-run: detect tool status and display what would happen
	// without actually installing anything.
	if a.flags.dryRun {
		return a.dryRun(ctx, ids)
	}

	if a.formatter.Kind() != output.JsonFormat {
		a.console.MessageUxItem(ctx, &ux.MessageTitle{
			Title:     "Install Azure development tools (azd tool install)",
			TitleNote: "Installs specified tools onto the local machine",
		})
	}

	tools := make([]*tool.ToolDefinition, 0, len(ids))
	resolvedIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		toolDef, findErr := a.manager.FindTool(id)
		if findErr != nil {
			return nil, wrapToolNotFoundIfErr(findErr)
		}
		tools = append(tools, toolDef)
		resolvedIDs = append(resolvedIDs, toolDef.Id)
	}

	// Emit tool.id (single) or tool.ids (batch) — never both. See
	// toolIDUsageAttrs and the rationale in tracing-in-azd.md.
	idAttrs := toolIDUsageAttrs(a.flags.dryRun, resolvedIDs)
	tracing.SetUsageAttributes(idAttrs...)

	// Resolve which agents to install skills for, based on the
	// --agent flag. When no agent is given and several are detected, the
	// user is asked to choose explicitly.
	agentOpts, agentErr := a.resolveAgentOptions(ctx, tools)
	if agentErr != nil {
		return nil, agentErr
	}

	// In JSON mode, capture skill agent CLI output so it never leaks onto
	// stdout ahead of the structured result.
	if a.formatter.Kind() == output.JsonFormat {
		agentOpts = append(agentOpts, tool.WithQuiet())
	}

	start := time.Now()

	var (
		installResults []*toolInstallResultItem
		rawResults     []*tool.InstallResult
		opErr          error
	)

	if useStepSpinner(a.console, a.formatter, tools) {
		// Tools render live per-step progress with the step spinner (like
		// azd provision). JSON output is gated out of this path and
		// handled below via the per-tool results.
		rawResults, opErr = runStepSpinner(
			ctx, a.console, tools,
			func(ctx context.Context, ids []string, progress ...tool.InstallOption) ([]*tool.InstallResult, error) {
				return a.manager.InstallTools(ctx, ids, append(slices.Clone(agentOpts), progress...)...)
			},
		)
	} else {
		operationFn := func(ctx context.Context, allIDs []string) ([]*tool.InstallResult, error) {
			return a.manager.InstallTools(ctx, allIDs, agentOpts...)
		}
		outcome := runToolOperation(ctx, tools, operationFn, "Installing", "install", a.console,
			a.formatter.Kind() == output.JsonFormat)
		installResults = outcome.Items
		rawResults = outcome.Results
		opErr = outcome.Err
	}

	emitToolInstallTelemetry(rawResults, time.Since(start), opErr, tools)

	if len(rawResults) == 1 {
		tracing.SetUsageAttributes(singleResultCommonAttrs(rawResults[0])...)
	}

	if a.formatter.Kind() == output.JsonFormat {
		return nil, a.formatter.Format(installResults, a.writer, nil)
	}

	// When one or more tools failed, surface the error so the command
	// exits non-zero and the success header is NOT printed. The per-tool
	// failures were already shown inline.
	if opErr != nil {
		return nil, opErr
	}

	header := "Your tool is installed."
	if len(tools) > 1 {
		header = "Your tools are installed."
	}

	// Surface any per-tool follow-up guidance (e.g. how to start using a skill)
	// after the success header.
	var notes []string
	for _, t := range tools {
		if t.SpinnerNote != "" {
			notes = append(notes, t.SpinnerNote)
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   header,
			FollowUp: strings.Join(notes, "\n"),
		},
	}, nil
}

// allAgentsKeyword is the reserved --agent value that selects every
// detected agent.
const allAgentsKeyword = "all"

// firstSkillTool returns the first skill tool among tools, or nil when
// none are present.
func firstSkillTool(tools []*tool.ToolDefinition) *tool.ToolDefinition {
	for _, t := range tools {
		if t.Category == tool.ToolCategorySkill {
			return t
		}
	}
	return nil
}

// resolveExplicitSkillAgents maps an explicit --agent flag value to install
// options. The reserved value "all" installs through every available
// agent (resolved at install time); otherwise the named agents are passed
// through for the installer to validate. Shared by the install and
// upgrade actions.
func resolveExplicitSkillAgents(agents []string) ([]tool.InstallOption, error) {
	// --agent all selects every detected agent. It cannot be mixed with
	// specific agent names.
	if slices.Contains(agents, allAgentsKeyword) {
		if len(agents) > 1 {
			return nil, fmt.Errorf(
				"--agent all cannot be combined with specific agents",
			)
		}
		return []tool.InstallOption{tool.WithAllAvailableAgents()}, nil
	}
	// The installer validates that each named agent is configured and on
	// PATH, surfacing a descriptive error otherwise.
	return []tool.InstallOption{tool.WithAgents(agents...)}, nil
}

// resolveAgentOptions determines which agent CLI(s) a skill should
// be installed for. With --agent it targets the named agent(s); --agent all
// targets every detected agent. Without --agent, a skill pulled in by a
// batch (--all or the interactive picker) installs through every
// available agent, while an explicitly-named skill with several detected
// agents returns guidance asking the user to choose. It returns the
// install options to pass to the installer (nil selects the single
// preferred agent).
//
// When an explicitly-named skill has several agents on PATH, an
// interactive terminal is prompted to choose which agents to install
// for (we still print a --agent hint); in non-interactive mode it falls
// back to a guidance error telling the user to re-run with --agent.
func (a *toolInstallAction) resolveAgentOptions(
	ctx context.Context,
	tools []*tool.ToolDefinition,
) ([]tool.InstallOption, error) {
	explicit := len(a.flags.agents) > 0
	skill := firstSkillTool(tools)

	if explicit && skill == nil {
		return nil, fmt.Errorf("--agent only applies to skill tools")
	}
	if skill == nil {
		return nil, nil
	}

	if explicit {
		// "all" expands to every detected agent and is validated at
		// install time. Specific agent names are checked here so an
		// unusable agent (unknown name or not on PATH) can fall back to
		// an interactive picker instead of hard-failing.
		if !slices.Contains(a.flags.agents, allAgentsKeyword) {
			if opts, handled, err := a.resolveUnavailableAgentPrompt(ctx, skill); handled || err != nil {
				return opts, err
			}
		}
		return resolveExplicitSkillAgents(a.flags.agents)
	}

	// No --agent. A skill the user did not name explicitly (batch --all or
	// interactive selection) installs through every available agent,
	// resolved at install time so agent CLIs installed earlier in the same
	// batch are picked up. This is also why --all does not abort when
	// several agents are present.
	if !slices.Contains(a.args, skill.Id) {
		return []tool.InstallOption{tool.WithAllAvailableAgents()}, nil
	}

	// Explicitly-named skill: when multiple agents are detected we cannot
	// safely guess which the user wants.
	present, presentName := a.manager.AvailableSkillAgents(ctx, skill)
	if len(present) > 1 {
		// Interactive terminal: prompt the user to pick the agent(s),
		// after surfacing the --agent hint so they learn the shortcut too.
		if promptAllowed(a.console, a.formatter) {
			a.console.Message(ctx, "Multiple AI agents detected.\n"+
				output.WithGrayFormat("Tip: Use `")+
				output.WithHighLightFormat("--agent <agent>")+
				output.WithGrayFormat("` or `")+
				output.WithHighLightFormat("--agent all")+
				output.WithGrayFormat("` to select a specific agent or all agents.\n"))

			opts, err := a.promptForSkillAgents(ctx, skill, present, presentName)
			if err != nil {
				return nil, err
			}
			if opts != nil {
				return opts, nil
			}
			// Nothing selected — fall through to the guidance error.
		}

		return nil, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf("multiple AI agents detected for %s", skill.Name),
			Message: fmt.Sprintf(
				"Detected multiple agents: %s", strings.Join(presentName, ", "),
			),
			Suggestion: fmt.Sprintf(
				"Specify which agents to install for:\n\n"+
					"    azd tool install %s --agent <agent>\n\n"+
					"    azd tool install %s --agent all",
				skill.Id, skill.Id,
			),
		}
	}

	// Zero or one agent detected: keep the single preferred-agent default.
	return nil, nil
}

// resolveUnavailableAgentPrompt handles an explicit --agent whose named
// agents are not usable (unknown name or not on PATH). In an
// interactive terminal it tells the user the requested agent is
// unavailable and prompts them to pick from the agents detected on PATH;
// the chosen agents are returned with handled=true. When no supported
// agent is on PATH at all it defers to the installer's install guidance
// (handled=true via WithAllAvailableAgents). In non-interactive mode, or
// when every requested agent is already available, it returns
// handled=false so the caller validates the request as usual.
func (a *toolInstallAction) resolveUnavailableAgentPrompt(
	ctx context.Context,
	skill *tool.ToolDefinition,
) (opts []tool.InstallOption, handled bool, err error) {
	if !promptAllowed(a.console, a.formatter) {
		return nil, false, nil
	}

	available, availableNames := a.manager.AvailableSkillAgents(ctx, skill)
	var unavailable []string
	for _, agent := range a.flags.agents {
		// Match case-insensitively, mirroring findSkillAgent and the --agent
		// contract, so e.g. "--agent Copilot" is not falsely reported
		// unavailable (and does not open another prompt) when the installer
		// would accept it.
		if !slices.ContainsFunc(available, func(cmd string) bool {
			return strings.EqualFold(cmd, agent)
		}) {
			unavailable = append(unavailable, fmt.Sprintf("%q", agent))
		}
	}
	if len(unavailable) == 0 {
		return nil, false, nil
	}

	// No usable agent on PATH — defer to the installer's install guidance
	// (recommends installing a CLI agent first) by targeting every
	// available agent.
	if len(available) == 0 {
		return []tool.InstallOption{tool.WithAllAvailableAgents()}, true, nil
	}

	a.console.Message(ctx, fmt.Sprintf(
		"Agent %s is not available for %s. Choose from the agents detected "+
			"on your PATH:",
		strings.Join(unavailable, ", "), skill.Name,
	))
	picked, err := a.promptForSkillAgents(ctx, skill, available, availableNames)
	if err != nil {
		return nil, false, err
	}
	// Nothing selected — let the caller surface the installer's
	// validation error for the originally requested agent.
	if picked == nil {
		return nil, false, nil
	}
	return picked, true, nil
}

// promptForSkillAgents shows an interactive multi-select over the given
// available agents and returns the matching install option, or (nil, nil)
// when the user selects nothing so callers can fall back to their own
// guidance. commands and names are index-aligned (from AvailableSkillAgents):
// the picker displays the friendly name for each agent and maps the selection
// back to its command so the installer resolves it by command.
func (a *toolInstallAction) promptForSkillAgents(
	ctx context.Context,
	skill *tool.ToolDefinition,
	commands []string,
	names []string,
) ([]tool.InstallOption, error) {
	toCommand := make(map[string]string, len(names))
	for i, name := range names {
		toCommand[name] = commands[i]
	}

	selected, err := a.console.MultiSelect(ctx, input.ConsoleOptions{
		Message: fmt.Sprintf(
			"Select the agents to install %s for", skill.Name,
		),
		Options:      names,
		DefaultValue: []string{names[0]},
	})
	if err != nil {
		return nil, fmt.Errorf("selecting agents: %w", err)
	}
	if len(selected) == 0 {
		return nil, nil
	}

	picked := make([]string, len(selected))
	for i, name := range selected {
		picked[i] = toCommand[name]
	}
	return []tool.InstallOption{tool.WithAgents(picked...)}, nil
}

// detectAllTools runs manager.DetectAll to get the status of every tool,
// showing a detection spinner with the given text. The spinner is skipped when
// the output format is JSON: it writes control bytes to stdout and would
// corrupt the machine-readable stream (matching tool list/check, which also
// bypass the spinner in JSON mode).
func detectAllTools(
	ctx context.Context,
	manager *tool.Manager,
	formatter output.Formatter,
	writer io.Writer,
	spinnerText string,
) ([]*tool.ToolStatus, error) {
	if formatter != nil && formatter.Kind() == output.JsonFormat {
		return manager.DetectAll(ctx)
	}

	var statuses []*tool.ToolStatus
	spinner := uxlib.NewSpinner(&uxlib.SpinnerOptions{
		Text:        spinnerText,
		ClearOnStop: true,
		Writer:      writer,
	})
	if err := spinner.Run(ctx, func(ctx context.Context) error {
		var detectErr error
		statuses, detectErr = manager.DetectAll(ctx)
		return detectErr
	}); err != nil {
		return nil, err
	}
	return statuses, nil
}

// promptAllowed reports whether azd may show an interactive prompt for a tool
// operation. Prompting requires an interactive terminal, that --no-prompt is
// not set, and that output is not JSON: a JSON-mode prompt writes control bytes
// to the same stdout the result array is serialized to, so the output would no
// longer be a single valid JSON document. When prompting is not allowed, callers
// must require an explicit target (tool IDs / --agent / --all) instead.
func promptAllowed(console input.Console, formatter output.Formatter) bool {
	return console.IsSpinnerInteractive() &&
		!console.IsNoPromptMode() &&
		(formatter == nil || formatter.Kind() != output.JsonFormat)
}

// useStepSpinner reports whether a tool operation should render live
// per-step progress with the step spinner (like azd provision) instead of
// the batch task list. It applies to any interactive, non-JSON operation.
func useStepSpinner(
	console input.Console,
	formatter output.Formatter,
	tools []*tool.ToolDefinition,
) bool {
	return len(tools) > 0 &&
		formatter.Kind() != output.JsonFormat &&
		console.IsSpinnerInteractive()
}

// runStepSpinner runs a tool install/upgrade/uninstall with a live per-step
// step spinner (like azd provision): the installer renders each step via the
// console (each targeted agent for a skill tool, or the tool itself
// otherwise). run performs the manager call with the step-progress option
// appended to its own options. It returns the per-tool results (for
// telemetry) and an aggregate error when any step failed.
func runStepSpinner(
	ctx context.Context,
	console input.Console,
	tools []*tool.ToolDefinition,
	run func(context.Context, []string, ...tool.InstallOption) ([]*tool.InstallResult, error),
) ([]*tool.InstallResult, error) {
	ids := make([]string, len(tools))
	for i, t := range tools {
		ids[i] = t.Id
	}

	results, err := run(ctx, ids, tool.WithStepProgress(console), tool.WithInput(console.Handles().Stdin))
	if err != nil {
		return results, err
	}

	// Per-step failures were shown inline by the installer; surface an
	// aggregate error so the command still exits non-zero.
	var failed []error
	for _, r := range results {
		if r.Error != nil {
			failed = append(failed, r.Error)
		}
	}
	if len(failed) > 0 {
		return results, errors.Join(failed...)
	}
	return results, nil
}

// dryRun detects the current status of the requested tools and
// displays what the install command would do without making changes.
func (a *toolInstallAction) dryRun(
	ctx context.Context,
	ids []string,
) (*actions.ActionResult, error) {
	rows := make([]toolDryRunItem, 0, len(ids))
	resolvedIDs := make([]string, 0, len(ids))

	for _, id := range ids {
		toolDef, findErr := a.manager.FindTool(id)
		if findErr != nil {
			return nil, wrapToolNotFoundIfErr(findErr)
		}

		status, detectErr := a.manager.DetectTool(ctx, id)
		if detectErr != nil {
			return nil, fmt.Errorf("detecting %s: %w", id, detectErr)
		}

		action := "install"
		currentVersion := ""
		if status.Installed {
			action = "skip (already installed)"
			currentVersion = status.InstalledVersion
		}

		rows = append(rows, toolDryRunItem{
			Id:             id,
			Name:           toolDef.Name,
			CurrentVersion: currentVersion,
			Action:         action,
		})
		resolvedIDs = append(resolvedIDs, id)
	}

	// Dry-run emits the same tool.id/tool.ids contract as the real install
	// path; dry_run is hardcoded true here. See toolIDUsageAttrs.
	idAttrs := toolIDUsageAttrs(true, resolvedIDs)
	tracing.SetUsageAttributes(idAttrs...)

	if a.formatter.Kind() == output.JsonFormat {
		return nil, a.formatter.Format(rows, a.writer, nil)
	}

	if err := writeDryRunTable(a.writer, rows); err != nil {
		return nil, err
	}

	a.console.Message(ctx, "")
	a.console.Message(ctx, output.WithGrayFormat(
		"Dry run complete. No changes were made.",
	))

	return nil, nil
}

// noToolTargetError builds the guidance error returned when a tool operation
// cannot determine which tools to act on: prompting is unavailable (a
// non-interactive terminal or --no-prompt) and the user gave neither tool IDs
// nor --all. All three commands (install, upgrade, uninstall) require an
// explicit target here rather than guessing, matching azd's --no-prompt
// contract of failing with a structured error instead of an implicit default.
func noToolTargetError(command string) error {
	return &internal.ErrorWithSuggestion{
		Err:     fmt.Errorf("no tools specified to %s", command),
		Message: fmt.Sprintf("A tool ID is required to %s", command),
		Suggestion: fmt.Sprintf(
			"Specify one or more tool IDs, or --all:\n\n"+
				"    azd tool %s <tool-id> [<tool-id> ...]\n\n"+
				"    azd tool %s --all",
			command, command,
		),
	}
}

// toolIDsWithAllError is returned when a tool operation is given both explicit
// tool IDs and --all. --all applies only when no tool ID is specified, so the
// combination is rejected rather than silently ignoring one of them
func toolIDsWithAllError(command string) error {
	return &internal.ErrorWithSuggestion{
		Err: fmt.Errorf(
			"cannot specify both tool IDs and --all: %w",
			internal.ErrInvalidFlagCombination),
		Message: "Tool IDs and --all cannot be combined.",
		Suggestion: fmt.Sprintf(
			"Use either:\n"+
				"    azd tool %s <tool-id>\n"+
				"    azd tool %s --all",
			command, command),
	}
}

// resolveToolIds determines which tool IDs to install based on flags and arguments.
func (a *toolInstallAction) resolveToolIds(ctx context.Context) ([]string, error) {
	if len(a.args) > 0 && a.flags.all {
		return nil, toolIDsWithAllError("install")
	}

	// --all: install all recommended tools that are not already installed.
	if a.flags.all {
		statuses, err := detectAllTools(ctx, a.manager, a.formatter, a.writer, "Detecting tool status...")
		if err != nil {
			return nil, fmt.Errorf("detecting tools: %w", err)
		}

		var ids []string
		for _, s := range statuses {
			if !s.Installed && s.Tool.Priority == tool.ToolPriorityRecommended {
				ids = append(ids, s.Tool.Id)
			}
		}
		return ids, nil
	}

	// Positional args: install specified tools by ID.
	if len(a.args) > 0 {
		return a.args, nil
	}

	// Interactive: let the user pick from uninstalled tools.
	statuses, err := detectAllTools(ctx, a.manager, a.formatter, a.writer, "Detecting tool status...")
	if err != nil {
		return nil, fmt.Errorf("detecting tools: %w", err)
	}

	var uninstalled []*tool.ToolStatus
	for _, s := range statuses {
		if !s.Installed {
			uninstalled = append(uninstalled, s)
		}
	}

	if len(uninstalled) == 0 {
		return nil, nil
	}

	// Non-interactive (no TTY), --no-prompt, or JSON output: the picker can't
	// run (or would corrupt JSON), so require an explicit target (tool IDs or
	// --all) rather than implicitly installing the recommended set.
	if !promptAllowed(a.console, a.formatter) {
		return nil, noToolTargetError("install")
	}

	choices := make([]*uxlib.MultiSelectChoice, len(uninstalled))
	for i, s := range uninstalled {
		choices[i] = &uxlib.MultiSelectChoice{
			Value:    s.Tool.Id,
			Label:    s.Tool.Name,
			Selected: s.Tool.Priority == tool.ToolPriorityRecommended,
		}
	}

	multiSelect := uxlib.NewMultiSelect(&uxlib.MultiSelectOptions{
		Writer:  a.console.Handles().Stdout,
		Reader:  a.console.Handles().Stdin,
		Message: "Select tools to install",
		Choices: choices,
	})

	selected, err := multiSelect.Ask(ctx)
	if err != nil {
		return nil, fmt.Errorf("selecting tools: %w", err)
	}

	var ids []string
	for _, choice := range selected {
		if choice.Selected {
			ids = append(ids, choice.Value)
		}
	}
	return ids, nil
}

// ---------------------------------------------------------------------------
// azd tool upgrade [tool-name...]
// ---------------------------------------------------------------------------

type toolUpgradeFlags struct {
	all    bool
	dryRun bool
	agents []string
}

func newToolUpgradeFlags(cmd *cobra.Command) *toolUpgradeFlags {
	flags := &toolUpgradeFlags{}
	cmd.Flags().BoolVar(
		&flags.all, "all", false,
		"Upgrade all installed tools",
	)
	cmd.Flags().BoolVar(
		&flags.dryRun, "dry-run", false,
		"Preview what would be upgraded without making changes",
	)
	cmd.Flags().StringSliceVar(
		&flags.agents, "agent", nil,
		"Upgrade the skill for the specified agent(s): copilot, claude. "+
			"Use --agent all for every detected agent (skill tools only)",
	)
	return flags
}

type toolUpgradeAction struct {
	args      []string
	flags     *toolUpgradeFlags
	manager   *tool.Manager
	console   input.Console
	formatter output.Formatter
	writer    io.Writer
}

func newToolUpgradeAction(
	args []string,
	flags *toolUpgradeFlags,
	manager *tool.Manager,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
) actions.Action {
	return &toolUpgradeAction{
		args:      args,
		flags:     flags,
		manager:   manager,
		console:   console,
		formatter: formatter,
		writer:    writer,
	}
}

func (a *toolUpgradeAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Determine which tools to upgrade — resolve tool definitions
	// up front but defer the actual upgrade work to each task callback
	// so that the spinner reflects real-time progress.
	var toolsToUpgrade []*tool.ToolDefinition

	// fromVersions captures the pre-upgrade installed version per tool ID,
	// populated on both branches so that tool.upgrade.from_version is
	// emitted on the single-tool path regardless of whether the user
	// supplied explicit args. Detection failures are non-fatal here —
	// from_version is a best-effort telemetry signal, not a precondition
	// for upgrading.
	fromVersions := make(map[string]string)

	if len(a.args) > 0 && a.flags.all {
		return nil, toolIDsWithAllError("upgrade")
	}

	switch {
	case a.flags.all:
		// --all: upgrade every installed tool.
		statuses, err := a.detectInstalledTools(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range statuses {
			if s.Installed {
				toolsToUpgrade = append(toolsToUpgrade, s.Tool)
				if s.Tool != nil {
					fromVersions[s.Tool.Id] = s.InstalledVersion
				}
			}
		}
	case len(a.args) > 0:
		for _, id := range a.args {
			toolDef, findErr := a.manager.FindTool(id)
			if findErr != nil {
				return nil, wrapToolNotFoundIfErr(findErr)
			}
			toolsToUpgrade = append(toolsToUpgrade, toolDef)
			if status, detectErr := a.manager.DetectTool(ctx, toolDef.Id); detectErr == nil &&
				status != nil && status.Installed {
				fromVersions[toolDef.Id] = status.InstalledVersion
			}
		}
	default:
		// No args: prompt the user to pick from installed tools (like
		// `azd tool install`). When prompting is unavailable, require an
		// explicit target instead of upgrading everything implicitly.
		statuses, err := a.detectInstalledTools(ctx)
		if err != nil {
			return nil, err
		}
		var installed []*tool.ToolStatus
		for _, s := range statuses {
			if s.Installed {
				installed = append(installed, s)
			}
		}
		// Non-interactive (no TTY), --no-prompt, or JSON output: the picker
		// can't run (or would corrupt JSON), so require an explicit target
		// (tool IDs or --all) rather than implicitly upgrading every tool.
		if len(installed) > 0 && !promptAllowed(a.console, a.formatter) {
			return nil, noToolTargetError("upgrade")
		}
		chosen := installed
		if promptAllowed(a.console, a.formatter) && len(installed) > 0 {
			chosen, err = a.promptForUpgradeTools(ctx, installed)
			if err != nil {
				return nil, err
			}
		}
		for _, s := range chosen {
			toolsToUpgrade = append(toolsToUpgrade, s.Tool)
			if s.Tool != nil {
				fromVersions[s.Tool.Id] = s.InstalledVersion
			}
		}
	}

	if len(toolsToUpgrade) == 0 {
		if a.formatter.Kind() == output.JsonFormat {
			// Keep a stable array schema for automation instead of a
			// consoleMessage object.
			return nil, a.formatter.Format([]*toolInstallResultItem{}, a.writer, nil)
		}
		a.console.Message(ctx, output.WithGrayFormat(
			"No installed tools to upgrade.",
		))
		return nil, nil
	}

	upgradeIDs := make([]string, 0, len(toolsToUpgrade))
	for _, t := range toolsToUpgrade {
		upgradeIDs = append(upgradeIDs, t.Id)
	}
	// Mutually exclusive tool.id vs tool.ids — see toolIDUsageAttrs.
	usageAttrs := toolIDUsageAttrs(a.flags.dryRun, upgradeIDs)
	tracing.SetUsageAttributes(usageAttrs...)

	// --dry-run: display what would be upgraded without making
	// changes.
	if a.flags.dryRun {
		return a.dryRun(ctx, toolsToUpgrade)
	}

	if a.formatter.Kind() != output.JsonFormat {
		a.console.MessageUxItem(ctx, &ux.MessageTitle{
			Title:     "Upgrade Azure development tools (azd tool upgrade)",
			TitleNote: "Upgrades installed tools to their latest versions",
		})
	}

	agentOpts, agentErr := a.resolveAgentOptions(toolsToUpgrade)
	if agentErr != nil {
		return nil, agentErr
	}

	// In JSON mode, capture skill agent CLI output so it never leaks onto
	// stdout ahead of the structured result.
	if a.formatter.Kind() == output.JsonFormat {
		agentOpts = append(agentOpts, tool.WithQuiet())
	}

	start := time.Now()

	var (
		upgradeResults []*toolInstallResultItem
		rawResults     []*tool.InstallResult
		opErr          error
	)

	if useStepSpinner(a.console, a.formatter, toolsToUpgrade) {
		rawResults, opErr = runStepSpinner(
			ctx, a.console, toolsToUpgrade,
			func(ctx context.Context, ids []string, progress ...tool.InstallOption) ([]*tool.InstallResult, error) {
				return a.manager.UpgradeTools(ctx, ids, append(slices.Clone(agentOpts), progress...)...)
			},
		)
	} else {
		operationFn := func(ctx context.Context, allIDs []string) ([]*tool.InstallResult, error) {
			return a.manager.UpgradeTools(ctx, allIDs, agentOpts...)
		}
		outcome := runToolOperation(ctx, toolsToUpgrade, operationFn, "Upgrading", "upgrade", a.console,
			a.formatter.Kind() == output.JsonFormat)
		upgradeResults = outcome.Items
		rawResults = outcome.Results
		opErr = outcome.Err
	}
	emitToolInstallTelemetry(rawResults, time.Since(start), opErr, toolsToUpgrade)

	if len(rawResults) == 1 {
		r := rawResults[0]
		singleAttrs := singleResultCommonAttrs(r)
		if r.Tool != nil {
			if from, ok := fromVersions[r.Tool.Id]; ok && from != "" {
				singleAttrs = append(singleAttrs, fields.ToolUpgradeFromVersionKey.String(from))
			}
		}
		if r.Success && r.InstalledVersion != "" {
			singleAttrs = append(singleAttrs, fields.ToolUpgradeToVersionKey.String(r.InstalledVersion))
		}
		tracing.SetUsageAttributes(singleAttrs...)
	}

	if a.formatter.Kind() == output.JsonFormat {
		return nil, a.formatter.Format(upgradeResults, a.writer, nil)
	}

	// When one or more tools failed, surface the error so the command
	// exits non-zero and the success header is NOT printed. The per-tool
	// failures and a summary warning were already shown by
	// runToolOperation.
	if opErr != nil {
		return nil, opErr
	}

	// Choose the success header based on whether anything actually changed.
	// A tool is "already up to date" when the installer flagged it. For skills
	// this flag is authoritative (set per agent, so an upgrade on any agent
	// clears it), so we trust it as-is. For non-skill tools — which never set
	// the flag — fall back to comparing the version detected before the
	// upgrade (fromVersions) with the one detected after (InstalledVersion); a
	// missing version on either side counts as a change, so azd never claims
	// "up to date" without evidence.
	allUpToDate := len(rawResults) > 0
	for _, r := range rawResults {
		upToDate := r.AlreadyUpToDate
		if !upToDate && r.Tool != nil && r.Tool.Category != tool.ToolCategorySkill {
			before := fromVersions[r.Tool.Id]
			upToDate = before != "" && r.InstalledVersion != "" &&
				before == r.InstalledVersion
		}
		if !upToDate {
			allUpToDate = false
			break
		}
	}

	header := "Tool is upgraded."
	if allUpToDate {
		header = "Tool is already up to date."
	}
	if len(rawResults) > 1 {
		header = "Tools are upgraded."
		if allUpToDate {
			header = "Tools are already up to date."
		}
	}
	// For a single tool, include the resulting version in the done message,
	// e.g. "Tool is upgraded to v2.0.0." or
	// "Tool is already up to date (v1.1.75).".
	if len(rawResults) == 1 && rawResults[0].InstalledVersion != "" {
		version := rawResults[0].InstalledVersion
		if allUpToDate {
			header = fmt.Sprintf("Tool is already up to date (v%s).", version)
		} else {
			header = fmt.Sprintf("Tool is upgraded to v%s.", version)
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: header,
		},
	}, nil
}

// detectInstalledTools runs DetectAll behind a spinner and returns the full
// set of tool statuses. Used by the --all and interactive upgrade paths.
func (a *toolUpgradeAction) detectInstalledTools(ctx context.Context) ([]*tool.ToolStatus, error) {
	statuses, err := detectAllTools(ctx, a.manager, a.formatter, a.writer, "Detecting installed tools...")
	if err != nil {
		return nil, fmt.Errorf("detecting installed tools: %w", err)
	}
	return statuses, nil
}

// promptForUpgradeTools shows an interactive multi-select of the installed
// tools (all pre-selected) and returns the statuses the user chose to
// upgrade, mirroring the selection prompt in `azd tool install`.
func (a *toolUpgradeAction) promptForUpgradeTools(
	ctx context.Context,
	installed []*tool.ToolStatus,
) ([]*tool.ToolStatus, error) {
	choices := make([]*uxlib.MultiSelectChoice, len(installed))
	for i, s := range installed {
		choices[i] = &uxlib.MultiSelectChoice{
			Value:    s.Tool.Id,
			Label:    s.Tool.Name,
			Selected: true,
		}
	}

	multiSelect := uxlib.NewMultiSelect(&uxlib.MultiSelectOptions{
		Writer:  a.console.Handles().Stdout,
		Reader:  a.console.Handles().Stdin,
		Message: "Select tools to upgrade",
		Choices: choices,
	})

	selected, err := multiSelect.Ask(ctx)
	if err != nil {
		return nil, fmt.Errorf("selecting tools: %w", err)
	}

	byID := make(map[string]*tool.ToolStatus, len(installed))
	for _, s := range installed {
		byID[s.Tool.Id] = s
	}

	var chosen []*tool.ToolStatus
	for _, choice := range selected {
		if choice.Selected {
			if s, ok := byID[choice.Value]; ok {
				chosen = append(chosen, s)
			}
		}
	}
	return chosen, nil
}

// resolveAgentOptions determines which agent CLI(s) a skill should
// be upgraded for, based on the --agent flag. --agent all targets every
// detected agent; specific names target those agents. When --agent is
// omitted it returns no options, letting the installer upgrade every agent
// the skill is already installed through.
func (a *toolUpgradeAction) resolveAgentOptions(
	tools []*tool.ToolDefinition,
) ([]tool.InstallOption, error) {
	if len(a.flags.agents) == 0 {
		return nil, nil
	}

	skill := firstSkillTool(tools)
	if skill == nil {
		return nil, fmt.Errorf("--agent only applies to skill tools")
	}

	return resolveExplicitSkillAgents(a.flags.agents)
}

// dryRun detects the current status of the tools and displays what
// the upgrade command would do without making changes.
func (a *toolUpgradeAction) dryRun(
	ctx context.Context,
	tools []*tool.ToolDefinition,
) (*actions.ActionResult, error) {
	rows := make([]toolDryRunItem, 0, len(tools))

	for _, t := range tools {
		status, detectErr := a.manager.DetectTool(ctx, t.Id)
		if detectErr != nil {
			return nil, fmt.Errorf(
				"detecting %s: %w", t.Id, detectErr,
			)
		}

		action := "upgrade"
		currentVersion := ""
		if status.Installed {
			currentVersion = status.InstalledVersion
		} else {
			action = "skip (not installed)"
		}

		rows = append(rows, toolDryRunItem{
			Id:             t.Id,
			Name:           t.Name,
			CurrentVersion: currentVersion,
			Action:         action,
		})
	}

	if a.formatter.Kind() == output.JsonFormat {
		return nil, a.formatter.Format(rows, a.writer, nil)
	}

	if err := writeDryRunTable(a.writer, rows); err != nil {
		return nil, err
	}

	a.console.Message(ctx, "")
	a.console.Message(ctx, output.WithGrayFormat(
		"Dry run complete. No changes were made.",
	))

	return nil, nil
}

// ---------------------------------------------------------------------------
// azd tool uninstall [tool-name...]
// ---------------------------------------------------------------------------

type toolUninstallFlags struct {
	all    bool
	agents []string
	dryRun bool
}

func newToolUninstallFlags(cmd *cobra.Command) *toolUninstallFlags {
	flags := &toolUninstallFlags{}
	cmd.Flags().BoolVar(
		&flags.all, "all", false, "Uninstall all installed tools",
	)
	cmd.Flags().StringSliceVar(
		&flags.agents, "agent", nil,
		"Uninstall the skill from the specified agent(s): copilot, claude. "+
			"Use --agent all (or omit --agent) to remove the skill from every agent it is "+
			"installed through (skill tools only)",
	)
	cmd.Flags().BoolVar(
		&flags.dryRun, "dry-run", false,
		"Preview what would be uninstalled without making changes",
	)
	return flags
}

type toolUninstallAction struct {
	args      []string
	flags     *toolUninstallFlags
	manager   *tool.Manager
	console   input.Console
	formatter output.Formatter
	writer    io.Writer
}

func newToolUninstallAction(
	args []string,
	flags *toolUninstallFlags,
	manager *tool.Manager,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
) actions.Action {
	return &toolUninstallAction{
		args:      args,
		flags:     flags,
		manager:   manager,
		console:   console,
		formatter: formatter,
		writer:    writer,
	}
}

func (a *toolUninstallAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	ids, err := a.resolveToolIds(ctx)
	if err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		if a.formatter.Kind() == output.JsonFormat {
			// Keep a stable array schema for automation instead of a
			// consoleMessage object.
			return nil, a.formatter.Format([]*toolInstallResultItem{}, a.writer, nil)
		}
		a.console.Message(ctx, output.WithGrayFormat("No installed tools to uninstall."))
		return nil, nil
	}

	tools := make([]*tool.ToolDefinition, 0, len(ids))
	resolvedIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		toolDef, findErr := a.manager.FindTool(id)
		if findErr != nil {
			return nil, wrapToolNotFoundIfErr(findErr)
		}
		tools = append(tools, toolDef)
		resolvedIDs = append(resolvedIDs, toolDef.Id)
	}

	// Mutually exclusive tool.id vs tool.ids — see toolIDUsageAttrs.
	idAttrs := toolIDUsageAttrs(a.flags.dryRun, resolvedIDs)
	tracing.SetUsageAttributes(idAttrs...)

	// --dry-run: display what would be uninstalled without making changes.
	if a.flags.dryRun {
		return a.dryRun(ctx, tools)
	}

	if a.formatter.Kind() != output.JsonFormat {
		a.console.MessageUxItem(ctx, &ux.MessageTitle{
			Title:     "Uninstall Azure development tools (azd tool uninstall)",
			TitleNote: "Uninstalls specified tools from the local machine",
		})
	}

	agentOpts, agentErr := a.resolveAgentOptions(tools)
	if agentErr != nil {
		return nil, agentErr
	}

	// In JSON mode, capture skill agent CLI output so it never leaks onto
	// stdout ahead of the structured result.
	if a.formatter.Kind() == output.JsonFormat {
		agentOpts = append(agentOpts, tool.WithQuiet())
	}

	start := time.Now()

	var (
		uninstallResults []*toolInstallResultItem
		rawResults       []*tool.InstallResult
		opErr            error
	)

	if useStepSpinner(a.console, a.formatter, tools) {
		rawResults, opErr = runStepSpinner(
			ctx, a.console, tools,
			func(ctx context.Context, ids []string, progress ...tool.InstallOption) ([]*tool.InstallResult, error) {
				return a.manager.UninstallTools(ctx, ids, append(slices.Clone(agentOpts), progress...)...)
			},
		)
	} else {
		operationFn := func(ctx context.Context, allIDs []string) ([]*tool.InstallResult, error) {
			return a.manager.UninstallTools(ctx, allIDs, agentOpts...)
		}
		outcome := runToolOperation(ctx, tools, operationFn, "Uninstalling", "uninstall", a.console,
			a.formatter.Kind() == output.JsonFormat)
		uninstallResults = outcome.Items
		rawResults = outcome.Results
		opErr = outcome.Err
	}
	emitToolInstallTelemetry(rawResults, time.Since(start), opErr, tools)

	if len(rawResults) == 1 {
		tracing.SetUsageAttributes(singleResultCommonAttrs(rawResults[0])...)
	}

	if a.formatter.Kind() == output.JsonFormat {
		return nil, a.formatter.Format(uninstallResults, a.writer, nil)
	}

	// When one or more tools failed, surface the error so the command
	// exits non-zero and the success header is NOT printed. The per-tool
	// failures and a summary warning were already shown by
	// runToolOperation.
	if opErr != nil {
		return nil, opErr
	}

	header := "Your tool is uninstalled."
	if len(tools) > 1 {
		header = "Your tools are uninstalled."
	}
	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: header,
		},
	}, nil
}

// resolveAgentOptions determines which agent CLI(s) a skill should
// be uninstalled from, based on the --agent flag. --agent all targets every
// detected agent; specific names target those agents. When --agent is
// omitted it returns no options, letting the installer remove the skill
// from every agent it is installed through.
func (a *toolUninstallAction) resolveAgentOptions(
	tools []*tool.ToolDefinition,
) ([]tool.InstallOption, error) {
	if len(a.flags.agents) == 0 {
		return nil, nil
	}

	skill := firstSkillTool(tools)
	if skill == nil {
		return nil, fmt.Errorf("--agent only applies to skill tools")
	}

	return resolveExplicitSkillAgents(a.flags.agents)
}

// resolveToolIds determines which tool IDs to uninstall based on flags
// and arguments. Positional args win; --all (and --dry-run, which never
// mutates) select every installed tool; otherwise the interactive path
// lets the user pick from installed tools.
func (a *toolUninstallAction) resolveToolIds(ctx context.Context) ([]string, error) {
	if len(a.args) > 0 && a.flags.all {
		return nil, toolIDsWithAllError("uninstall")
	}

	// Positional args: uninstall specified tools by ID.
	if len(a.args) > 0 {
		return a.args, nil
	}

	// --all, --dry-run, and the interactive picker all need the current
	// installed set.
	statuses, err := detectAllTools(ctx, a.manager, a.formatter, a.writer, "Detecting installed tools...")
	if err != nil {
		return nil, fmt.Errorf("detecting installed tools: %w", err)
	}

	var installed []*tool.ToolStatus
	for _, s := range statuses {
		if s.Installed {
			installed = append(installed, s)
		}
	}

	if len(installed) == 0 {
		return nil, nil
	}

	// --all selects every installed tool. --dry-run does the same without
	// prompting: a preview never mutates anything, so it defaults to all
	// installed tools (a skill is previewed against the agents it is
	// installed through) instead of asking the user to pick.
	if a.flags.all || a.flags.dryRun {
		ids := make([]string, 0, len(installed))
		for _, s := range installed {
			ids = append(ids, s.Tool.Id)
		}
		return ids, nil
	}

	// Uninstall is destructive, so — unlike `azd tool install`/`upgrade`, which
	// only add — it must never treat "no target" as "all". When prompting is
	// unavailable (a non-interactive terminal, --no-prompt, or JSON output) and
	// the user gave neither tool IDs nor --all, fail with explicit guidance
	// instead of silently removing every installed tool.
	if !promptAllowed(a.console, a.formatter) {
		return nil, noToolTargetError("uninstall")
	}

	// Interactive: let the user pick from installed tools. Nothing is
	// pre-selected so uninstall is always an explicit choice.
	choices := make([]*uxlib.MultiSelectChoice, len(installed))
	for i, s := range installed {
		choices[i] = &uxlib.MultiSelectChoice{
			Value: s.Tool.Id,
			Label: s.Tool.Name,
		}
	}

	multiSelect := uxlib.NewMultiSelect(&uxlib.MultiSelectOptions{
		Writer:  a.console.Handles().Stdout,
		Reader:  a.console.Handles().Stdin,
		Message: "Select tools to uninstall",
		Choices: choices,
	})

	selected, err := multiSelect.Ask(ctx)
	if err != nil {
		return nil, fmt.Errorf("selecting tools: %w", err)
	}

	var ids []string
	for _, choice := range selected {
		if choice.Selected {
			ids = append(ids, choice.Value)
		}
	}
	return ids, nil
}

// dryRun detects the current status of the tools and displays what the
// uninstall command would do without making changes.
func (a *toolUninstallAction) dryRun(
	ctx context.Context,
	tools []*tool.ToolDefinition,
) (*actions.ActionResult, error) {
	rows := make([]toolDryRunItem, 0, len(tools))

	for _, t := range tools {
		status, detectErr := a.manager.DetectTool(ctx, t.Id)
		if detectErr != nil {
			return nil, fmt.Errorf("detecting %s: %w", t.Id, detectErr)
		}

		action := "uninstall"
		currentVersion := ""
		if status.Installed {
			currentVersion = status.InstalledVersion
		} else {
			action = "skip (not installed)"
		}

		rows = append(rows, toolDryRunItem{
			Id:             t.Id,
			Name:           t.Name,
			CurrentVersion: currentVersion,
			Action:         action,
		})
	}

	if a.formatter.Kind() == output.JsonFormat {
		return nil, a.formatter.Format(rows, a.writer, nil)
	}

	if err := writeDryRunTable(a.writer, rows); err != nil {
		return nil, err
	}

	a.console.Message(ctx, "")
	a.console.Message(ctx, output.WithGrayFormat(
		"Dry run complete. No changes were made.",
	))

	return nil, nil
}

// ---------------------------------------------------------------------------
// azd tool check
// ---------------------------------------------------------------------------

type toolCheckItem struct {
	Id   string `json:"id"`
	Name string `json:"name"`
	// Agent is the agent CLI a skill row is checked through (e.g.
	// "copilot"), empty for non-skill tools.
	Agent            string `json:"agent,omitempty"`
	InstalledVersion string `json:"installedVersion"`
	LatestVersion    string `json:"latestVersion"`
	UpdateAvailable  bool   `json:"updateAvailable"`
	// Status is a human-readable installation/upgrade status indicator.
	// Populated only for pretty-table rendering; omitted from JSON.
	Status string `json:"-"`
	// DisplayName is the NAME cell shown in the table: a skill row is
	// prefixed with its agent label (e.g. "[Copilot] Azure Skills"), other
	// rows use the plain name. Excluded from JSON, which carries Name + Agent.
	DisplayName string `json:"-"`
}

type toolCheckAction struct {
	manager   *tool.Manager
	console   input.Console
	formatter output.Formatter
	writer    io.Writer
}

func newToolCheckAction(
	manager *tool.Manager,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
) actions.Action {
	return &toolCheckAction{
		manager:   manager,
		console:   console,
		formatter: formatter,
		writer:    writer,
	}
}

func (a *toolCheckAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	var results []*tool.UpdateCheckResult
	if a.formatter.Kind() != output.JsonFormat {
		spinner := uxlib.NewSpinner(&uxlib.SpinnerOptions{
			Text:        "Checking for upgrades...",
			ClearOnStop: true,
		})
		if err := spinner.Run(ctx, func(ctx context.Context) error {
			var detectErr error
			results, detectErr = a.manager.CheckForUpdates(ctx)
			return detectErr
		}); err != nil {
			return nil, fmt.Errorf("checking for upgrades: %w", err)
		}
	} else {
		var err error
		results, err = a.manager.CheckForUpdates(ctx)
		if err != nil {
			return nil, fmt.Errorf("checking for upgrades: %w", err)
		}
	}

	rows := make([]toolCheckItem, 0, len(results))
	updatesAvailable := 0
	for _, r := range results {
		if r.UpdateAvailable {
			updatesAvailable++
		}
		// A skill installed on one or more agents expands into one row per
		// agent, each prefixed with the agent label and carrying that agent's
		// installed version and upgrade status.
		if r.Tool.Category == tool.ToolCategorySkill && len(r.SkillAgents) > 0 {
			for _, h := range r.SkillAgents {
				rows = append(rows, toolCheckItem{
					Id:    r.Tool.Id,
					Name:  r.Tool.Name,
					Agent: h.Agent,
					DisplayName: fmt.Sprintf("[%s] %s",
						skillAgentDisplayName(r.Tool, h.Agent), r.Tool.Name),
					InstalledVersion: h.CurrentVersion,
					LatestVersion:    r.LatestVersion,
					UpdateAvailable:  h.UpdateAvailable,
					Status:           toolCheckStatus(h.CurrentVersion != "", h.UpdateAvailable),
				})
			}
			continue
		}
		rows = append(rows, toolCheckItem{
			Id:               r.Tool.Id,
			Name:             r.Tool.Name,
			DisplayName:      r.Tool.Name,
			InstalledVersion: r.CurrentVersion,
			LatestVersion:    r.LatestVersion,
			UpdateAvailable:  r.UpdateAvailable,
			Status:           toolCheckStatus(r.CurrentVersion != "", r.UpdateAvailable),
		})
	}
	tracing.SetUsageAttributes(
		fields.ToolCheckUpdatesAvailableKey.Int(updatesAvailable),
	)

	if len(rows) == 0 {
		a.console.Message(ctx, output.WithGrayFormat("No tools found."))
		return nil, nil
	}

	var formatErr error

	if a.formatter.Kind() == output.TableFormat {
		prettyFormatter := &output.PrettyTableFormatter{}
		columns := []output.PrettyColumn{
			{
				Column:   output.Column{Heading: "ID", ValueTemplate: "{{.Id}}"},
				Priority: 1,
			},
			{
				Column:      output.Column{Heading: "NAME", ValueTemplate: "{{.DisplayName}}"},
				Priority:    2,
				CardTitle:   true,
				Wrappable:   true,
				Truncatable: true,
				ColorFunc:   colorAgentPrefix,
			},
			{
				Column:      output.Column{Heading: "STATUS", ValueTemplate: "{{.Status}}"},
				Priority:    1,
				Truncatable: true,
				ColorFunc:   extensionStatusColor,
			},
			{
				Column: output.Column{
					Heading:       "INSTALLED",
					ValueTemplate: `{{if .InstalledVersion}}{{.InstalledVersion}}{{else}}-{{end}}`,
				},
				CardValueTemplate: `{{if .InstalledVersion}}{{.InstalledVersion}}{{end}}`,
				Priority:          1,
			},
			{
				Column: output.Column{
					Heading:       "LATEST",
					ValueTemplate: "{{.LatestVersion}}",
				},
				CardValueTemplate: `{{if or .UpdateAvailable (not .InstalledVersion)}}{{.LatestVersion}}{{end}}`,
				Priority:          3,
				Truncatable:       true,
			},
		}

		formatErr = prettyFormatter.Format(
			rows, a.writer, output.PrettyTableFormatterOptions{
				Columns:              columns,
				ResponsiveColumnHint: true,
			},
		)

		if formatErr == nil {
			hasUpdates := false
			for _, r := range rows {
				if r.UpdateAvailable {
					hasUpdates = true
					break
				}
			}

			if hasUpdates {
				a.console.Message(ctx, "")
				a.console.Message(ctx, fmt.Sprintf(
					"To upgrade: %s",
					output.WithHighLightFormat("azd tool upgrade <tool-id>"),
				))
				a.console.Message(ctx, fmt.Sprintf(
					"To upgrade all: %s",
					output.WithHighLightFormat("azd tool upgrade --all"),
				))
			}
		}
	} else {
		formatErr = a.formatter.Format(rows, a.writer, nil)
	}

	return nil, formatErr
}

// ---------------------------------------------------------------------------
// azd tool show <tool-name>
// ---------------------------------------------------------------------------

type toolShowAction struct {
	args      []string
	console   input.Console
	manager   *tool.Manager
	formatter output.Formatter
	writer    io.Writer
}

func newToolShowAction(
	args []string,
	manager *tool.Manager,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
) actions.Action {
	return &toolShowAction{
		args:      args,
		manager:   manager,
		console:   console,
		formatter: formatter,
		writer:    writer,
	}
}

func (a *toolShowAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if len(a.args) == 0 {
		return nil, &internal.ErrorWithSuggestion{
			Err:        internal.ErrNoArgsProvided,
			Suggestion: "Run 'azd tool show <tool-name>' specifying the tool ID.",
		}
	}

	if len(a.args) > 1 {
		return nil, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"cannot specify multiple tools: %w",
				internal.ErrInvalidFlagCombination,
			),
			Suggestion: "Specify a single tool ID.",
		}
	}

	toolID := a.args[0]

	toolDef, err := a.manager.FindTool(toolID)
	if err != nil {
		return nil, wrapToolNotFoundIfErr(fmt.Errorf("finding tool: %w", err))
	}

	// Emit tool.id only after FindTool succeeds
	tracing.SetUsageAttributes(fields.ToolIdKey.String(toolDef.Id))

	var status *tool.ToolStatus
	if a.formatter.Kind() != output.JsonFormat {
		spinner := uxlib.NewSpinner(&uxlib.SpinnerOptions{
			Text:        fmt.Sprintf("Checking %s...", toolDef.Name),
			ClearOnStop: true,
		})
		if err := spinner.Run(ctx, func(ctx context.Context) error {
			var detectErr error
			status, detectErr = a.manager.DetectTool(ctx, toolID)
			return detectErr
		}); err != nil {
			return nil, fmt.Errorf("detecting tool: %w", err)
		}
	} else {
		var err error
		status, err = a.manager.DetectTool(ctx, toolID)
		if err != nil {
			return nil, fmt.Errorf("detecting tool: %w", err)
		}
	}

	// JSON output: return structured data.
	if a.formatter.Kind() == output.JsonFormat {
		item := toolShowItem{
			Id:          toolDef.Id,
			Name:        toolDef.Name,
			Description: toolDef.Description,
			Category:    string(toolDef.Category),
			Priority:    string(toolDef.Priority),
			Website:     toolDef.Website,
			Installed:   status.Installed,
			Version:     status.InstalledVersion,
		}
		return nil, a.formatter.Format(item, a.writer, nil)
	}

	if displayErr := a.displayToolDetails(toolDef, status); displayErr != nil {
		return nil, displayErr
	}

	return nil, nil
}

// displayToolDetails renders a formatted tool information view to the writer.
func (a *toolShowAction) displayToolDetails(
	toolDef *tool.ToolDefinition,
	status *tool.ToolStatus,
) error {
	writeSection := func(header string, rows [][]string) error {
		if len(rows) == 0 {
			return nil
		}

		underlinedHeader := output.WithUnderline("%s", header)
		boldHeader := output.WithBold("%s", underlinedHeader)
		if _, err := fmt.Fprintf(a.writer, "%s\n", boldHeader); err != nil {
			return err
		}

		tabs := tabwriter.NewWriter(
			a.writer,
			0,
			output.TableTabSize,
			1,
			output.TablePadCharacter,
			output.TableFlags,
		)

		for _, row := range rows {
			if _, err := tabs.Write([]byte(strings.Join(row, "\t") + "\n")); err != nil {
				return err
			}
		}

		if err := tabs.Flush(); err != nil {
			return err
		}

		_, err := fmt.Fprintln(a.writer)
		return err
	}

	// Tool Information
	installedVersion := "Not installed"
	if status.Installed {
		installedVersion = status.InstalledVersion
		if installedVersion == "" {
			installedVersion = "unknown"
		}
	}

	toolInfo := [][]string{
		{"Id", ":", toolDef.Id},
		{"Name", ":", toolDef.Name},
		{"Description", ":", toolDef.Description},
		{"Category", ":", string(toolDef.Category)},
		{"Priority", ":", string(toolDef.Priority)},
	}
	if toolDef.Website != "" {
		toolInfo = append(toolInfo, []string{"Website", ":", toolDef.Website})
	}
	toolInfo = append(toolInfo, []string{"Installed Version", ":", installedVersion})

	if err := writeSection("Tool Information", toolInfo); err != nil {
		return err
	}

	// Install Strategies
	if len(toolDef.InstallStrategies) > 0 {
		var strategyRows [][]string
		for _, platform := range slices.Sorted(maps.Keys(toolDef.InstallStrategies)) {
			for _, strategy := range toolDef.InstallStrategies[platform] {
				label := strategy.PackageManager
				if label == "" {
					label = "command"
				}
				detail := strategy.PackageId
				if detail == "" {
					detail = strategy.InstallCommand
				}
				strategyRows = append(strategyRows, []string{
					platform, ":", fmt.Sprintf("%s (%s)", label, detail),
				})
			}
		}
		if err := writeSection("Install Strategies", strategyRows); err != nil {
			return err
		}
	}

	// Dependencies
	if len(toolDef.Dependencies) > 0 {
		var depRows [][]string
		for _, dep := range toolDef.Dependencies {
			depRows = append(depRows, []string{"-", dep})
		}
		if err := writeSection("Dependencies", depRows); err != nil {
			return err
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// toolDryRunItem represents a single row in the dry-run output table for
// install and upgrade commands.
type toolDryRunItem struct {
	Id             string `json:"id"`
	Name           string `json:"name"`
	CurrentVersion string `json:"currentVersion"`
	Action         string `json:"action"`
}

// toolInstallResultItem represents the JSON output for a single install or
// upgrade result.
type toolInstallResultItem struct {
	Id               string `json:"id"`
	Name             string `json:"name"`
	Action           string `json:"action"`
	Success          bool   `json:"success"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	Error            string `json:"error,omitempty"`
}

// toolShowItem is the structured JSON representation returned by
// "azd tool show --output json".
type toolShowItem struct {
	Id          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Priority    string `json:"priority"`
	Website     string `json:"website"`
	Installed   bool   `json:"installed"`
	Version     string `json:"version"`
}

func wrapToolNotFoundIfErr(err error) error {
	if err == nil {
		return nil
	}
	return &internal.ErrorWithSuggestion{
		Err: err,
		Suggestion: "Use the tool ID as the argument. " +
			"Run 'azd tool list' to see available tool IDs.",
	}
}

// toolIDUsageAttrs returns the usage attributes for a tool operation. tool.id
// (single target) and tool.ids (sorted, batch) are mutually exclusive per
// tracing-in-azd.md ("Single-target" vs "Batch"); emitting both would
// double-count single-tool operations in any query against tool.ids. tool.ids
// is sorted so the value is a canonical set rather than a permutation.
// tool.dry_run is always emitted alongside so dry_run never appears without a
// matching tool.id/tool.ids.
func toolIDUsageAttrs(dryRun bool, ids []string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{fields.ToolDryRunKey.Bool(dryRun)}
	if len(ids) == 1 {
		attrs = append(attrs, fields.ToolIdKey.String(ids[0]))
	} else {
		sorted := slices.Clone(ids)
		slices.Sort(sorted)
		attrs = append(attrs, fields.ToolIdsKey.String(strings.Join(sorted, ",")))
	}
	return attrs
}

// toolOperationFn abstracts InstallTools and UpgradeTools so that
// runToolOperation can handle both operations uniformly.
type toolOperationFn func(ctx context.Context, ids []string) ([]*tool.InstallResult, error)

// toolOpOutcome is the named result of runToolOperation. Items is the
// formatted, user-visible result list; Results is the raw installer output
// that telemetry / version-emission paths consume; Err is the TaskList's
// aggregate error (non-nil when any task reported failure).
type toolOpOutcome struct {
	Items   []*toolInstallResultItem
	Results []*tool.InstallResult
	Err     error
}

// runToolOperation executes a batch tool operation (install or upgrade) with a
// single call to operationFn, then maps the results to per-tool TaskList entries
// for user-visible progress. This avoids the N+1 problem of calling the
// operation once per tool (which triggers redundant dependency resolution).
//
// Parameters:
//   - tools: the resolved ToolDefinition slice to operate on
//   - operationFn: either InstallTools or UpgradeTools
//   - title: verb for task titles (e.g. "Installing", "Upgrading")
//   - action: action label for result items (e.g. "install", "upgrade")
//   - console: for displaying warnings on partial failure
//   - quiet: when true (JSON output) the per-tool TaskList is routed to
//     io.Discard so its progress/control bytes never corrupt the
//     machine-readable stream; the results are still collected for the caller.
func runToolOperation(
	ctx context.Context,
	tools []*tool.ToolDefinition,
	operationFn toolOperationFn,
	title string,
	action string,
	console input.Console,
	quiet bool,
) toolOpOutcome {
	// Collect all IDs and run the operation once.
	ids := make([]string, len(tools))
	for i, t := range tools {
		ids[i] = t.Id
	}

	results, opErr := operationFn(ctx, ids)

	// Index results by tool ID for O(1) lookup.
	resultsByID := make(map[string]*tool.InstallResult, len(results))
	for _, r := range results {
		if r.Tool != nil {
			resultsByID[r.Tool.Id] = r
		}
	}

	// Build per-tool result items and a TaskList for display.
	resultItems := make([]*toolInstallResultItem, 0, len(tools))

	// Identify IDs of the originally requested tools for dependency detection.
	requestedIDs := make(map[string]bool, len(tools))
	for _, t := range tools {
		requestedIDs[t.Id] = true
	}

	taskListOptions := &uxlib.TaskListOptions{ContinueOnError: true}
	if quiet {
		// JSON output: suppress the TaskList UI so it cannot write progress or
		// control bytes to stdout ahead of the JSON payload.
		taskListOptions.Writer = io.Discard
	}
	taskList := uxlib.NewTaskList(taskListOptions)

	for _, t := range tools {
		capturedTool := t
		r := resultsByID[capturedTool.Id]

		taskList.AddTask(uxlib.TaskOptions{
			Title: fmt.Sprintf("%s %s", title, capturedTool.Name),
			Action: func(setProgress uxlib.SetProgressFunc) (uxlib.TaskState, error) {
				// If the batch call itself failed, every tool is an error.
				if opErr != nil {
					resultItems = append(resultItems, &toolInstallResultItem{
						Id:      capturedTool.Id,
						Name:    capturedTool.Name,
						Action:  action,
						Success: false,
						Error:   opErr.Error(),
					})
					return uxlib.Error, opErr
				}

				if r == nil {
					resultItems = append(resultItems, &toolInstallResultItem{
						Id:      capturedTool.Id,
						Name:    capturedTool.Name,
						Action:  action,
						Success: false,
						Error:   "no result returned",
					})
					return uxlib.Error, fmt.Errorf("no result returned for %s", capturedTool.Id)
				}

				if r.Error != nil {
					resultItems = append(resultItems, &toolInstallResultItem{
						Id:      capturedTool.Id,
						Name:    capturedTool.Name,
						Action:  action,
						Success: false,
						Error:   r.Error.Error(),
					})
					return uxlib.Error, r.Error
				}

				if !r.Success {
					resultItems = append(resultItems, &toolInstallResultItem{
						Id:      capturedTool.Id,
						Name:    capturedTool.Name,
						Action:  action,
						Success: false,
					})
					return uxlib.Error, fmt.Errorf(
						"%s did not succeed", action,
					)
				}

				if r.InstalledVersion != "" {
					setProgress(r.InstalledVersion)
				}

				resultItems = append(resultItems, &toolInstallResultItem{
					Id:               capturedTool.Id,
					Name:             capturedTool.Name,
					Action:           action,
					Success:          true,
					InstalledVersion: r.InstalledVersion,
				})
				return uxlib.Success, nil
			},
		})
	}

	// Add dependency results (tools returned by the batch operation but not
	// explicitly requested by the user).
	for _, r := range results {
		if r.Tool == nil || requestedIDs[r.Tool.Id] {
			continue
		}
		depResult := r
		taskList.AddTask(uxlib.TaskOptions{
			Title: fmt.Sprintf("%s %s (dependency)", title, depResult.Tool.Name),
			Action: func(setProgress uxlib.SetProgressFunc) (uxlib.TaskState, error) {
				if depResult.Error != nil {
					resultItems = append(resultItems, &toolInstallResultItem{
						Id:      depResult.Tool.Id,
						Name:    depResult.Tool.Name + " (dependency)",
						Action:  action,
						Success: false,
						Error:   depResult.Error.Error(),
					})
					return uxlib.Error, depResult.Error
				}

				if depResult.InstalledVersion != "" {
					setProgress(depResult.InstalledVersion)
				}

				resultItems = append(resultItems, &toolInstallResultItem{
					Id:               depResult.Tool.Id,
					Name:             depResult.Tool.Name + " (dependency)",
					Action:           action,
					Success:          depResult.Success,
					InstalledVersion: depResult.InstalledVersion,
				})

				if !depResult.Success {
					return uxlib.Error, fmt.Errorf("%s did not succeed", action)
				}
				return uxlib.Success, nil
			},
		})
	}

	taskErr := taskList.Run()
	if taskErr != nil && !quiet {
		// Build the past participle: "install" -> "installed",
		// "upgrade" -> "upgraded". Appending only "d" would be wrong,
		// so append "ed" unless the verb already ends in "e".
		participle := action + "ed"
		if strings.HasSuffix(action, "e") {
			participle = action + "d"
		}
		// Skipped in quiet (JSON) mode: AskerConsole.Message would emit a
		// standalone consoleMessage object ahead of the result array, breaking
		// single-document JSON output.
		console.Message(ctx, output.WithWarningFormat(
			"\nSome tools could not be %s. Run 'azd tool list' for details.", participle,
		))
	}

	return toolOpOutcome{Items: resultItems, Results: results, Err: taskErr}
}

// writeDryRunTable renders a dry-run results table using tabwriter.
func writeDryRunTable(w io.Writer, rows []toolDryRunItem) error {
	tw := tabwriter.NewWriter(
		w,
		0,
		output.TableTabSize,
		1,
		output.TablePadCharacter,
		output.TableFlags,
	)

	header := fmt.Sprintf(
		"%s\t%s\t%s\t%s\n",
		"Id", "Name", "Current Version", "Action",
	)
	if _, err := tw.Write([]byte(header)); err != nil {
		return err
	}

	for _, r := range rows {
		line := fmt.Sprintf(
			"%s\t%s\t%s\t%s\n",
			r.Id, r.Name, r.CurrentVersion, r.Action,
		)
		if _, err := tw.Write([]byte(line)); err != nil {
			return err
		}
	}

	return tw.Flush()
}

// toolStatusColor applies color formatting based on install status text.
func toolStatusColor(s string) string {
	switch s {
	case "Installed":
		return output.WithSuccessFormat(s)
	default:
		// "Not installed" and any other state render in gray.
		return output.WithGrayFormat(s)
	}
}

// toolCheckStatus returns a human-readable status string for the tool check
// table.
func toolCheckStatus(installed, updateAvailable bool) string {
	switch {
	case !installed:
		return statusNotInstall
	case updateAvailable:
		return statusUpgrade
	default:
		return statusUpToDate
	}
}

// skillAgentDisplayName maps an installed skill agent's command identity (e.g.
// "copilot") to the agent's display name from the tool's manifest (e.g.
// "GitHub Copilot CLI"), used to prefix skill rows in the list/check tables.
// It falls back to the command when no configured agent matches.
func skillAgentDisplayName(t *tool.ToolDefinition, command string) string {
	for _, agent := range t.SkillAgents {
		if agent.Command == command {
			return agent.DisplayName
		}
	}
	return command
}

// colorAgentPrefix colors a leading "[agent]" label (as prepended to skill
// names for the list/check tables) so the agent stands out, leaving the rest
// of the name — and any name without a bracket label — unchanged. It is the
// NAME column's ColorFunc: the pretty table applies it per rendered line after
// layout, so the cell value itself stays plain and the table can wrap and
// align it correctly at narrow terminal widths (embedding ANSI in the value
// would suppress wrapping and break the alignment of later columns).
//
// Because it runs per line, it must also color a label the table wrapped
// across lines (e.g. "[GitHub Copilot" then "CLI] Azure Skills"): the opening
// line ("[" with no "]") is colored whole, and a continuation line carrying
// the label tail is colored up to and including its "]".
func colorAgentPrefix(s string) string {
	switch {
	case strings.HasPrefix(s, "["):
		// Whole label on this line ("[..]"), or the first line of a label the
		// table wrapped (no "]" yet, so it continues on the next line).
		if end := strings.IndexByte(s, ']'); end >= 0 {
			return output.WithWarningFormat(s[:end+1]) + s[end+1:]
		}
		return output.WithWarningFormat(s)
	case !strings.ContainsRune(s, '[') && strings.ContainsRune(s, ']'):
		// Continuation line carrying a wrapped label's tail: color up to and
		// including "]". Guarded by "no '[' on this line" so plain tool names
		// (which carry no brackets at all) are never touched.
		end := strings.IndexByte(s, ']')
		return output.WithWarningFormat(s[:end+1]) + s[end+1:]
	default:
		return s
	}
}
