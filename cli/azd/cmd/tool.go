// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
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
			Short: "Check for tool updates.",
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

	_ = runToolOperation(ctx, tools, operationFn, "Installing", "install", a.console)
	// runToolOperation already displayed warnings; we intentionally
	// discard the outcome here — child tasks have surfaced what the user
	// needs to see, and this caller does not propagate the task error.

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Tool installation complete",
		},
	}, nil
}

// ---------------------------------------------------------------------------
// azd tool list
// ---------------------------------------------------------------------------

type toolListItem struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Priority string `json:"priority"`
	Status   string `json:"status"`
	Version  string `json:"version"`
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
		status := "Not installed"
		version := ""
		if s.Installed {
			status = "Installed"
			version = s.InstalledVersion
		}

		rows = append(rows, toolListItem{
			Id:       s.Tool.Id,
			Name:     s.Tool.Name,
			Category: string(s.Tool.Category),
			Priority: string(s.Tool.Priority),
			Status:   status,
			Version:  version,
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
				Column:      output.Column{Heading: "NAME", ValueTemplate: "{{.Name}}"},
				Priority:    2,
				CardTitle:   true,
				Wrappable:   true,
				Truncatable: true,
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
			{
				Column:      output.Column{Heading: "PRIORITY", ValueTemplate: "{{.Priority}}"},
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
	hosts  []string
	dryRun bool
}

func newToolInstallFlags(cmd *cobra.Command) *toolInstallFlags {
	flags := &toolInstallFlags{}
	cmd.Flags().BoolVar(
		&flags.all, "all", false, "Install all recommended tools",
	)
	cmd.Flags().StringSliceVar(
		&flags.hosts, "host", nil,
		"Install the skill for the specified agent host(s): copilot, claude. "+
			"Use --host all for every detected host (skill tools only)",
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
		a.console.Message(ctx, output.WithSuccessFormat("Nothing to install."))
		return nil, nil
	}

	// --dry-run: detect tool status and display what would happen
	// without actually installing anything.
	if a.flags.dryRun {
		return a.dryRun(ctx, ids)
	}

	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Install Azure development tools (azd tool install)",
		TitleNote: "Installs specified tools onto the local machine",
	})

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

	// Emit tool.id for single-target installs and tool.ids for multi-target
	// installs — never both. The two attributes are documented as mutually
	// exclusive (tracing-in-azd.md: "Single-target" vs "Batch"), so co-emit
	// would double-count single-tool installs in any downstream query
	// against tool.ids. Sort before joining for batch so the attribute
	// value is a canonical set rather than a permutation.

	idAttrs := []attribute.KeyValue{
		fields.ToolDryRunKey.Bool(a.flags.dryRun),
	}
	if len(resolvedIDs) == 1 {
		idAttrs = append(idAttrs, fields.ToolIdKey.String(resolvedIDs[0]))
	} else {
		sortedIDs := slices.Clone(resolvedIDs)
		slices.Sort(sortedIDs)
		idAttrs = append(idAttrs, fields.ToolIdsKey.String(strings.Join(sortedIDs, ",")))
	}
	tracing.SetUsageAttributes(idAttrs...)

	// Resolve which agent host(s) to install skills for, based on the
	// --host flag. When no host is given and several are detected, the
	// user is asked to choose explicitly.
	hostOpts, hostErr := a.resolveHostOptions(ctx, tools)
	if hostErr != nil {
		return nil, hostErr
	}

	operationFn := func(ctx context.Context, allIDs []string) ([]*tool.InstallResult, error) {
		return a.manager.InstallTools(ctx, allIDs, hostOpts...)
	}

	start := time.Now()
	outcome := runToolOperation(ctx, tools, operationFn, "Installing", "install", a.console)
	installResults := outcome.Items
	rawResults := outcome.Results
	opErr := outcome.Err
	emitToolInstallTelemetry(rawResults, time.Since(start), opErr, tools)

	if len(rawResults) == 1 {
		tracing.SetUsageAttributes(singleResultCommonAttrs(rawResults[0])...)
	}

	if a.formatter.Kind() == output.JsonFormat {
		return nil, a.formatter.Format(installResults, a.writer, nil)
	}

	// When one or more tools failed, surface the error so the command
	// exits non-zero and the success header is NOT printed. The per-tool
	// failures and a summary warning were already shown by
	// runToolOperation.
	if opErr != nil {
		return nil, opErr
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Tool installation complete",
		},
	}, nil
}

