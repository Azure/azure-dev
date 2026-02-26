// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
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
			Use:   "show <extension-id>",
			Short: "Show details for a specific extension.",
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newExtensionShowAction,
		FlagsResolver:  newExtensionShowFlags,
	})

	// azd extension install <extension-id>
	group.Add("install", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "install <extension-id>",
			Short: "Installs specified extensions.",
		},
		ActionResolver: newExtensionInstallAction,
		FlagsResolver:  newExtensionInstallFlags,
	})

	// azd extension uninstall <extension-id>
	group.Add("uninstall", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "uninstall [extension-id]",
			Short: "Uninstall specified extensions.",
		},
		ActionResolver: newExtensionUninstallAction,
		FlagsResolver:  newExtensionUninstallFlags,
	})

	// azd extension upgrade <extension-id>
	group.Add("upgrade", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "upgrade [extension-id]",
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

	// azd extension source validate <name-or-path-or-url>
	sourceGroup.Add("validate", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "validate <name-or-path-or-url>",
			Short: "Validate an extension source's registry.json file.",
			Long: "Validate an extension source's registry.json file.\n\n" +
				"Accepts a source name (from 'azd extension source list'), a local file path,\n" +
				"or a URL. Checks required fields, valid capabilities, semver version format,\n" +
				"platform artifact structure, and extension ID format.",
		},
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		ActionResolver: newExtensionSourceValidateAction,
		FlagsResolver:  newExtensionSourceValidateFlags,
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
	UpdateAvailable  bool   `json:"updateAvailable"`
	Incompatible     bool   `json:"-"`
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
	azdVersion := currentAzdSemver()

	for _, extension := range registryExtensions {
		if len(extension.Versions) == 0 {
			continue
		}

		installedExtension, has := installedExtensions[extension.Id]
		installed := has && installedExtension.Source == extension.Source

		if a.flags.installed && !installed {
			continue
		}

		// Always show the true latest version
		latestVersion := extension.Versions[len(extension.Versions)-1].Version

		var installedVersion string
		var updateAvailable bool
		var updateIncompatible bool

		if installed {
			installedVersion = installedExtension.Version

			// Compare versions to determine if an update is available
			installedSemver, installedErr := semver.NewVersion(installedExtension.Version)
			latestSemver, latestErr := semver.NewVersion(latestVersion)
			if installedErr == nil && latestErr == nil {
				updateAvailable = latestSemver.GreaterThan(installedSemver)
			}

			// Check if the update is incompatible with the current azd version
			if updateAvailable && azdVersion != nil {
				compatResult := extensions.FilterCompatibleVersions(extension.Versions, azdVersion)
				updateIncompatible = compatResult.HasNewerIncompatible
			}
		}

		extensionRows = append(extensionRows, extensionListItem{
			Id:               extension.Id,
			Name:             extension.DisplayName,
			Namespace:        extension.Namespace,
			Version:          latestVersion,
			InstalledVersion: installedVersion,
			UpdateAvailable:  updateAvailable && !updateIncompatible,
			Incompatible:     updateIncompatible,
			Source:           extension.Source,
		})
	}

	if len(extensionRows) == 0 {
		if a.flags.installed {
			a.console.Message(ctx, output.WithWarningFormat("WARNING: No extensions installed.\n"))
			a.console.Message(ctx, fmt.Sprintf(
				"Run %s to install extensions.",
				output.WithHighLightFormat("azd extension install <extension-id>"),
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
				Heading:       "Latest Version",
				ValueTemplate: `{{.Version}}`,
			},
			{
				Heading:       "Installed Version",
				ValueTemplate: `{{.InstalledVersion}}{{if .UpdateAvailable}}*{{else if .Incompatible}}!{{end}}`,
			},
			{
				Heading:       "Source",
				ValueTemplate: `{{.Source}}`,
			},
		}

		formatErr = a.formatter.Format(extensionRows, a.writer, output.TableFormatterOptions{
			Columns: columns,
		})

		if formatErr == nil {
			hasCompatibleUpdates := slices.ContainsFunc(extensionRows, func(row extensionListItem) bool {
				return row.UpdateAvailable
			})
			hasIncompatibleUpdates := slices.ContainsFunc(extensionRows, func(row extensionListItem) bool {
				return row.Incompatible
			})

			if hasCompatibleUpdates || hasIncompatibleUpdates {
				a.console.Message(ctx, "")
			}

			if hasCompatibleUpdates {
				a.console.Message(ctx, "(*) Update available")
				a.console.Message(ctx, fmt.Sprintf(
					"    To upgrade: %s", output.WithHighLightFormat("azd extension upgrade <extension-id>")))
				a.console.Message(ctx, fmt.Sprintf(
					"    To upgrade all: %s", output.WithHighLightFormat("azd extension upgrade --all")))
			}

			if hasIncompatibleUpdates {
				if hasCompatibleUpdates {
					a.console.Message(ctx, "")
				}
				a.console.Message(ctx, "(!) Update available but incompatible with current azd version")
			}
		}
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
	Name              string
	Website           string
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
		{"Name", ":", t.Name},
		{"Description", ":", t.Description},
		{"Source", ":", t.Source},
		{"Namespace", ":", t.Namespace},
	}
	if t.Website != "" {
		extensionInfo = append(extensionInfo, []string{"Website", ":", t.Website})
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
		return nil, fmt.Errorf("must specify an extension id")
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
		Name:              registryExtension.DisplayName,
		Website:           registryExtension.Website,
		Source:            registryExtension.Source,
		Namespace:         registryExtension.Namespace,
		Description:       registryExtension.Description,
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
		BoolVarP(&flags.force, "force", "f", false, "Force installation, including downgrades and reinstalls")

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
		return nil, fmt.Errorf("must specify an extension id")
	}

	if len(extensionIds) > 1 && a.flags.version != "" {
		return nil, fmt.Errorf("cannot specify --version flag when using multiple extensions")
	}

	azdVersion := currentAzdSemver()

	for index, extensionId := range extensionIds {
		if index > 0 {
			a.console.Message(ctx, "")
		}

		stepMessage := fmt.Sprintf("Installing %s extension", output.WithHighLightFormat(extensionId))
		a.console.ShowSpinner(ctx, stepMessage, input.Step)

		// Check if extension is already installed
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

		// Check azd version compatibility
		compatibleExtension, compatResult, err := resolveCompatibleExtension(
			selectedExtension, extensionId, a.flags.version, azdVersion,
		)
		if err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, err
		}
		if compatResult != nil && compatResult.HasNewerIncompatible && compatResult.LatestOverall != nil {
			a.console.StopSpinner(ctx, stepMessage, input.Step)
			displayVersionCompatibilityWarning(ctx, a.console,
				compatResult.LatestOverall, compatResult.LatestCompatible, azdVersion,
			)
			a.console.ShowSpinner(ctx, stepMessage, input.Step)
		}

		// Check for namespace conflicts with installed extensions
		if err := checkNamespaceConflict(
			compatibleExtension.Id,
			compatibleExtension.Namespace,
			allInstalled,
		); err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, err
		}

		// Determine target version
		targetVersion := a.flags.version
		if targetVersion == "" || targetVersion == "latest" {
			targetVersion = compatibleExtension.Versions[len(compatibleExtension.Versions)-1].Version
		}

		var extensionVersion *extensions.ExtensionVersion

		if alreadyInstalled {
			// Extension is already installed - apply smart upgrade/downgrade logic

			// Check if same version (regardless of source)
			if installedExtension.Version == targetVersion && !a.flags.force {
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
			extensionVersion, err = a.extensionManager.Upgrade(ctx, compatibleExtension, a.flags.version)
			if err != nil {
				a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return nil, fmt.Errorf("failed to upgrade extension: %w", err)
			}

			stepMessage += output.WithGrayFormat(" (%s)", extensionVersion.Version)
			a.console.StopSpinner(ctx, stepMessage, input.StepDone)

		} else {
			// Extension not installed - proceed with fresh install
			a.console.ShowSpinner(ctx, stepMessage, input.Step)
			extensionVersion, err = a.extensionManager.Install(ctx, compatibleExtension, a.flags.version)
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
		return nil, fmt.Errorf("must specify an extension id or use --all flag")
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
		return nil, fmt.Errorf("must specify an extension id or use --all flag")
	}

	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Upgrade azd extensions (azd extension upgrade)",
		TitleNote: "Upgrades the specified extensions on the local machine",
	})

	azdVersion := currentAzdSemver()

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

		// Check azd version compatibility
		compatibleExtension, compatResult, err := resolveCompatibleExtension(
			selectedExtension, extensionId, a.flags.version, azdVersion,
		)
		if err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, err
		}
		if compatResult != nil && compatResult.HasNewerIncompatible && compatResult.LatestOverall != nil {
			a.console.StopSpinner(ctx, stepMessage, input.Step)
			displayVersionCompatibilityWarning(ctx, a.console,
				compatResult.LatestOverall, compatResult.LatestCompatible, azdVersion,
			)
			a.console.ShowSpinner(ctx, stepMessage, input.Step)
		}

		// Determine the target version for comparison:
		// - If --version is specified, compare against the requested version
		// - Otherwise, compare against the latest compatible version
		var targetVersionStr string
		if a.flags.version != "" && a.flags.version != "latest" {
			targetVersionStr = a.flags.version
		} else {
			targetVersionStr = compatibleExtension.Versions[len(compatibleExtension.Versions)-1].Version
		}

		// Parse semantic versions for proper comparison
		installedSemver, err := semver.NewVersion(installed.Version)
		if err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, fmt.Errorf("failed to parse installed version '%s': %w", installed.Version, err)
		}

		targetSemver, err := semver.NewVersion(targetVersionStr)
		if err != nil {
			a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, fmt.Errorf("failed to parse target version '%s': %w", targetVersionStr, err)
		}

		// Compare versions: skip if installed version >= target version
		if installedSemver.GreaterThan(targetSemver) {
			stepMessage += output.WithGrayFormat(
				" (Installed version %s is newer than %s)",
				installed.Version,
				targetVersionStr,
			)
			a.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
		} else if installedSemver.Equal(targetSemver) {
			stepMessage += output.WithGrayFormat(" (No upgrade available)")
			a.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
		} else {
			extensionVersion, err := a.extensionManager.Upgrade(ctx, compatibleExtension, a.flags.version)
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

// checkNamespaceConflict checks if the given namespace conflicts with any installed extension.
// Two namespaces conflict if one is a prefix of the other (e.g., "ai" and "ai.agent").
func checkNamespaceConflict(
	newExtId string,
	newNamespace string,
	installedExtensions map[string]*extensions.Extension,
) error {
	if newNamespace == "" {
		return nil
	}

	for extId, ext := range installedExtensions {
		if extId == newExtId {
			continue // Skip self (for upgrades)
		}
		if ext.Namespace == "" {
			continue
		}

		if conflict, _ := namespacesConflict(newNamespace, ext.Namespace); conflict {
			return &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"namespace '%s' conflicts with installed extension '%s' (namespace '%s')",
					newNamespace, extId, ext.Namespace,
				),
				Suggestion: fmt.Sprintf(
					"Suggestion: uninstall %s first or choose a different extension.",
					output.WithHighLightFormat(extId),
				),
			}
		}
	}

	return nil
}

