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

	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

// Register extension commands
func extensionActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("extension", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:     "extension",
			Aliases: []string{"ext"},
			Short:   "Manage azd extensions.",
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupBeta,
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
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newExtensionShowAction,
		FlagsResolver:  newExtensionShowFlags,
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
	console          input.Console
	writer           io.Writer
	sourceManager    *extensions.SourceManager
	extensionManager *extensions.Manager
}

func newExtensionListAction(
	flags *extensionListFlags,
	formatter output.Formatter,
	console input.Console,
	writer io.Writer,
	sourceManager *extensions.SourceManager,
	extensionManager *extensions.Manager,
) actions.Action {
	return &extensionListAction{
		flags:            flags,
		formatter:        formatter,
		console:          console,
		writer:           writer,
		sourceManager:    sourceManager,
		extensionManager: extensionManager,
	}
}

type extensionListItem struct {
	Id               string `json:"id"`
	Name             string `json:"name"`
	Namespace        string `json:"namespace"`
	Version          string `json:"version"`
	InstalledVersion string `json:"installedVersion"`
	Source           string `json:"source"`
}

func (a *extensionListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	options := &extensions.FilterOptions{
		Source: a.flags.source,
		Tags:   a.flags.tags,
	}

	if options.Source != "" {
		if _, err := a.sourceManager.Get(ctx, options.Source); err != nil {
			return nil, fmt.Errorf("extension source '%s' not found: %w", options.Source, err)
		}
	}

	registryExtensions, err := a.extensionManager.FindExtensions(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("failed listing extensions from registry: %w", err)
	}

	installedExtensions, err := a.extensionManager.ListInstalled()
	if err != nil {
		return nil, fmt.Errorf("failed listing installed extensions: %w", err)
	}

	extensionRows := []extensionListItem{}

	for _, extension := range registryExtensions {
		installedExtension, has := installedExtensions[extension.Id]
		installed := has && installedExtension.Source == extension.Source

		if a.flags.installed && !installed {
			continue
		}

		var installedVersion string
		if installed {
			installedVersion = installedExtension.Version
		}

		extensionRows = append(extensionRows, extensionListItem{
			Id:               extension.Id,
			Name:             extension.DisplayName,
			Namespace:        extension.Namespace,
			Version:          extension.Versions[len(extension.Versions)-1].Version,
			InstalledVersion: installedVersion,
			Source:           extension.Source,
		})
	}

	if len(extensionRows) == 0 {
		if a.flags.installed {
			a.console.Message(ctx, output.WithWarningFormat("WARNING: No extensions installed.\n"))
			a.console.Message(ctx, fmt.Sprintf(
				"Run %s to install extensions.",
				output.WithHighLightFormat("azd extension install <extension-name>"),
			))
		} else {
			a.console.Message(ctx, output.WithWarningFormat("WARNING: No extensions found in configured sources.\n"))
			a.console.Message(ctx, fmt.Sprintf(
				"Run %s to add a new extension source.",
				output.WithHighLightFormat("azd extension source add [flags]"),
			))
		}

		return nil, nil
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
				Heading:       "Installed Version",
				ValueTemplate: `{{.InstalledVersion}}`,
			},
			{
				Heading:       "Source",
				ValueTemplate: `{{.Source}}`,
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
type extensionShowFlags struct {
	source string
	global *internal.GlobalCommandOptions
}

func newExtensionShowFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *extensionShowFlags {
	flags := &extensionShowFlags{
		global: global,
	}
	cmd.Flags().StringVarP(&flags.source, "source", "s", "", "The extension source to use.")
	return flags
}

type extensionShowAction struct {
	args             []string
	flags            *extensionShowFlags
	console          input.Console
	formatter        output.Formatter
	writer           io.Writer
	extensionManager *extensions.Manager
}

func newExtensionShowAction(
	args []string,
	flags *extensionShowFlags,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	extensionManager *extensions.Manager,
) actions.Action {
	return &extensionShowAction{
		args:             args,
		flags:            flags,
		console:          console,
		formatter:        formatter,
		writer:           writer,
		extensionManager: extensionManager,
	}
}

type extensionShowItem struct {
	Id                string
	Source            string
	Namespace         string
	Description       string
	Tags              []string
	LatestVersion     string
	InstalledVersion  string
	AvailableVersions []string
	Usage             string
	Examples          []extensions.ExtensionExample
	Providers         []extensions.Provider
	Capabilities      []extensions.CapabilityType
}

func (t *extensionShowItem) Display(writer io.Writer) error {
	// Helper function to write a section with its own tabwriter
	writeSection := func(header string, rows [][]string) error {
		if len(rows) == 0 {
			return nil
		}

		// Write bold and underlined header
		underlinedHeader := output.WithUnderline("%s", header)
		boldUnderlinedHeader := output.WithBold("%s", underlinedHeader)
		_, err := fmt.Fprintf(writer, "%s\n", boldUnderlinedHeader)
		if err != nil {
			return err
		} // Create tabwriter for this section
		tabs := tabwriter.NewWriter(
			writer,
			0,
			output.TableTabSize,
			1,
			output.TablePadCharacter,
			output.TableFlags)

		// Write rows
		for _, row := range rows {
			_, err := tabs.Write([]byte(strings.Join(row, "\t") + "\n"))
			if err != nil {
				return err
			}
		}

		// Flush and add spacing
		if err := tabs.Flush(); err != nil {
			return err
		}
		_, err = fmt.Fprintln(writer)
		return err
	}

	// Extension Information section
	extensionInfo := [][]string{
		{"Id", ":", t.Id},
		{"Source", ":", t.Source},
		{"Namespace", ":", t.Namespace},
		{"Description", ":", t.Description},
	}
	if err := writeSection("Extension Information", extensionInfo); err != nil {
		return err
	}

	// Version Information section
	versionInfo := [][]string{
		{"Latest Version", ":", t.LatestVersion},
		{"Installed Version", ":", t.InstalledVersion},
	}
	// Only add Available Versions if there are any
	if len(t.AvailableVersions) > 0 {
		versionInfo = append(versionInfo, []string{"Available Versions", ":", strings.Join(t.AvailableVersions, ", ")})
	}
	// Only add Tags if they are defined
	if len(t.Tags) > 0 {
		versionInfo = append(versionInfo, []string{"Tags", ":", strings.Join(t.Tags, ", ")})
	}
	if err := writeSection("Version Information", versionInfo); err != nil {
		return err
	}

	// Capabilities section - only if there are capabilities
	if len(t.Capabilities) > 0 {
		capabilityRows := [][]string{}
		for _, capability := range t.Capabilities {
			capabilityRows = append(capabilityRows, []string{"-", string(capability)})
		}
		if err := writeSection("Capabilities", capabilityRows); err != nil {
			return err
		}
	}

	// Providers section - only if there are providers
	if len(t.Providers) > 0 {
		providerRows := [][]string{}
		for _, provider := range t.Providers {
			providerInfo := fmt.Sprintf("%s (%s) - %s", provider.Name, provider.Type, provider.Description)
			providerRows = append(providerRows, []string{"", "", providerInfo})
		}
		if err := writeSection("Providers", providerRows); err != nil {
			return err
		}
	}

	// Usage section
	usageRows := [][]string{
		{"", "", t.Usage},
	}
	if err := writeSection("Usage", usageRows); err != nil {
		return err
	}

	// Examples section - only if there are examples
	if len(t.Examples) > 0 {
		exampleRows := [][]string{}
		for _, example := range t.Examples {
			exampleRows = append(exampleRows, []string{"", "", example.Usage})
		}
		if err := writeSection("Examples", exampleRows); err != nil {
			return err
		}
	}

	return nil
}

func (a *extensionShowAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if len(a.args) == 0 {
		return nil, fmt.Errorf("must specify an extension name")
	}
	if len(a.args) > 1 {
		return nil, fmt.Errorf("cannot specify multiple extensions")
	}
	extensionId := a.args[0]
	filterOptions := &extensions.FilterOptions{
		Source: a.flags.source,
		Id:     extensionId,
	}

	extensionMatches, err := a.extensionManager.FindExtensions(ctx, filterOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to find extension: %w", err)
	}

	registryExtension, err := selectDistinctExtension(ctx, a.console, extensionId, extensionMatches, a.flags.global)
	if err != nil {
		return nil, err
	}

	latestVersion := registryExtension.Versions[len(registryExtension.Versions)-1]

	var otherVersions []string
	for _, version := range registryExtension.Versions {
		if version.Version != latestVersion.Version {
			otherVersions = append(otherVersions, version.Version)
		}
	}

	extensionDetails := extensionShowItem{
		Id:                registryExtension.Id,
		Source:            registryExtension.Source,
		Namespace:         registryExtension.Namespace,
		Description:       registryExtension.DisplayName,
		Tags:              registryExtension.Tags,
		LatestVersion:     latestVersion.Version,
		AvailableVersions: otherVersions,
		Usage:             latestVersion.Usage,
		Examples:          latestVersion.Examples,
		Providers:         latestVersion.Providers,
		Capabilities:      latestVersion.Capabilities,
		InstalledVersion:  "N/A",
	}

	installedExtension, err := a.extensionManager.GetInstalled(
		extensions.FilterOptions{Id: extensionId},
	)
	if err == nil && installedExtension.Source == extensionDetails.Source {
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
	source  string
	force   bool
	global  *internal.GlobalCommandOptions
}

func newExtensionInstallFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *extensionInstallFlags {
	flags := &extensionInstallFlags{
		global: global,
	}

	cmd.Flags().StringVarP(&flags.source, "source", "s", "", "The extension source to use for installs")
	cmd.Flags().StringVarP(&flags.version, "version", "v", "", "The version of the extension to install")
	cmd.Flags().
		BoolVarP(&flags.force, "force", "f", false, "Force installation even if it would downgrade the current version")

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

		// Check if extension is already installed (any source)
		allInstalled, err := a.extensionManager.ListInstalled()
		if err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, fmt.Errorf("failed to list installed extensions: %w", err)
		}

		installedExtension, alreadyInstalled := allInstalled[extensionId]

		// Find the extension metadata first
		filterOptions := &extensions.FilterOptions{
			Source:  a.flags.source,
			Version: a.flags.version,
			Id:      extensionId,
		}

		extensionMatches, err := a.extensionManager.FindExtensions(ctx, filterOptions)
		if err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, fmt.Errorf("failed to find extension: %w", err)
		}

		selectedExtension, err := selectDistinctExtension(ctx, a.console, extensionId, extensionMatches, a.flags.global)
		if err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, err
		}

		a.console.ShowSpinner(ctx, stepMessage, input.Step)

		// Determine target version
		targetVersion := a.flags.version
		if targetVersion == "" || targetVersion == "latest" {
			targetVersion = selectedExtension.Versions[len(selectedExtension.Versions)-1].Version
		}

		var extensionVersion *extensions.ExtensionVersion

		if alreadyInstalled {
			// Extension is already installed - apply smart upgrade/downgrade logic

			// Check if same version (regardless of source)
			if installedExtension.Version == targetVersion {
				stepMessage += output.WithGrayFormat(" (version %s already installed)", installedExtension.Version)
				a.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
				continue
			}

			// Parse versions for semantic comparison
			installedSemver, err := semver.NewVersion(installedExtension.Version)
			if err != nil {
				a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return nil, fmt.Errorf("failed to parse installed version '%s': %w", installedExtension.Version, err)
			}

			targetSemver, err := semver.NewVersion(targetVersion)
			if err != nil {
				a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return nil, fmt.Errorf("failed to parse target version '%s': %w", targetVersion, err)
			}

			if targetSemver.LessThan(installedSemver) && !a.flags.force {
				// Would be a downgrade - require --force
				stepMessage += output.WithGrayFormat(
					" (would downgrade from %s to %s, use --force to override)",
					installedExtension.Version,
					targetVersion,
				)
				a.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
				continue
			}

			// Use upgrade logic for existing installations
			a.console.ShowSpinner(ctx, stepMessage, input.Step)
			extensionVersion, err = a.extensionManager.Upgrade(ctx, selectedExtension, a.flags.version)
			if err != nil {
				a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return nil, fmt.Errorf("failed to upgrade extension: %w", err)
			}

			stepMessage += output.WithGrayFormat(" (%s)", extensionVersion.Version)
			a.console.StopSpinner(ctx, stepMessage, input.StepDone)

		} else {
			// Extension not installed - proceed with fresh install
			a.console.ShowSpinner(ctx, stepMessage, input.Step)
			extensionVersion, err = a.extensionManager.Install(ctx, selectedExtension, a.flags.version)
			if err != nil {
				a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return nil, fmt.Errorf("failed to install extension: %w", err)
			}

			stepMessage += output.WithGrayFormat(" (%s)", extensionVersion.Version)
			a.console.StopSpinner(ctx, stepMessage, input.StepDone)
		}

		displayExtensionUsageAndExamples(ctx, a.console, extensionVersion)
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

		installed, err := a.extensionManager.GetInstalled(extensions.FilterOptions{
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
	source  string
	all     bool
	global  *internal.GlobalCommandOptions
}

func newExtensionUpgradeFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *extensionUpgradeFlags {
	flags := &extensionUpgradeFlags{
		global: global,
	}
	cmd.Flags().StringVarP(&flags.version, "version", "v", "", "The version of the extension to upgrade to")
	cmd.Flags().StringVarP(&flags.source, "source", "s", "", "The extension source to use for upgrades")
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

		installed, err := a.extensionManager.GetInstalled(extensions.FilterOptions{
			Id: extensionId,
		})
		if err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, fmt.Errorf("failed to get installed extension: %w", err)
		}

		filterOptions := &extensions.FilterOptions{
			Id:      extensionId,
			Source:  a.flags.source,
			Version: a.flags.version,
		}

		matches, err := a.extensionManager.FindExtensions(ctx, filterOptions)
		if err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, fmt.Errorf("failed to get extension %s: %w", extensionId, err)
		}

		if len(matches) == 0 {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, fmt.Errorf("extension %s not found", extensionId)
		}

		selectedExtension, err := selectDistinctExtension(ctx, a.console, extensionId, matches, a.flags.global)
		if err != nil {
			return nil, err
		}

		a.console.ShowSpinner(ctx, stepMessage, input.Step)
		latestVersion := selectedExtension.Versions[len(selectedExtension.Versions)-1]

		// Parse semantic versions for proper comparison
		installedSemver, err := semver.NewVersion(installed.Version)
		if err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, fmt.Errorf("failed to parse installed version '%s': %w", installed.Version, err)
		}

		latestSemver, err := semver.NewVersion(latestVersion.Version)
		if err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, fmt.Errorf("failed to parse latest version '%s': %w", latestVersion.Version, err)
		}

		// Compare versions: skip if installed version >= latest version
		if installedSemver.GreaterThan(latestSemver) {
			stepMessage += output.WithGrayFormat(
				" (Installed version %s is newer than available %s)",
				installed.Version,
				latestVersion.Version,
			)
			a.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
		} else if installedSemver.Equal(latestSemver) {
			stepMessage += output.WithGrayFormat(" (No upgrade available)")
			a.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
		} else {
			extensionVersion, err := a.extensionManager.Upgrade(ctx, selectedExtension, a.flags.version)
			if err != nil {
				return nil, fmt.Errorf("failed to upgrade extension: %w", err)
			}

			stepMessage += output.WithGrayFormat(" (%s)", extensionVersion.Version)
			a.console.StopSpinner(ctx, stepMessage, input.StepDone)

			displayExtensionUsageAndExamples(ctx, a.console, extensionVersion)
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
	cmd.Flags().StringVarP(&flags.name, "name", "n", "", "The name of the extension source")
	cmd.Flags().StringVarP(&flags.location, "location", "l", "", "The location of the extension source")
	cmd.Flags().StringVarP(&flags.kind,
		"type", "t", "", "The type of the extension source. Supported types are 'file' and 'url'")

	return flags
}

