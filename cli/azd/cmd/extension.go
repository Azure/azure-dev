// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/spf13/cobra"
)

// Register extension commands
func extensionActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("extension", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:     "extension",
			Aliases: []string{"ext"},
			Short:   fmt.Sprintf("Manage azd extensions. %s", output.WithWarningFormat("(Alpha)")),
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupConfig,
		},
	})

	// azd extension list [--installed]
	group.Add("list", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "list [--installed]",
			Short: "List available extensions.",
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat:  output.TableFormat,
		ActionResolver: newExtensionListAction,
		FlagsResolver:  newExtensionListFlags,
	})

	// azd extension show
	group.Add("show", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "show <extension-name>",
			Short: "Show details for a specific extension.",
			Args:  cobra.ExactArgs(1),
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newExtensionShowAction,
	})

	// azd extension install <extension-name>
	group.Add("install", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "install <extension-name>",
			Short: "Installs specified extensions.",
		},
		ActionResolver: newExtensionInstallAction,
		FlagsResolver:  newExtensionInstallFlags,
	})

	// azd extension uninstall <extension-name>
	group.Add("uninstall", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "uninstall <extension-name>",
			Short: "Uninstall specified extensions.",
		},
		ActionResolver: newExtensionUninstallAction,
		FlagsResolver:  newExtensionUninstallFlags,
	})

	// azd extension upgrade <extension-name>
	group.Add("upgrade", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "upgrade <extension-name>",
			Short: "Upgrade specified extensions.",
		},
		ActionResolver: newExtensionUpgradeAction,
		FlagsResolver:  newExtensionUpgradeFlags,
	})

	sourceGroup := group.Add("source", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "source",
			Short: "View and manage extension sources",
		},
	})

	sourceGroup.Add("list", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "list",
			Short: "List extension sources",
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat:  output.TableFormat,
		ActionResolver: newExtensionSourceListAction,
	})

	sourceGroup.Add("add", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "add",
			Short: "Add an extension source with the specified name",
		},
		ActionResolver: newExtensionSourceAddAction,
		FlagsResolver:  newExtensionSourceAddFlags,
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})

	sourceGroup.Add("remove", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "remove <name>",
			Short: "Remove an extension source with the specified name",
		},
		ActionResolver: newExtensionSourceRemoveAction,
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})

	return group
}

type extensionListFlags struct {
	installed bool
	source    string
	tags      []string
}

func newExtensionListFlags(cmd *cobra.Command) *extensionListFlags {
	flags := &extensionListFlags{}
	cmd.Flags().BoolVar(&flags.installed, "installed", false, "List installed extensions")
	cmd.Flags().StringVar(&flags.source, "source", "", "Filter extensions by source")
	cmd.Flags().StringSliceVar(&flags.tags, "tags", nil, "Filter extensions by tags")

	return flags
}

// azd extension list [--installed]
type extensionListAction struct {
	flags            *extensionListFlags
	formatter        output.Formatter
	writer           io.Writer
	sourceManager    *extensions.SourceManager
	extensionManager *extensions.Manager
}

func newExtensionListAction(
	flags *extensionListFlags,
	formatter output.Formatter,
	writer io.Writer,
	sourceManager *extensions.SourceManager,
	extensionManager *extensions.Manager,
) actions.Action {
	return &extensionListAction{
		flags:            flags,
		formatter:        formatter,
		writer:           writer,
		sourceManager:    sourceManager,
		extensionManager: extensionManager,
	}
}

type extensionListItem struct {
	Id        string
	Name      string
	Namespace string
	Version   string
	Installed bool
}

