package cmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/spf13/cobra"
)

var userConfigPath string

// Setup account command category
func configActions(root *actions.ActionDescriptor, rootOptions *internal.GlobalCommandOptions) *actions.ActionDescriptor {
	userConfigDir, err := config.GetUserConfigDir()
	if rootOptions.GenerateStaticHelp {
		userConfigPath = heredoc.Doc(`the configuration path.

		The default value of the config directory is:
		* ` + output.WithBackticks(`$HOME/.azd`) + ` on Linux and macOS
		* ` + output.WithBackticks(`%USERPROFILE%\.azd`) + ` on Windows

		The configuration directory can be overridden by specifying a path in the AZD_CONFIG_DIR environment variable`)
	} else if err != nil {
		userConfigPath = output.WithBackticks(filepath.Join("$AZURE_CONFIG_DIR", "config.json"))
	} else {
		userConfigPath = output.WithBackticks(filepath.Join(userConfigDir, "config.json"))
	}

	var defaultConfigPath string
	if runtime.GOOS == "windows" {
		defaultConfigPath = filepath.Join("%USERPROFILE%", ".azd")
	} else {
		defaultConfigPath = filepath.Join("$HOME", ".azd")
	}

	var helpConfigPaths string
	if rootOptions.GenerateStaticHelp {
		//nolint:lll
		helpConfigPaths = heredoc.Doc(`
		Available since ` + output.WithBackticks("azure-dev-cli_0.4.0-beta.1") + `.

		The easiest way to configure ` + output.WithBackticks("azd") + ` for the first time is to run [` + output.WithBackticks("azd init") + `](#azd-init). The subscription and location you select will be stored in the ` + output.WithBackticks("config.json") + ` file located in the config directory. To configure ` + output.WithBackticks("azd") + ` anytime afterwards, you'll use [` + output.WithBackticks("azd config set") + `](#azd-config-set).

		The default value of the config directory is: 
		* $HOME/.azd on Linux and macOS
		* %USERPROFILE%\.azd on Windows
		`)
	} else {
		helpConfigPaths = heredoc.Docf(`
		The easiest way to initially configure azd is to run %s.
		The subscription and location you select will be stored at %s.
		The default configuration path is %s.`,
			output.WithBackticks("azd init"),
			userConfigPath,
			output.WithBackticks(defaultConfigPath))
	}

	longDescription := heredoc.Docf(`
		Manage the Azure Developer CLI user configuration, which includes your default Azure subscription and location.

		%s

		The configuration directory can be overridden by specifying a path in the AZD_CONFIG_DIR environment variable.`,
		helpConfigPaths)

	group := root.Add("config", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "config",
			Short: "Manage azd configurations (ex: default Azure subscription, location).",
			Long:  longDescription,
		},
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdConfigHelpDescription,
			Footer:      getCmdConfigHelpFooter,
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupConfig,
		},
	})

	group.Add("show", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short: "Show all the configuration values.",
			Long:  `Show all configuration values in ` + userConfigPath + `.`,
		},
		ActionResolver: newConfigShowAction,
		OutputFormats:  []output.Format{output.JsonFormat},
		DefaultFormat:  output.JsonFormat,
	})

	group.Add("list", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short:  "List all the configuration values. (Deprecated. Use azd config show)",
			Hidden: true,
		},
		ActionResolver: newConfigListAction,
		OutputFormats:  []output.Format{output.JsonFormat},
		DefaultFormat:  output.JsonFormat,
	})

	group.Add("get", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "get <path>",
			Short: "Gets a configuration.",
			Long:  `Gets a configuration in ` + userConfigPath + `.`,
			Args:  cobra.ExactArgs(1),
		},
		ActionResolver: newConfigGetAction,
		OutputFormats:  []output.Format{output.JsonFormat},
		DefaultFormat:  output.JsonFormat,
	})

	group.Add("set", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "set <path> <value>",
			Short: "Sets a configuration.",
			Long:  `Sets a configuration in ` + userConfigPath + `.`,
			Args:  cobra.ExactArgs(2),
			Example: `$ azd config set defaults.subscription <yourSubscriptionID>
$ azd config set defaults.location eastus`,
		},
		ActionResolver: newConfigSetAction,
	})

	group.Add("unset", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:     "unset <path>",
			Short:   "Unsets a configuration.",
			Long:    `Removes a configuration in ` + userConfigPath + `.`,
			Example: `$ azd config unset defaults.location`,
			Args:    cobra.ExactArgs(1),
		},
		ActionResolver: newConfigUnsetAction,
	})

	group.Add("reset", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short: "Resets configuration to default.",
			Long:  `Resets all configuration in ` + userConfigPath + ` to the default.`,
		},
		ActionResolver: newConfigResetAction,
		FlagsResolver:  newConfigResetFlags,
	})

	group.Add("list-alpha", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short: "Display the list of available features in alpha stage.",
		},
		HelpOptions: actions.ActionHelpOptions{
			Footer: getCmdListAlphaHelpFooter,
		},
		ActionResolver: newConfigListAlphaAction,
	})

	return group
}