type extensionSourceAddAction struct {
	flags         *extensionSourceAddFlags
	console       input.Console
	sourceManager *extensions.SourceManager
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
	}
}

func (a *extensionSourceAddAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Add extension source (azd extension source add)",
	})

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

	err = a.sourceManager.Add(ctx, a.flags.name, sourceConfig)
	a.console.StopSpinner(ctx, spinnerMessage, input.GetStepResultFormat(err))
	if err != nil {
		return nil, fmt.Errorf("failed adding extension source: %w", err)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   fmt.Sprintf("Added azd extension source %s", a.flags.name),
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
	if len(a.args) == 0 {
		return nil, fmt.Errorf("must specify an extension source name")
	}
	if len(a.args) > 1 {
		return nil, fmt.Errorf("cannot specify multiple extension sources")
	}
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

func displayExtensionUsageAndExamples(
	ctx context.Context,
	console input.Console,
	extensionVersion *extensions.ExtensionVersion,
) {
	console.Message(ctx, fmt.Sprintf("      %s %s", output.WithBold("Usage: "), extensionVersion.Usage))
	console.Message(ctx, output.WithBold("      Examples:"))

	for _, example := range extensionVersion.Examples {
		console.Message(ctx, "        "+output.WithHighLightFormat(example.Usage))
	}
}

func selectDistinctExtension(
	ctx context.Context,
	console input.Console,
	extensionId string,
	matches []*extensions.ExtensionMetadata,
	global *internal.GlobalCommandOptions,
) (*extensions.ExtensionMetadata, error) {
	if len(matches) == 0 {
		return nil, fmt.Errorf("no extensions found")
	}

	if len(matches) == 1 {
		return matches[0], nil
	}

	if global.NoPrompt {
		return nil, &internal.ErrorWithSuggestion{
			Err:        fmt.Errorf("the %s extension was found in multiple sources.", extensionId),
			Suggestion: "Specify the extension source using the --source flag.",
		}
	}

	console.StopSpinner(ctx, "", input.Step)

	sourceChoices := make([]*uxlib.SelectChoice, len(matches))
	for i, ext := range matches {
		sourceChoices[i] = &uxlib.SelectChoice{
			Value: ext.Source,
			Label: ext.Source,
		}
	}

	selectSource := uxlib.NewSelect(&uxlib.SelectOptions{
		Message: fmt.Sprintf(
			"The %s extension was found in multiple sources.\nSelect the source to continue",
			output.WithHighLightFormat(extensionId),
		),
		Choices: sourceChoices,
	})

	sourceResponseIndex, err := selectSource.Ask(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to select extension source: %w", err)
	}

	console.Message(ctx, "")

	return matches[*sourceResponseIndex], nil
}