func (a *extensionListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	options := &extensions.ListOptions{
		Source: a.flags.source,
		Tags:   a.flags.tags,
	}

	if options.Source != "" {
		if _, err := a.sourceManager.Get(ctx, options.Source); err != nil {
			return nil, fmt.Errorf("extension source '%s' not found: %w", options.Source, err)
		}
	}

	registryExtensions, err := a.extensionManager.ListFromRegistry(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("failed listing extensions from registry: %w", err)
	}

	installedExtensions, err := a.extensionManager.ListInstalled()
	if err != nil {
		return nil, fmt.Errorf("failed listing installed extensions: %w", err)
	}

	extensionRows := []extensionListItem{}

	for _, extension := range registryExtensions {
		installedExtension, installed := installedExtensions[extension.Id]
		if a.flags.installed && !installed {
			continue
		}

		var version string
		if installed {
			version = installedExtension.Version
		} else {
			version = extension.Versions[len(extension.Versions)-1].Version
		}

		extensionRows = append(extensionRows, extensionListItem{
			Id:        extension.Id,
			Name:      extension.DisplayName,
			Namespace: extension.Namespace,
			Version:   version,
			Installed: installedExtensions[extension.Id] != nil,
		})
	}

	var formatErr error

	if a.formatter.Kind() == output.TableFormat {
		columns := []output.Column{
			{
				Heading:       "Id",
				ValueTemplate: "{{.Id}}",
			},
			{
				Heading:       "Name",
				ValueTemplate: "{{.Name}}",
			},
			{
				Heading:       "Version",
				ValueTemplate: `{{.Version}}`,
			},
			{
				Heading:       "Installed",
				ValueTemplate: `{{.Installed}}`,
			},
		}

		formatErr = a.formatter.Format(extensionRows, a.writer, output.TableFormatterOptions{
			Columns: columns,
		})
	} else {
		formatErr = a.formatter.Format(extensionRows, a.writer, nil)
	}

	return nil, formatErr
}

// azd extension show
type extensionShowAction struct {
	args             []string
	formatter        output.Formatter
	writer           io.Writer
	extensionManager *extensions.Manager
}

func newExtensionShowAction(
	args []string,
	formatter output.Formatter,
	writer io.Writer,
	extensionManager *extensions.Manager,
) actions.Action {
	return &extensionShowAction{
		args:             args,
		formatter:        formatter,
		writer:           writer,
		extensionManager: extensionManager,
	}
}

type extensionShowItem struct {
	Name             string
	Description      string
	LatestVersion    string
	InstalledVersion string
	Usage            string
	Examples         []extensions.ExtensionExample
}

func (t *extensionShowItem) Display(writer io.Writer) error {
	tabs := tabwriter.NewWriter(
		writer,
		0,
		output.TableTabSize,
		1,
		output.TablePadCharacter,
		output.TableFlags)
	text := [][]string{
		{"Name", ":", t.Name},
		{"Description", ":", t.Description},
		{"Latest Version", ":", t.LatestVersion},
		{"Installed Version", ":", t.InstalledVersion},
		{"", "", ""},
		{"Usage", ":", t.Usage},
		{"Examples", ":", ""},
	}

	for _, example := range t.Examples {
		text = append(text, []string{"", "", example.Usage})
	}

	for _, line := range text {
		_, err := tabs.Write([]byte(strings.Join(line, "\t") + "\n"))
		if err != nil {
			return err
		}
	}

	return tabs.Flush()
}

func (a *extensionShowAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	extensionId := a.args[0]
	registryExtension, err := a.extensionManager.GetFromRegistry(ctx, extensionId)
	if err != nil {
		return nil, fmt.Errorf("failed to get extension details: %w", err)
	}

	latestVersion := registryExtension.Versions[len(registryExtension.Versions)-1]

	extensionDetails := extensionShowItem{
		Name:             registryExtension.Id,
		Description:      registryExtension.DisplayName,
		LatestVersion:    latestVersion.Version,
		Usage:            latestVersion.Usage,
		Examples:         latestVersion.Examples,
		InstalledVersion: "N/A",
	}

	installedExtension, err := a.extensionManager.GetInstalled(
		extensions.GetInstalledOptions{Id: extensionId},
	)
	if err == nil {
		extensionDetails.InstalledVersion = installedExtension.Version
	}

	var formatErr error

	if a.formatter.Kind() == output.NoneFormat {
		formatErr = extensionDetails.Display(a.writer)
	} else {
		formatErr = a.formatter.Format(extensionDetails, a.writer, nil)
	}

	return nil, formatErr
}

type extensionInstallFlags struct {
	version string
}

func newExtensionInstallFlags(cmd *cobra.Command) *extensionInstallFlags {
	flags := &extensionInstallFlags{}
	cmd.Flags().StringVarP(&flags.version, "version", "v", "", "The version of the extension to install")

	return flags
}

// azd extension install
type extensionInstallAction struct {
	args             []string
	flags            *extensionInstallFlags
	console          input.Console
	extensionManager *extensions.Manager
}

