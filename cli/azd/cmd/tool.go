// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/tool"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

// toolActions registers the "azd tool" command group and all of its subcommands.
func toolActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("tool", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "tool",
			Short: "Manage Azure development tools.",
			Long:  "Discover, install, upgrade, and check status of Azure development tools.",
		},
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
		ActionResolver: newToolInstallAction,
		FlagsResolver:  newToolInstallFlags,
	})

	// azd tool upgrade [tool-name...]
	group.Add("upgrade", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "upgrade [tool-name...]",
			Short: "Upgrade installed tools.",
		},
		ActionResolver: newToolUpgradeAction,
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
	statuses, err := a.manager.DetectAll(ctx)
	if err != nil {
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

	taskList := uxlib.NewTaskList(
		&uxlib.TaskListOptions{ContinueOnError: true},
	)

	for _, id := range ids {
		capturedID := id
		toolDef, findErr := a.manager.FindTool(capturedID)
		if findErr != nil {
			return nil, findErr
		}

		taskList.AddTask(uxlib.TaskOptions{
			Title: fmt.Sprintf("Installing %s", toolDef.Name),
			Action: func(setProgress uxlib.SetProgressFunc) (uxlib.TaskState, error) {
				results, installErr := a.manager.InstallTools(ctx, []string{capturedID})
				if installErr != nil {
					return uxlib.Error, installErr
				}
				if len(results) > 0 && !results[0].Success {
					return uxlib.Error, results[0].Error
				}
				return uxlib.Success, nil
			},
		})
	}

	if err := taskList.Run(); err != nil {
		a.console.Message(ctx, output.WithWarningFormat(
			"\nSome tools could not be installed. Run 'azd tool list' for details.",
		))
	}

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
	statuses, err := a.manager.DetectAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("detecting tools: %w", err)
	}

	rows := make([]toolListItem, 0, len(statuses))
	for _, s := range statuses {
		status := "Not Installed"
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
		columns := []output.Column{
			{Heading: "Id", ValueTemplate: "{{.Id}}"},
			{Heading: "Name", ValueTemplate: "{{.Name}}"},
			{Heading: "Category", ValueTemplate: "{{.Category}}"},
			{Heading: "Priority", ValueTemplate: "{{.Priority}}"},
			{Heading: "Status", ValueTemplate: "{{.Status}}"},
			{Heading: "Version", ValueTemplate: "{{.Version}}"},
		}

		formatErr = a.formatter.Format(
			rows, a.writer, output.TableFormatterOptions{Columns: columns},
		)
	} else {
		formatErr = a.formatter.Format(rows, a.writer, nil)
	}

	return nil, formatErr
}

// ---------------------------------------------------------------------------
// azd tool install [tool-name...]
// ---------------------------------------------------------------------------

type toolInstallFlags struct {
	all bool
}

func newToolInstallFlags(cmd *cobra.Command) *toolInstallFlags {
	flags := &toolInstallFlags{}
	cmd.Flags().BoolVar(
		&flags.all, "all", false, "Install all recommended tools",
	)
	return flags
}

type toolInstallAction struct {
	args    []string
	flags   *toolInstallFlags
	manager *tool.Manager
	console input.Console
}

func newToolInstallAction(
	args []string,
	flags *toolInstallFlags,
	manager *tool.Manager,
	console input.Console,
) actions.Action {
	return &toolInstallAction{
		args:    args,
		flags:   flags,
		manager: manager,
		console: console,
	}
}

func (a *toolInstallAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Install Azure development tools (azd tool install)",
		TitleNote: "Installs specified tools onto the local machine",
	})

	ids, err := a.resolveToolIds(ctx)
	if err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		a.console.Message(ctx, output.WithSuccessFormat("Nothing to install."))
		return nil, nil
	}

	taskList := uxlib.NewTaskList(
		&uxlib.TaskListOptions{ContinueOnError: true},
	)

	for _, id := range ids {
		capturedID := id
		toolDef, findErr := a.manager.FindTool(capturedID)
		if findErr != nil {
			return nil, findErr
		}

		taskList.AddTask(uxlib.TaskOptions{
			Title: fmt.Sprintf("Installing %s", toolDef.Name),
			Action: func(setProgress uxlib.SetProgressFunc) (uxlib.TaskState, error) {
				results, installErr := a.manager.InstallTools(ctx, []string{capturedID})
				if installErr != nil {
					return uxlib.Error, installErr
				}
				if len(results) > 0 && !results[0].Success {
					return uxlib.Error, results[0].Error
				}
				return uxlib.Success, nil
			},
		})
	}

	if err := taskList.Run(); err != nil {
		a.console.Message(ctx, output.WithWarningFormat(
			"\nSome tools could not be installed. Run 'azd tool list' for details.",
		))
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Tool installation complete",
		},
	}, nil
}