// allHostsKeyword is the reserved --host value that selects every
// detected agent host.
const allHostsKeyword = "all"

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

// resolveExplicitSkillHosts maps an explicit --host flag value to install
// options. The reserved value "all" installs through every available
// host (resolved at install time); otherwise the named hosts are passed
// through for the installer to validate. Shared by the install and
// upgrade actions.
func resolveExplicitSkillHosts(hosts []string) ([]tool.InstallOption, error) {
	// --host all selects every detected host. It cannot be mixed with
	// specific host names.
	if slices.Contains(hosts, allHostsKeyword) {
		if len(hosts) > 1 {
			return nil, fmt.Errorf(
				"--host all cannot be combined with specific hosts",
			)
		}
		return []tool.InstallOption{tool.WithAllAvailableHosts()}, nil
	}
	// The installer validates that each named host is configured and on
	// PATH, surfacing a descriptive error otherwise.
	return []tool.InstallOption{tool.WithHosts(hosts...)}, nil
}

// resolveHostOptions determines which agentic CLI host(s) a skill should
// be installed for. With --host it targets the named host(s); --host all
// targets every detected host. Without --host, a skill pulled in by a
// batch (--all or the interactive picker) installs through every
// available host, while an explicitly-named skill with several detected
// hosts returns guidance asking the user to choose. It returns the
// install options to pass to the installer (nil selects the single
// preferred host).
//
// When an explicitly-named skill has several hosts on PATH, an
// interactive terminal is prompted to choose which host(s) to install
// for (we still print a --host hint); in non-interactive mode it falls
// back to a guidance error telling the user to re-run with --host.
func (a *toolInstallAction) resolveHostOptions(
	ctx context.Context,
	tools []*tool.ToolDefinition,
) ([]tool.InstallOption, error) {
	explicit := len(a.flags.hosts) > 0
	skill := firstSkillTool(tools)

	if explicit && skill == nil {
		return nil, fmt.Errorf("--host only applies to skill tools")
	}
	if skill == nil {
		return nil, nil
	}

	if explicit {
		// "all" expands to every detected host and is validated at
		// install time. Specific host names are checked here so an
		// unusable host (unknown name or not on PATH) can fall back to
		// an interactive picker instead of hard-failing.
		if !slices.Contains(a.flags.hosts, allHostsKeyword) {
			if opts, handled, err := a.resolveUnavailableHostPrompt(ctx, skill); handled || err != nil {
				return opts, err
			}
		}
		return resolveExplicitSkillHosts(a.flags.hosts)
	}

	// No --host. A skill the user did not name explicitly (batch --all or
	// interactive selection) installs through every available host,
	// resolved at install time so host CLIs installed earlier in the same
	// batch are picked up. This is also why --all does not abort when
	// several hosts are present.
	if !slices.Contains(a.args, skill.Id) {
		return []tool.InstallOption{tool.WithAllAvailableHosts()}, nil
	}

	// Explicitly-named skill: when multiple hosts are detected we cannot
	// safely guess which the user wants.
	present := a.manager.AvailableSkillHosts(skill)
	if len(present) > 1 {
		// Interactive terminal: prompt the user to pick the host(s),
		// after surfacing the --host hint so they learn the shortcut too.
		if a.console.IsSpinnerInteractive() && !a.console.IsNoPromptMode() {
			a.console.Message(ctx, fmt.Sprintf(
				"Multiple agent hosts detected. You can install "+
					"directly with `azd tool install %s --host <host>` "+
					"or `azd tool install %s --host all`.",
				skill.Id, skill.Id,
			))

			opts, err := a.promptForSkillHosts(ctx, skill, present)
			if err != nil {
				return nil, err
			}
			if opts != nil {
				return opts, nil
			}
			// Nothing selected — fall through to the guidance error.
		}

		return nil, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf("multiple agent hosts detected for %s", skill.Name),
			Message: fmt.Sprintf(
				"Detected multiple hosts: %s", strings.Join(present, ", "),
			),
			Suggestion: fmt.Sprintf(
				"Specify which host(s) to install for:\n\n"+
					"    azd tool install %s --host <host>\n\n"+
					"    azd tool install %s --host all",
				skill.Id, skill.Id,
			),
		}
	}

	// Zero or one host detected: keep the single preferred-host default.
	return nil, nil
}