func newExtensionInstallAction(
	args []string,
	flags *extensionInstallFlags,
	console input.Console,
	extensionManager *extensions.Manager,
) actions.Action {
	return &extensionInstallAction{
		args:             args,
		flags:            flags,
		console:          console,
		extensionManager: extensionManager,
	}
}

func (a *extensionInstallAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Install an azd extension (azd extension install)",
		TitleNote: "Installs the specified extension onto the local machine",
	})

	extensionIds := a.args
	if len(extensionIds) == 0 {
		return nil, fmt.Errorf("must specify an extension name")
	}

	if len(extensionIds) > 1 && a.flags.version != "" {
		return nil, fmt.Errorf("cannot specify --version flag when using multiple extensions")
	}

	for index, extensionId := range extensionIds {
		if index > 0 {
			a.console.Message(ctx, "")
		}

		stepMessage := fmt.Sprintf("Installing %s extension", output.WithHighLightFormat(extensionId))
		a.console.ShowSpinner(ctx, stepMessage, input.Step)

		installed, err := a.extensionManager.GetInstalled(extensions.GetInstalledOptions{
			Id: extensionId,
		})
		if err == nil {
			stepMessage += output.WithGrayFormat(" (version %s already installed)", installed.Version)
			a.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
			continue
		}

		extensionVersion, err := a.extensionManager.Install(ctx, extensionId, a.flags.version)
		if err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, fmt.Errorf("failed to install extension: %w", err)
		}

		stepMessage += output.WithGrayFormat(" (%s)", extensionVersion.Version)
		a.console.StopSpinner(ctx, stepMessage, input.StepDone)

		a.console.Message(ctx, fmt.Sprintf("      %s %s", output.WithBold("Usage: "), extensionVersion.Usage))
		a.console.Message(ctx, output.WithBold("      Examples:"))

		for _, example := range extensionVersion.Examples {
			a.console.Message(ctx, "        "+output.WithHighLightFormat(example.Usage))
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Extension(s) installed successfully",
		},
	}, nil
}

// azd extension uninstall
type extensionUninstallFlags struct {
	all bool
}

func newExtensionUninstallFlags(cmd *cobra.Command) *extensionUninstallFlags {
	flags := &extensionUninstallFlags{}
	cmd.Flags().BoolVar(&flags.all, "all", false, "Uninstall all installed extensions")

	return flags
}

type extensionUninstallAction struct {
	args             []string
	flags            *extensionUninstallFlags
	console          input.Console
	extensionManager *extensions.Manager
}

func newExtensionUninstallAction(
	args []string,
	flags *extensionUninstallFlags,
	console input.Console,
	extensionManager *extensions.Manager,
) actions.Action {
	return &extensionUninstallAction{
		args:             args,
		flags:            flags,
		console:          console,
		extensionManager: extensionManager,
	}
}

func (a *extensionUninstallAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if len(a.args) > 0 && a.flags.all {
		return nil, fmt.Errorf("cannot specify both an extension name and --all flag")
	}

	if len(a.args) == 0 && !a.flags.all {
		return nil, fmt.Errorf("must specify an extension name or use --all flag")
	}

	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Uninstall an azd extension (azd extension uninstall)",
		TitleNote: "Uninstalls the specified extension from the local machine",
	})

	extensionIds := a.args
	if a.flags.all {
		installed, err := a.extensionManager.ListInstalled()
		if err != nil {
			return nil, fmt.Errorf("failed to list installed extensions: %w", err)
		}

		extensionIds = make([]string, 0, len(installed))
		for name := range installed {
			extensionIds = append(extensionIds, name)
		}
	}

	if len(extensionIds) == 0 {
		return nil, fmt.Errorf("no extensions to uninstall")
	}

	for _, extensionId := range extensionIds {
		stepMessage := fmt.Sprintf("Uninstalling %s extension", output.WithHighLightFormat(extensionId))

		installed, err := a.extensionManager.GetInstalled(extensions.GetInstalledOptions{
			Id: extensionId,
		})
		if err != nil {
			a.console.ShowSpinner(ctx, stepMessage, input.Step)
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)

			return nil, fmt.Errorf("failed to get installed extension: %w", err)
		}

		stepMessage += fmt.Sprintf(" (%s)", installed.Version)
		a.console.ShowSpinner(ctx, stepMessage, input.Step)

		if err := a.extensionManager.Uninstall(extensionId); err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, fmt.Errorf("failed to uninstall extension: %w", err)
		}

		a.console.StopSpinner(ctx, stepMessage, input.StepDone)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Extension(s) uninstalled successfully",
		},
	}, nil
}