// namespacesConflict checks if two namespaces conflict.
// Returns true and a description if they conflict.
// Comparison is case-insensitive.
func namespacesConflict(ns1, ns2 string) (bool, string) {
	// Normalize to lowercase for case-insensitive comparison
	ns1Lower := strings.ToLower(ns1)
	ns2Lower := strings.ToLower(ns2)

	if ns1Lower == ns2Lower {
		return true, "the same namespace"
	}

	// Check if one is a prefix of the other (with dot separator)
	if strings.HasPrefix(ns1Lower, ns2Lower+".") {
		return true, "overlapping namespaces"
	}
	if strings.HasPrefix(ns2Lower, ns1Lower+".") {
		return true, "overlapping namespaces"
	}

	return false, ""
}

// currentAzdSemver returns the current azd version as a Masterminds semver.
// Returns nil for dev builds (0.0.0-dev.0) to skip compatibility checks entirely.
// For PR and daily builds, the prerelease tag is stripped so that constraint
// matching works correctly (semver constraints exclude prerelease versions by default).
// Returns nil if the version cannot be parsed (should not happen in practice).
func currentAzdSemver() *semver.Version {
	if internal.IsDevVersion() {
		return nil
	}
	versionInfo := internal.VersionInfo()
	// Re-parse is required: internal.VersionInfo uses blang/semver while extension
	// compatibility checking uses Masterminds/semver for constraint evaluation.
	v, err := semver.NewVersion(versionInfo.Version.String())
	if err != nil {
		return nil
	}

	// Strip prerelease tags so that PR/daily builds are compared by their base version.
	// This is required because semver constraints like ">= 1.24.0" exclude prerelease versions
	// by design, so "1.24.0-beta.1-pr.5861630" would not satisfy ">= 1.24.0" without stripping.
	if v.Prerelease() != "" {
		stripped, err := semver.NewVersion(fmt.Sprintf("%d.%d.%d", v.Major(), v.Minor(), v.Patch()))
		if err != nil {
			return nil
		}
		return stripped
	}

	return v
}

