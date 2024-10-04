package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
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
			Use:   "extension",
			Short: "Manage azd extensions.",
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
			Short: "Install an extension.",
			Args:  cobra.ExactArgs(1),
		},
		ActionResolver: newExtensionInstallAction,
		FlagsResolver:  newExtensionInstallFlags,
	})

	// azd extension uninstall <extension-name>
	group.Add("uninstall", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "uninstall <extension-name>",
			Short: "Uninstall an extension.",
			Args:  cobra.ExactArgs(1),
		},
		ActionResolver: newExtensionUninstallAction,
	})

	// azd extension upgrade <extension-name>
	group.Add("upgrade", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "upgrade <extension-name>",
			Short: "Upgrade an installed extension.",
			Args:  cobra.MaximumNArgs(1),
		},
		ActionResolver: newExtensionUpgradeAction,
		FlagsResolver:  newExtensionUpgradeFlags,
	})

	return group
}

type extensionListFlags struct {
	installed bool
}

func newExtensionListFlags(cmd *cobra.Command) *extensionListFlags {
	flags := &extensionListFlags{}
	cmd.Flags().BoolVar(&flags.installed, "installed", false, "List installed extensions")

	return flags
}

// azd extension list [--installed]
type extensionListAction struct {
	flags            *extensionListFlags
	formatter        output.Formatter
	writer           io.Writer
	extensionManager *extensions.Manager
}

func newExtensionListAction(
	flags *extensionListFlags,
	formatter output.Formatter,
	writer io.Writer,
	extensionManager *extensions.Manager,
) actions.Action {
	return &extensionListAction{
		flags:            flags,
		formatter:        formatter,
		writer:           writer,
		extensionManager: extensionManager,
	}
}

type extensionListItem struct {
	Name        string
	Description string
	Version     string
	Installed   bool
}

func (a *extensionListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	registryExtensions, err := a.extensionManager.ListFromRegistry(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed listing extensions from registry: %w", err)
	}

	installedExtensions, err := a.extensionManager.ListInstalled()
	if err != nil {
		return nil, fmt.Errorf("failed listing installed extensions: %w", err)
	}

	extensionRows := []extensionListItem{}

	for _, extension := range registryExtensions {
		installedExtension, installed := installedExtensions[extension.Name]
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
			Name:        extension.Name,
			Version:     version,
			Description: extension.DisplayName,
			Installed:   installedExtensions[extension.Name] != nil,
		})
	}

	var formatErr error

	if a.formatter.Kind() == output.TableFormat {
		columns := []output.Column{
			{
				Heading:       "Name",
				ValueTemplate: `{{.Name}}`,
			},
			{
				Heading:       "Description",
				ValueTemplate: "{{.Description}}",
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
	Examples         []string
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
		text = append(text, []string{"", "", example})
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
	extensionName := a.args[0]
	registryExtension, err := a.extensionManager.GetFromRegistry(ctx, extensionName)
	if err != nil {
		return nil, fmt.Errorf("failed to get extension details: %w", err)
	}

	latestVersion := registryExtension.Versions[len(registryExtension.Versions)-1]

	extensionDetails := extensionShowItem{
		Name:             registryExtension.Name,
		Description:      registryExtension.DisplayName,
		LatestVersion:    latestVersion.Version,
		Usage:            latestVersion.Usage,
		Examples:         latestVersion.Examples,
		InstalledVersion: "N/A",
	}

	installedExtension, err := a.extensionManager.GetInstalled(extensionName)
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

	extensionName := a.args[0]

	stepMessage := fmt.Sprintf("Installing extension %s", extensionName)
	a.console.ShowSpinner(ctx, stepMessage, input.Step)

	extensionVersion, err := a.extensionManager.Install(ctx, extensionName, a.flags.version)
	if err != nil {
		a.console.StopSpinner(ctx, stepMessage, input.StepFailed)

		if errors.Is(err, extensions.ErrExtensionInstalled) {
			return nil, &internal.ErrorWithSuggestion{
				Err:        err,
				Suggestion: fmt.Sprint("Run 'azd extension upgrade ", extensionName, "' to upgrade the extension."),
			}
		}

		return nil, fmt.Errorf("failed to install extension: %w", err)
	}

	stepMessage += fmt.Sprintf(" (%s)", extensionVersion.Version)
	a.console.StopSpinner(ctx, stepMessage, input.StepDone)

	lines := []string{
		fmt.Sprintf("Usage: %s", extensionVersion.Usage),
		"\nExamples:",
	}

	lines = append(lines, extensionVersion.Examples...)

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   "Extension installed successfully",
			FollowUp: strings.Join(lines, "\n"),
		},
	}, nil
}