// resolveToolIds determines which tool IDs to install based on flags and arguments.
func (a *toolInstallAction) resolveToolIds(ctx context.Context) ([]string, error) {
	// --all: install all recommended tools that are not already installed.
	if a.flags.all {
		statuses, err := a.manager.DetectAll(ctx)
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
	statuses, err := a.manager.DetectAll(ctx)
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

type toolUpgradeAction struct {
	args    []string
	manager *tool.Manager
	console input.Console
}

func newToolUpgradeAction(
	args []string,
	manager *tool.Manager,
	console input.Console,
) actions.Action {
	return &toolUpgradeAction{
		args:    args,
		manager: manager,
		console: console,
	}
}

func (a *toolUpgradeAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Upgrade Azure development tools (azd tool upgrade)",
		TitleNote: "Upgrades installed tools to their latest versions",
	})

	// Determine which tools to upgrade — resolve tool definitions
	// up front but defer the actual upgrade work to each task callback
	// so that the spinner reflects real-time progress.
	var toolsToUpgrade []*tool.ToolDefinition

	if len(a.args) > 0 {
		for _, id := range a.args {
			toolDef, findErr := a.manager.FindTool(id)
			if findErr != nil {
				return nil, findErr
			}
			toolsToUpgrade = append(toolsToUpgrade, toolDef)
		}
	} else {
		statuses, detectErr := a.manager.DetectAll(ctx)
		if detectErr != nil {
			return nil, fmt.Errorf("detecting installed tools: %w", detectErr)
		}
		for _, s := range statuses {
			if s.Installed {
				toolsToUpgrade = append(toolsToUpgrade, s.Tool)
			}
		}
	}

	if len(toolsToUpgrade) == 0 {
		a.console.Message(ctx, output.WithGrayFormat("No installed tools to upgrade."))
		return nil, nil
	}

	taskList := uxlib.NewTaskList(
		&uxlib.TaskListOptions{ContinueOnError: true},
	)

	for _, t := range toolsToUpgrade {
		capturedTool := t
		taskList.AddTask(uxlib.TaskOptions{
			Title: fmt.Sprintf("Upgrading %s", capturedTool.Name),
			Action: func(setProgress uxlib.SetProgressFunc) (uxlib.TaskState, error) {
				results, upgradeErr := a.manager.UpgradeTools(ctx, []string{capturedTool.Id})
				if upgradeErr != nil {
					return uxlib.Error, upgradeErr
				}
				if len(results) > 0 {
					r := results[0]
					if r.Error != nil {
						return uxlib.Error, r.Error
					}
					if !r.Success {
						return uxlib.Warning, fmt.Errorf("upgrade did not succeed")
					}
					if r.InstalledVersion != "" {
						setProgress(r.InstalledVersion)
					}
				}
				return uxlib.Success, nil
			},
		})
	}

	if err := taskList.Run(); err != nil {
		a.console.Message(ctx, output.WithWarningFormat(
			"\nSome tools could not be upgraded. Run 'azd tool check' for details.",
		))
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Tool upgrade complete",
		},
	}, nil
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
	results, err := a.manager.CheckForUpdates(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking for updates: %w", err)
	}

	rows := make([]toolCheckItem, 0, len(results))
	for _, r := range results {
		rows = append(rows, toolCheckItem{
			Id:               r.Tool.Id,
			Name:             r.Tool.Name,
			InstalledVersion: r.CurrentVersion,
			LatestVersion:    r.LatestVersion,
			UpdateAvailable:  r.UpdateAvailable,
		})
	}

	if len(rows) == 0 {
		a.console.Message(ctx, output.WithGrayFormat("No tools found."))
		return nil, nil
	}

	var formatErr error

	if a.formatter.Kind() == output.TableFormat {
		columns := []output.Column{
			{Heading: "Id", ValueTemplate: "{{.Id}}"},
			{Heading: "Name", ValueTemplate: "{{.Name}}"},
			{Heading: "Installed Version", ValueTemplate: "{{.InstalledVersion}}"},
			{Heading: "Latest Version", ValueTemplate: "{{.LatestVersion}}"},
			{
				Heading:       "Update Available",
				ValueTemplate: `{{if .UpdateAvailable}}Yes{{else}}No{{end}}`,
			},
		}

		formatErr = a.formatter.Format(
			rows, a.writer, output.TableFormatterOptions{Columns: columns},
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
	args    []string
	console input.Console
	manager *tool.Manager
	writer  io.Writer
}

func newToolShowAction(
	args []string,
	manager *tool.Manager,
	console input.Console,
	writer io.Writer,
) actions.Action {
	return &toolShowAction{
		args:    args,
		manager: manager,
		console: console,
		writer:  writer,
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
		return nil, fmt.Errorf("finding tool: %w", err)
	}

	status, err := a.manager.DetectTool(ctx, toolID)
	if err != nil {
		return nil, fmt.Errorf("detecting tool: %w", err)
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
	installedVersion := "Not Installed"
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
		for platform, strategy := range toolDef.InstallStrategies {
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