// azd config show

type configShowAction struct {
	configManager config.UserConfigManager
	formatter     output.Formatter
	writer        io.Writer
}

func newConfigShowAction(
	configManager config.UserConfigManager, formatter output.Formatter, writer io.Writer,
) actions.Action {
	return &configShowAction{
		configManager: configManager,
		formatter:     formatter,
		writer:        writer,
	}
}

// Executes the `azd config show` action
func (a *configShowAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	azdConfig, err := a.configManager.Load()
	if err != nil {
		return nil, err
	}

	values := azdConfig.Raw()

	if a.formatter.Kind() == output.JsonFormat {
		err := a.formatter.Format(values, a.writer, nil)
		if err != nil {
			return nil, fmt.Errorf("failing formatting config values: %w", err)
		}
	}

	return nil, nil
}

// azd config list - Deprecated

type configListAction struct {
	configShow *configShowAction
	console    input.Console
}

func newConfigListAction(
	console input.Console, configShow *configShowAction,
) actions.Action {
	return &configListAction{
		configShow: configShow,
		console:    console,
	}
}

func (a *configListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	fmt.Fprintln(
		a.console.Handles().Stderr,
		output.WithWarningFormat(
			"WARNING: `azd config list` is deprecated and will be removed in a future release."))
	fmt.Fprintln(
		a.console.Handles().Stderr,
		"Next time use `azd config show`")
	return a.configShow.Run(ctx)
}

// azd config get <path>

type configGetAction struct {
	configManager config.UserConfigManager
	formatter     output.Formatter
	writer        io.Writer
	args          []string
}

func newConfigGetAction(
	configManager config.UserConfigManager,
	formatter output.Formatter,
	writer io.Writer,
	args []string,
) actions.Action {
	return &configGetAction{
		configManager: configManager,
		formatter:     formatter,
		writer:        writer,
		args:          args,
	}
}

// Executes the `azd config get <path>` action
func (a *configGetAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	azdConfig, err := a.configManager.Load()
	if err != nil {
		return nil, err
	}

	key := a.args[0]
	value, ok := azdConfig.Get(key)

	if !ok {
		return nil, fmt.Errorf("no value stored at path '%s'", key)
	}

	if a.formatter.Kind() == output.JsonFormat {
		err := a.formatter.Format(value, a.writer, nil)
		if err != nil {
			return nil, fmt.Errorf("failing formatting config values: %w", err)
		}
	}

	return nil, nil
}

// azd config set <path> <value>

type configSetAction struct {
	configManager config.UserConfigManager
	args          []string
}

func newConfigSetAction(configManager config.UserConfigManager, args []string) actions.Action {
	return &configSetAction{
		configManager: configManager,
		args:          args,
	}
}

// Executes the `azd config set <path> <value>` action
func (a *configSetAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	azdConfig, err := a.configManager.Load()
	if err != nil {
		return nil, err
	}

	path := a.args[0]
	value := a.args[1]

	err = azdConfig.Set(path, value)
	if err != nil {
		return nil, fmt.Errorf("failed setting configuration value '%s' to '%s'. %w", path, value, err)
	}

	return nil, a.configManager.Save(azdConfig)
}

// azd config unset <path>

type configUnsetAction struct {
	configManager config.UserConfigManager
	args          []string
}

func newConfigUnsetAction(configManager config.UserConfigManager, args []string) actions.Action {
	return &configUnsetAction{
		configManager: configManager,
		args:          args,
	}
}

// Executes the `azd config unset <path>` action
func (a *configUnsetAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	azdConfig, err := a.configManager.Load()
	if err != nil {
		return nil, err
	}

	path := a.args[0]

	err = azdConfig.Unset(path)
	if err != nil {
		return nil, fmt.Errorf("failed removing configuration with path '%s'. %w", path, err)
	}

	return nil, a.configManager.Save(azdConfig)
}

// azd config reset

type configResetActionFlags struct {
	force bool
}