// resolveUnavailableHostPrompt handles an explicit --host whose named
// host(s) are not usable (unknown name or not on PATH). In an
// interactive terminal it tells the user the requested host is
// unavailable and prompts them to pick from the hosts detected on PATH;
// the chosen host(s) are returned with handled=true. When no supported
// host is on PATH at all it defers to the installer's install guidance
// (handled=true via WithAllAvailableHosts). In non-interactive mode, or
// when every requested host is already available, it returns
// handled=false so the caller validates the request as usual.
func (a *toolInstallAction) resolveUnavailableHostPrompt(
	ctx context.Context,
	skill *tool.ToolDefinition,
) (opts []tool.InstallOption, handled bool, err error) {
	if !a.console.IsSpinnerInteractive() || a.console.IsNoPromptMode() {
		return nil, false, nil
	}

	available := a.manager.AvailableSkillHosts(skill)
	var unavailable []string
	for _, host := range a.flags.hosts {
		if !slices.Contains(available, host) {
			unavailable = append(unavailable, fmt.Sprintf("%q", host))
		}
	}
	if len(unavailable) == 0 {
		return nil, false, nil
	}

	// No usable host on PATH — defer to the installer's install guidance
	// (recommends installing a CLI host first) by targeting every
	// available host.
	if len(available) == 0 {
		return []tool.InstallOption{tool.WithAllAvailableHosts()}, true, nil
	}

	a.console.Message(ctx, fmt.Sprintf(
		"Host %s is not available for %s. Choose from the hosts detected "+
			"on your PATH:",
		strings.Join(unavailable, ", "), skill.Name,
	))
	picked, err := a.promptForSkillHosts(ctx, skill, available)
	if err != nil {
		return nil, false, err
	}
	// Nothing selected — let the caller surface the installer's
	// validation error for the originally requested host.
	if picked == nil {
		return nil, false, nil
	}
	return picked, true, nil
}