// azd extension uninstall
type extensionUninstallAction struct {
	args             []string
	console          input.Console
	extensionManager *extensions.Manager
}

func newExtensionUninstallAction(
	args []string,
	console input.Console,
	extensionManager *extensions.Manager,
) actions.Action {
	return &extensionUninstallAction{
		args:             args,
		console:          console,
		extensionManager: extensionManager,
	}
}

func (a *extensionUninstallAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Uninstall an azd extension (azd extension uninstall)",
		TitleNote: "Uninstalls the specified extension from the local machine",
	})

	extensionName := a.args[0]
	stepMessage := fmt.Sprintf("Uninstalling extension %s", extensionName)

	installed, err := a.extensionManager.GetInstalled(extensionName)
	if err != nil {
		a.console.ShowSpinner(ctx, stepMessage, input.Step)
		a.console.StopSpinner(ctx, stepMessage, input.StepFailed)

		return nil, fmt.Errorf("failed to get installed extension: %w", err)
	}

	stepMessage += fmt.Sprintf(" (%s)", installed.Version)
	a.console.ShowSpinner(ctx, stepMessage, input.Step)

	if err := a.extensionManager.Uninstall(extensionName); err != nil {
		a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
		return nil, fmt.Errorf("failed to uninstall extension: %w", err)
	}

	a.console.StopSpinner(ctx, stepMessage, input.StepDone)

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Extension uninstalled successfully",
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
	extensionName := ""
	if len(a.args) > 0 {
		extensionName = a.args[0]
	}

	if extensionName != "" && a.flags.all {
		return nil, fmt.Errorf("cannot specify both an extension name and --all flag")
	}

	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Upgrade azd extensions (azd extension upgrade)",
		TitleNote: "Upgrades the specified extensions on the local machine",
	})

	if extensionName != "" {
		stepMessage := fmt.Sprintf("Upgrading extension %s", extensionName)
		a.console.ShowSpinner(ctx, stepMessage, input.Step)

		extensionVersion, err := a.extensionManager.Upgrade(ctx, extensionName, a.flags.version)
		if err != nil {
			return nil, fmt.Errorf("failed to upgrade extension: %w", err)
		}

		stepMessage += fmt.Sprintf(" (%s)", extensionVersion.Version)
		a.console.StopSpinner(ctx, stepMessage, input.StepDone)

		lines := []string{
			fmt.Sprintf("%s %s", output.WithBold("Usage: "), extensionVersion.Usage),
			output.WithBold("\nExamples:"),
		}

		for _, example := range extensionVersion.Examples {
			lines = append(lines, "  "+output.WithHighLightFormat(example))
		}

		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header:   "Extension upgraded successfully",
				FollowUp: strings.Join(lines, "\n"),
			},
		}, nil
	} else {
		installed, err := a.extensionManager.ListInstalled()
		if err != nil {
			return nil, fmt.Errorf("failed to list installed extensions: %w", err)
		}

		for name := range installed {
			stepMessage := fmt.Sprintf("Upgrading extension %s", name)
			a.console.ShowSpinner(ctx, stepMessage, input.Step)

			extensionVersion, err := a.extensionManager.Upgrade(ctx, name, "")
			if err != nil {
				a.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return nil, fmt.Errorf("failed to upgrade extension %s: %w", name, err)
			}

			stepMessage += fmt.Sprintf(" (%s)", extensionVersion.Version)
			a.console.StopSpinner(ctx, stepMessage, input.StepDone)
		}

		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header: "All extensions upgraded successfully",
			},
		}, nil
	}
}