type extensionUpgradeFlags struct {
	version string
	all     bool
}

func newExtensionUpgradeFlags(cmd *cobra.Command) *extensionUpgradeFlags {
	flags := &extensionUpgradeFlags{}
	cmd.Flags().StringVarP(&flags.version, "version", "v", "", "The version of the extension to upgrade to")
	cmd.Flags().BoolVar(&flags.all, "all", false, "Upgrade all installed extensions")

	return flags
}

// azd extension upgrade
type extensionUpgradeAction struct {
	args             []string
	flags            *extensionUpgradeFlags
	console          input.Console
	extensionManager *extensions.Manager
}

func newExtensionUpgradeAction(
	args []string,
	flags *extensionUpgradeFlags,
	console input.Console,
	extensionManager *extensions.Manager,
) actions.Action {
	return &extensionUpgradeAction{
		args:             args,
		flags:            flags,
		console:          console,
		extensionManager: extensionManager,
	}
}

func (a *extensionUpgradeAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if len(a.args) > 0 && a.flags.all {
		return nil, fmt.Errorf("cannot specify both an extension name and --all flag")
	}

	if len(a.args) > 1 && a.flags.version != "" {
		return nil, fmt.Errorf("cannot specify --version flag when using multiple extensions")
	}

	if len(a.args) == 0 && !a.flags.all {
		return nil, fmt.Errorf("must specify an extension name or use --all flag")
	}

	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Upgrade azd extensions (azd extension upgrade)",
		TitleNote: "Upgrades the specified extensions on the local machine",
	})

	extensionIds := a.args
	if a.flags.all {
		installed, err := a.extensionManager.ListInstalled()
		if err != nil {
			return nil, fmt.Errorf("failed to list installed extensions: %w", err)
		}

		extensionIds = make([]string, 0, len(installed))
		for name := range installed {
			extensionIds = append(extensionIds, name)
		}
	}

	if len(extensionIds) == 0 {
		return nil, fmt.Errorf("no extensions to upgrade")
	}

	for index, extensionId := range extensionIds {
		if index > 0 {
			a.console.Message(ctx, "")
		}

		stepMessage := fmt.Sprintf("Upgrading %s extension", output.WithHighLightFormat(extensionId))
		a.console.ShowSpinner(ctx, stepMessage, input.Step)

		installed, err := a.extensionManager.GetInstalled(extensions.GetInstalledOptions{
			Id: extensionId,
		})
		if err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, fmt.Errorf("failed to get installed extension: %w", err)
		}

		extension, err := a.extensionManager.GetFromRegistry(ctx, extensionId)
		if err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, fmt.Errorf("failed to get extension %s: %w", extensionId, err)
		}

		latestVersion := extension.Versions[len(extension.Versions)-1]
		if latestVersion.Version == installed.Version {
			stepMessage += output.WithGrayFormat(" (No upgrade available)")
			a.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
		} else {
			extensionVersion, err := a.extensionManager.Upgrade(ctx, extensionId, a.flags.version)
			if err != nil {
				return nil, fmt.Errorf("failed to upgrade extension: %w", err)
			}

			stepMessage += output.WithGrayFormat(" (%s)", extensionVersion.Version)
			a.console.StopSpinner(ctx, stepMessage, input.StepDone)

			a.console.Message(ctx, fmt.Sprintf("      %s %s", output.WithBold("Usage: "), extensionVersion.Usage))
			a.console.Message(ctx, output.WithBold("      Examples:"))

			for _, example := range extensionVersion.Examples {
				a.console.Message(ctx, "        "+output.WithHighLightFormat(example.Usage))
			}
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Extensions upgraded successfully",
		},
	}, nil
}

type extensionSourceListAction struct {
	formatter     output.Formatter
	writer        io.Writer
	sourceManager *extensions.SourceManager
}

func newExtensionSourceListAction(
	formatter output.Formatter,
	writer io.Writer,
	sourceManager *extensions.SourceManager,
) actions.Action {
	return &extensionSourceListAction{
		formatter:     formatter,
		writer:        writer,
		sourceManager: sourceManager,
	}
}