// promptForSkillHosts shows an interactive multi-select over the given
// available hosts and returns the matching install option, or (nil, nil)
// when the user selects nothing so callers can fall back to their own
// guidance.
func (a *toolInstallAction) promptForSkillHosts(
	ctx context.Context,
	skill *tool.ToolDefinition,
	available []string,
) ([]tool.InstallOption, error) {
	selected, err := a.console.MultiSelect(ctx, input.ConsoleOptions{
		Message: fmt.Sprintf(
			"Select the agent host(s) to install %s for", skill.Name,
		),
		Options:      available,
		DefaultValue: []string{available[0]},
	})
	if err != nil {
		return nil, fmt.Errorf("selecting hosts: %w", err)
	}
	if len(selected) == 0 {
		return nil, nil
	}
	return []tool.InstallOption{tool.WithHosts(selected...)}, nil
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

	// Emit tool.id vs tool.ids on the dry-run path with the same
	// mutual-exclusion discipline as the real install path. Values are
	// built from canonical toolDef.Id strings already validated by
	// FindTool. tool.dry_run is emitted alongside so the contract is
	// uniform: dry_run never appears without a matching tool.id/ids.
	idAttrs := []attribute.KeyValue{
		fields.ToolDryRunKey.Bool(true),
	}
	if len(resolvedIDs) == 1 {
		idAttrs = append(idAttrs, fields.ToolIdKey.String(resolvedIDs[0]))
	} else {
		sortedIDs := slices.Clone(resolvedIDs)
		slices.Sort(sortedIDs)
		idAttrs = append(idAttrs, fields.ToolIdsKey.String(strings.Join(sortedIDs, ",")))
	}
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

// resolveToolIds determines which tool IDs to install based on flags and arguments.
func (a *toolInstallAction) resolveToolIds(ctx context.Context) ([]string, error) {
	// --all: install all recommended tools that are not already installed.
	if a.flags.all {
		var statuses []*tool.ToolStatus
		spinner := uxlib.NewSpinner(&uxlib.SpinnerOptions{
			Text:        "Detecting tool status...",
			ClearOnStop: true,
		})
		if err := spinner.Run(ctx, func(ctx context.Context) error {
			var detectErr error
			statuses, detectErr = a.manager.DetectAll(ctx)
			return detectErr
		}); err != nil {
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
	var statuses []*tool.ToolStatus
	spinner := uxlib.NewSpinner(&uxlib.SpinnerOptions{
		Text:        "Detecting tool status...",
		ClearOnStop: true,
	})
	if err := spinner.Run(ctx, func(ctx context.Context) error {
		var detectErr error
		statuses, detectErr = a.manager.DetectAll(ctx)
		return detectErr
	}); err != nil {
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
	dryRun bool
	hosts  []string
}

func newToolUpgradeFlags(cmd *cobra.Command) *toolUpgradeFlags {
	flags := &toolUpgradeFlags{}
	cmd.Flags().BoolVar(
		&flags.dryRun, "dry-run", false,
		"Preview what would be upgraded without making changes",
	)
	cmd.Flags().StringSliceVar(
		&flags.hosts, "host", nil,
		"Upgrade the skill for the specified agent host(s): copilot, claude. "+
			"Use --host all for every detected host (skill tools only)",
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

	if len(a.args) > 0 {
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
	} else {
		var statuses []*tool.ToolStatus
		spinner := uxlib.NewSpinner(&uxlib.SpinnerOptions{
			Text:        "Detecting installed tools...",
			ClearOnStop: true,
		})
		if err := spinner.Run(ctx, func(ctx context.Context) error {
			var detectErr error
			statuses, detectErr = a.manager.DetectAll(ctx)
			return detectErr
		}); err != nil {
			return nil, fmt.Errorf("detecting installed tools: %w", err)
		}
		for _, s := range statuses {
			if s.Installed {
				toolsToUpgrade = append(toolsToUpgrade, s.Tool)
				if s.Tool != nil {
					fromVersions[s.Tool.Id] = s.InstalledVersion
				}
			}
		}
	}

	if len(toolsToUpgrade) == 0 {
		a.console.Message(ctx, output.WithGrayFormat(
			"No installed tools to upgrade.",
		))
		return nil, nil
	}

	upgradeIDs := make([]string, 0, len(toolsToUpgrade))
	for _, t := range toolsToUpgrade {
		upgradeIDs = append(upgradeIDs, t.Id)
	}
	// Mutually exclusive tool.id vs tool.ids — see the install action for
	// the same discipline and the rationale in tracing-in-azd.md.
	usageAttrs := []attribute.KeyValue{
		fields.ToolDryRunKey.Bool(a.flags.dryRun),
	}
	if len(upgradeIDs) == 1 {
		usageAttrs = append(usageAttrs, fields.ToolIdKey.String(upgradeIDs[0]))
	} else {
		sortedUpgradeIDs := slices.Clone(upgradeIDs)
		slices.Sort(sortedUpgradeIDs)
		usageAttrs = append(usageAttrs, fields.ToolIdsKey.String(strings.Join(sortedUpgradeIDs, ",")))
	}
	tracing.SetUsageAttributes(usageAttrs...)

	// --dry-run: display what would be upgraded without making
	// changes.
	if a.flags.dryRun {
		return a.dryRun(ctx, toolsToUpgrade)
	}

	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Upgrade Azure development tools (azd tool upgrade)",
		TitleNote: "Upgrades installed tools to their latest versions",
	})

	operationFn := func(ctx context.Context, allIDs []string) ([]*tool.InstallResult, error) {
		hostOpts, hostErr := a.resolveHostOptions(toolsToUpgrade)
		if hostErr != nil {
			return nil, hostErr
		}
		return a.manager.UpgradeTools(ctx, allIDs, hostOpts...)
	}

	start := time.Now()
	outcome := runToolOperation(ctx, toolsToUpgrade, operationFn, "Upgrading", "upgrade", a.console)
	upgradeResults := outcome.Items
	rawResults := outcome.Results
	opErr := outcome.Err
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

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Tool upgrade complete",
		},
	}, nil
}

// resolveHostOptions determines which agentic CLI host(s) a skill should
// be upgraded for, based on the --host flag. --host all targets every
// detected host; specific names target those hosts. When --host is
// omitted it returns no options, letting the installer upgrade every host
// the skill is already installed through.
func (a *toolUpgradeAction) resolveHostOptions(
	tools []*tool.ToolDefinition,
) ([]tool.InstallOption, error) {
	if len(a.flags.hosts) == 0 {
		return nil, nil
	}

	skill := firstSkillTool(tools)
	if skill == nil {
		return nil, fmt.Errorf("--host only applies to skill tools")
	}

	return resolveExplicitSkillHosts(a.flags.hosts)
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
	hosts  []string
	dryRun bool
}

func newToolUninstallFlags(cmd *cobra.Command) *toolUninstallFlags {
	flags := &toolUninstallFlags{}
	cmd.Flags().BoolVar(
		&flags.all, "all", false, "Uninstall all installed tools",
	)
	cmd.Flags().StringSliceVar(
		&flags.hosts, "host", nil,
		"Uninstall the skill from the specified agent host(s): copilot, claude. "+
			"Use --host all (or omit --host) to remove the skill from every host it is "+
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

	// Mutually exclusive tool.id vs tool.ids — see the install action for
	// the same discipline and the rationale in tracing-in-azd.md.
	idAttrs := []attribute.KeyValue{
		fields.ToolDryRunKey.Bool(a.flags.dryRun),
	}
	if len(resolvedIDs) == 1 {
		idAttrs = append(idAttrs, fields.ToolIdKey.String(resolvedIDs[0]))
	} else {
		sortedIDs := slices.Clone(resolvedIDs)
		slices.Sort(sortedIDs)
		idAttrs = append(idAttrs, fields.ToolIdsKey.String(strings.Join(sortedIDs, ",")))
	}
	tracing.SetUsageAttributes(idAttrs...)

	// --dry-run: display what would be uninstalled without making changes.
	if a.flags.dryRun {
		return a.dryRun(ctx, tools)
	}

	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Uninstall Azure development tools (azd tool uninstall)",
		TitleNote: "Uninstalls specified tools from the local machine",
	})

	operationFn := func(ctx context.Context, allIDs []string) ([]*tool.InstallResult, error) {
		hostOpts, hostErr := a.resolveHostOptions(tools)
		if hostErr != nil {
			return nil, hostErr
		}
		return a.manager.UninstallTools(ctx, allIDs, hostOpts...)
	}

	start := time.Now()
	outcome := runToolOperation(ctx, tools, operationFn, "Uninstalling", "uninstall", a.console)
	uninstallResults := outcome.Items
	rawResults := outcome.Results
	opErr := outcome.Err
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

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Tool uninstall complete",
		},
	}, nil
}