// resolveCompatibleExtension filters extension versions for azd version compatibility.
// Returns the (possibly filtered) extension metadata and the compatibility result for displaying warnings.
// Returns an error if no compatible versions are found or the specific requested version is incompatible.
func resolveCompatibleExtension(
	selectedExtension *extensions.ExtensionMetadata,
	extensionId string,
	requestedVersion string,
	azdVersion *semver.Version,
) (*extensions.ExtensionMetadata, *extensions.VersionCompatibilityResult, error) {
	if azdVersion == nil {
		return selectedExtension, nil, nil
	}

	if requestedVersion != "" && requestedVersion != "latest" {
		// Validate compatibility for the specific requested version
		if err := validateVersionCompatibility(
			selectedExtension.Versions, requestedVersion, extensionId, azdVersion,
		); err != nil {
			return nil, nil, err
		}
		return selectedExtension, nil, nil
	}

	// Filter versions for azd compatibility when no specific version is requested
	compatResult := extensions.FilterCompatibleVersions(selectedExtension.Versions, azdVersion)

	if len(compatResult.Compatible) == 0 {
		return nil, compatResult, fmt.Errorf(
			"no compatible version of %s found for azd %s",
			extensionId, azdVersion.String(),
		)
	}

	if len(compatResult.Compatible) < len(selectedExtension.Versions) {
		compatCopy := *selectedExtension
		compatCopy.Versions = compatResult.Compatible
		return &compatCopy, compatResult, nil
	}

	return selectedExtension, compatResult, nil
}