func (a *extensionSourceListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	sourceConfigs, err := a.sourceManager.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list extension sources: %w", err)
	}

	if a.formatter.Kind() == output.TableFormat {
		columns := []output.Column{
			{
				Heading:       "Name",
				ValueTemplate: "{{.Name}}",
			},
			{
				Heading:       "Type",
				ValueTemplate: "{{.Type}}",
			},
			{
				Heading:       "Location",
				ValueTemplate: "{{.Location}}",
			},
		}

		err = a.formatter.Format(sourceConfigs, a.writer, output.TableFormatterOptions{
			Columns: columns,
		})
	} else {
		err = a.formatter.Format(sourceConfigs, a.writer, nil)
	}

	return nil, err
}

type extensionSourceAddFlags struct {
	name     string
	location string
	kind     string
}

func newExtensionSourceAddFlags(cmd *cobra.Command) *extensionSourceAddFlags {
	flags := &extensionSourceAddFlags{}
	cmd.Flags().StringVar(&flags.name, "name", "", "The name of the extension source")
	cmd.Flags().StringVar(&flags.location, "location", "", "The location of the extension source")
	cmd.Flags().StringVar(&flags.kind, "king", "", "The type of the extension source")

	return flags
}

type extensionSourceAddAction struct {
	flags         *extensionSourceAddFlags
	console       input.Console
	sourceManager *extensions.SourceManager
	args          []string
}

func newExtensionSourceAddAction(
	flags *extensionSourceAddFlags,
	console input.Console,
	sourceManager *extensions.SourceManager,
	args []string,
) actions.Action {
	return &extensionSourceAddAction{
		flags:         flags,
		console:       console,
		sourceManager: sourceManager,
		args:          args,
	}
}

func (a *extensionSourceAddAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Add extension source (azd extension source add)",
	})

	var name = strings.ToLower(a.args[0])

	spinnerMessage := "Validating extension source"
	a.console.ShowSpinner(ctx, spinnerMessage, input.Step)

	sourceConfig := &extensions.SourceConfig{
		Type:     extensions.SourceKind(a.flags.kind),
		Location: a.flags.location,
		Name:     a.flags.name,
	}

	// Validate the custom source config
	_, err := a.sourceManager.CreateSource(ctx, sourceConfig)
	a.console.StopSpinner(ctx, spinnerMessage, input.GetStepResultFormat(err))
	if err != nil {
		if errors.Is(err, extensions.ErrSourceTypeInvalid) {
			return nil, fmt.Errorf(
				"extension source type '%s' is not supported. Supported types are %s",
				a.flags.kind,
				ux.ListAsText([]string{"'file'", "'url'"}),
			)
		}

		return nil, fmt.Errorf("extension source validation failed: %w", err)
	}

	spinnerMessage = "Saving extension source"
	a.console.ShowSpinner(ctx, spinnerMessage, input.Step)

	err = a.sourceManager.Add(ctx, name, sourceConfig)
	a.console.StopSpinner(ctx, spinnerMessage, input.GetStepResultFormat(err))
	if err != nil {
		return nil, fmt.Errorf("failed adding extension source: %w", err)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   fmt.Sprintf("Added azd extension source %s", name),
			FollowUp: "Run `azd extension list` to see the available set of azd extensions.",
		},
	}, nil
}

type extensionSourceRemoveAction struct {
	sourceManager *extensions.SourceManager
	console       input.Console
	args          []string
}

func newExtensionSourceRemoveAction(
	sourceManager *extensions.SourceManager,
	console input.Console,
	args []string,
) actions.Action {
	return &extensionSourceRemoveAction{
		sourceManager: sourceManager,
		console:       console,
		args:          args,
	}
}

func (a *extensionSourceRemoveAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Remove extension source (azd extension source remove)",
	})

	var key = strings.ToLower(a.args[0])
	spinnerMessage := fmt.Sprintf("Removing extension source (%s)", key)
	a.console.ShowSpinner(ctx, spinnerMessage, input.Step)
	err := a.sourceManager.Remove(ctx, key)
	a.console.StopSpinner(ctx, spinnerMessage, input.GetStepResultFormat(err))
	if err != nil {
		return nil, fmt.Errorf("failed removing extension source: %w", err)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("Removed azd extension source %s", key),
			FollowUp: fmt.Sprintf(
				"Add more extension sources by running %s",
				output.WithHighLightFormat("azd extension source add <key>"),
			),
		},
	}, nil
}