func newConfigResetFlags(cmd *cobra.Command) *configResetActionFlags {
	flags := &configResetActionFlags{}
	cmd.Flags().BoolVarP(&flags.force, "force", "f", false, "Force reset without confirmation.")

	return flags
}

type configResetAction struct {
	console       input.Console
	configManager config.UserConfigManager
	flags         *configResetActionFlags
	args          []string
}

func newConfigResetAction(
	console input.Console,
	configManager config.UserConfigManager,
	flags *configResetActionFlags, args []string,
) actions.Action {
	return &configResetAction{
		console:       console,
		configManager: configManager,
		flags:         flags,
		args:          args,
	}
}

// Executes the `azd config reset` action
func (a *configResetAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Reset configuration (azd config reset)",
	})

	spinnerMessage := "Resetting azd configuration"
	a.console.ShowSpinner(ctx, spinnerMessage, input.Step)

	if !a.flags.force {
		// nolint:lll
		warningMessage := "WARNING: Resetting azd configuration will remove all stored values including defaults, feature flags and custom template sources.\n\n"
		a.console.Message(ctx, output.WithWarningFormat(warningMessage))

		confirm, err := a.console.Confirm(ctx, input.ConsoleOptions{
			Message:      "Continue with reset?",
			DefaultValue: false,
		})

		if !confirm || err != nil {
			a.console.StopSpinner(ctx, spinnerMessage, input.StepSkipped)
			if err != nil {
				return nil, fmt.Errorf("user cancelled reset confirmation, %w", err)
			}
			return nil, nil
		}
	}

	err := a.configManager.Save(config.NewEmptyConfig())
	a.console.StopSpinner(ctx, spinnerMessage, input.GetStepResultFormat(err))
	if err != nil {
		return nil, err
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Configuration reset",
		},
	}, nil
}

func getCmdConfigHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		"Manage the Azure Developer CLI user configuration.",
		[]string{
			formatHelpNote(fmt.Sprintf("The default configuration path is: %s.",
				output.WithLinkFormat("%HOME/.azd"),
			)),
			formatHelpNote(fmt.Sprintf("The configuration directory can be overridden by specifying a path"+
				" in the %s environment variable.", output.WithBold("AZD_CONFIG_DIR"),
			)),
			formatHelpNote(fmt.Sprintf(
				"The default values for azd prompts like subscription and location are stored with the key: %s.",
				output.WithLinkFormat("defaults"),
			)),
		})
}

func getCmdConfigHelpFooter(c *cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Set the default Azure subscription.": fmt.Sprintf("%s %s",
			output.WithHighLightFormat("azd config set defaults.subscription"),
			output.WithWarningFormat("<yourSubscriptionID>")),
		"Set the default Azure deployment location.": fmt.Sprintf("%s %s",
			output.WithHighLightFormat("azd config set defaults.location"),
			output.WithWarningFormat("<location>")),
	})
}

type configListAlphaAction struct {
	alphaFeaturesManager *alpha.FeatureManager
	console              input.Console
	args                 []string
}

func (a *configListAlphaAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	features, err := a.alphaFeaturesManager.ListFeatures()
	if err != nil {
		return nil, err
	}
	var alphaOutput []string
	for _, alphaFeature := range features {
		alphaOutput = append(alphaOutput,
			strings.Join(
				[]string{
					fmt.Sprintf("Name: %s", alphaFeature.Id),
					fmt.Sprintf("Description: %s", alphaFeature.Description),
					fmt.Sprintf("Status: %s", alphaFeature.Status),
				},
				"\n",
			))
	}
	a.console.Message(ctx, strings.Join(alphaOutput, "\n\n"))

	// No UX output
	return nil, nil
}

func newConfigListAlphaAction(
	alphaFeaturesManager *alpha.FeatureManager,
	console input.Console,
	args []string) actions.Action {
	return &configListAlphaAction{
		alphaFeaturesManager: alphaFeaturesManager,
		console:              console,
		args:                 args,
	}
}

func getCmdListAlphaHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Displays a list of all available features in the alpha stage": output.WithHighLightFormat(
			"azd config list-alpha",
		),
		"Turn on a specific alpha feature": output.WithHighLightFormat(
			"azd config set alpha.<feature-name> on",
		),
		"Turn off a specific alpha feature": output.WithHighLightFormat(
			"azd config set alpha.<feature-name> off",
		),
		"Turn on all alpha features": output.WithHighLightFormat(
			"azd config set alpha.all on",
		),
		"Turn off all alpha features": output.WithHighLightFormat(
			"azd config set alpha.all off",
		),
	})
}