// displayVersionCompatibilityWarning prints a warning when the latest version is incompatible
// but an older compatible version is available.
func displayVersionCompatibilityWarning(
	ctx context.Context,
	console input.Console,
	latestOverall *extensions.ExtensionVersion,
	latestCompatible *extensions.ExtensionVersion,
	azdVersion *semver.Version,
) {
	console.Message(ctx, output.WithWarningFormat(
		"   WARNING: %s is incompatible with azd %s (requires %q), installing %s instead.",
		latestOverall.Version,
		azdVersion.String(),
		latestOverall.RequiredAzdVersion,
		latestCompatible.Version,
	))
}

// validateVersionCompatibility checks if a specific requested version is compatible with the current azd version.
// Returns an error if the version is found and is incompatible, nil otherwise.
func validateVersionCompatibility(
	versions []extensions.ExtensionVersion,
	requestedVersion string,
	extensionId string,
	azdVersion *semver.Version,
) error {
	for i := range versions {
		if versions[i].Version == requestedVersion {
			if !extensions.VersionIsCompatible(&versions[i], azdVersion) {
				return fmt.Errorf(
					"%s %s is incompatible with azd %s (requires %q)",
					extensionId,
					versions[i].Version,
					azdVersion.String(),
					versions[i].RequiredAzdVersion,
				)
			}
			break
		}
	}
	return nil
}