// resolveHostOptions determines which agentic CLI host(s) a skill should
// be uninstalled from, based on the --host flag. --host all targets every
// detected host; specific names target those hosts. When --host is
// omitted it returns no options, letting the installer remove the skill
// from every host it is installed through.
func (a *toolUninstallAction) resolveHostOptions(
	tools []*tool.ToolDefinition,
) ([]tool.InstallOption, error) {
	if len(a.flags.hosts) == 0 {
		return nil, nil
	}

	skill := firstSkillTool(tools)
	if skill == nil {
		return nil, fmt.Errorf("--host only applies to skill tools")
	}

	return resolveExplicitSkillHosts(a.flags.hosts)
}

// resolveToolIds determines which tool IDs to uninstall based on flags
// and arguments. Positional args win; --all (and --dry-run, which never
// mutates) select every installed tool; otherwise the interactive path
// lets the user pick from installed tools.
func (a *toolUninstallAction) resolveToolIds(ctx context.Context) ([]string, error) {
	// Positional args: uninstall specified tools by ID.
	if len(a.args) > 0 {
		return a.args, nil
	}

	// --all, --dry-run, and the interactive picker all need the current
	// installed set.
	var statuses []*tool.ToolStatus
	spinner := uxlib.NewSpinner(&uxlib.SpinnerOptions{
		Text:        "Detecting installed tools...",
		ClearOnStop: true,
	})
	if err := spinner.Run(ctx, func(ctx context.Context) error {
		var detectErr error
		statuses, detectErr = a.manager.DetectAll(ctx)
		return detectErr
	}); err != nil {
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
	// installed tools (a skill is previewed against the host(s) it is
	// installed through) instead of asking the user to pick.
	if a.flags.all || a.flags.dryRun {
		ids := make([]string, 0, len(installed))
		for _, s := range installed {
			ids = append(ids, s.Tool.Id)
		}
		return ids, nil
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
	Id               string `json:"id"`
	Name             string `json:"name"`
	InstalledVersion string `json:"installedVersion"`
	LatestVersion    string `json:"latestVersion"`
	UpdateAvailable  bool   `json:"updateAvailable"`
	// Status is a human-readable installation/update status indicator.
	// Populated only for pretty-table rendering; omitted from JSON.
	Status string `json:"-"`
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
			Text:        "Checking for updates...",
			ClearOnStop: true,
		})
		if err := spinner.Run(ctx, func(ctx context.Context) error {
			var detectErr error
			results, detectErr = a.manager.CheckForUpdates(ctx)
			return detectErr
		}); err != nil {
			return nil, fmt.Errorf("checking for updates: %w", err)
		}
	} else {
		var err error
		results, err = a.manager.CheckForUpdates(ctx)
		if err != nil {
			return nil, fmt.Errorf("checking for updates: %w", err)
		}
	}

	rows := make([]toolCheckItem, 0, len(results))
	updatesAvailable := 0
	for _, r := range results {
		if r.UpdateAvailable {
			updatesAvailable++
		}
		rows = append(rows, toolCheckItem{
			Id:               r.Tool.Id,
			Name:             r.Tool.Name,
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
				Column:      output.Column{Heading: "NAME", ValueTemplate: "{{.Name}}"},
				Priority:    2,
				CardTitle:   true,
				Wrappable:   true,
				Truncatable: true,
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
					"Run %s to upgrade all installed tools.",
					output.WithHighLightFormat("azd tool upgrade"),
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
			strategy := toolDef.InstallStrategies[platform]
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
func runToolOperation(
	ctx context.Context,
	tools []*tool.ToolDefinition,
	operationFn toolOperationFn,
	title string,
	action string,
	console input.Console,
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

	taskList := uxlib.NewTaskList(
		&uxlib.TaskListOptions{ContinueOnError: true},
	)

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
	if taskErr != nil {
		// Build the past participle: "install" -> "installed",
		// "upgrade" -> "upgraded". Appending only "d" would be wrong,
		// so append "ed" unless the verb already ends in "e".
		participle := action + "ed"
		if strings.HasSuffix(action, "e") {
			participle = action + "d"
		}
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
// table, reusing the extension status vocabulary for consistency.
func toolCheckStatus(installed, updateAvailable bool) string {
	switch {
	case !installed:
		return statusNotInstall
	case updateAvailable:
		return statusUpdate
	default:
		return statusUpToDate
	}
}