// azd extension source validate
type extensionSourceValidateFlags struct {
	strict bool
}

func newExtensionSourceValidateFlags(cmd *cobra.Command) *extensionSourceValidateFlags {
	flags := &extensionSourceValidateFlags{}
	cmd.Flags().BoolVar(&flags.strict, "strict", false, "Enable strict validation (require checksums)")
	return flags
}

type extensionSourceValidateAction struct {
	args          []string
	flags         *extensionSourceValidateFlags
	console       input.Console
	formatter     output.Formatter
	writer        io.Writer
	sourceManager *extensions.SourceManager
}

func newExtensionSourceValidateAction(
	args []string,
	flags *extensionSourceValidateFlags,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	sourceManager *extensions.SourceManager,
) actions.Action {
	return &extensionSourceValidateAction{
		args:          args,
		flags:         flags,
		console:       console,
		formatter:     formatter,
		writer:        writer,
		sourceManager: sourceManager,
	}
}

func (a *extensionSourceValidateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if len(a.args) == 0 {
		return nil, fmt.Errorf("must specify a source name, file path, or URL")
	}
	if len(a.args) > 1 {
		return nil, fmt.Errorf("cannot specify multiple sources")
	}

	arg := a.args[0]

	// Resolve the source: try as named source first, then as direct path/URL
	sourceConfig, err := a.sourceManager.Get(ctx, arg)
	if err != nil && !errors.Is(err, extensions.ErrSourceNotFound) {
		return nil, fmt.Errorf("failed to get source %q: %w", arg, err)
	}
	if err != nil {
		// Not a named source — auto-detect type from the argument
		kind := extensions.SourceKindFile
		if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
			kind = extensions.SourceKindUrl
		}
		sourceConfig = &extensions.SourceConfig{
			Name:     "validate",
			Type:     kind,
			Location: arg,
		}
	}

	source, err := a.sourceManager.CreateSource(ctx, sourceConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to load source: %w", err)
	}

	extensionList, err := source.ListExtensions(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list extensions: %w", err)
	}

	if len(extensionList) == 0 {
		return nil, fmt.Errorf("source contains no extensions")
	}

	result := extensions.ValidateExtensions(extensionList, a.flags.strict)

	if a.formatter.Kind() == output.JsonFormat {
		if err := a.formatter.Format(result, a.writer, nil); err != nil {
			return nil, err
		}
	} else {
		displayValidationResult(a.console, ctx, result)
	}

	if !result.Valid {
		return nil, fmt.Errorf("validation failed: one or more extensions have errors")
	}

	return nil, nil
}

func displayValidationResult(console input.Console, ctx context.Context, result *extensions.RegistryValidationResult) {
	for _, ext := range result.Extensions {
		id := ext.Id
		if id == "" {
			id = "(unknown)"
		}

		if ext.Valid {
			console.Message(ctx, fmt.Sprintf("  %s %s", output.WithSuccessFormat("✓"), id))
		} else {
			console.Message(ctx, fmt.Sprintf("  %s %s", output.WithErrorFormat("✗"), id))
		}

		if ext.LatestVersion != "" {
			console.Message(ctx, fmt.Sprintf("    Version: %s", ext.LatestVersion))
		}

		for _, issue := range ext.Issues {
			if issue.Severity == extensions.ValidationError {
				console.Message(ctx, fmt.Sprintf("    %s %s",
					output.WithErrorFormat("ERROR:"), issue.Message))
			} else {
				console.Message(ctx, fmt.Sprintf("    %s %s",
					output.WithWarningFormat("WARNING:"), issue.Message))
			}
		}
	}

	console.Message(ctx, "")
	if result.Valid {
		console.Message(ctx, output.WithSuccessFormat("Registry validation passed."))
	} else {
		console.Message(ctx, output.WithErrorFormat("Registry validation failed."))
	}
}
