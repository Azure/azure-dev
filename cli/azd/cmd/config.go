package cmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
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

	groupCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage the Azure Developer CLI user configuration.",
		Long:  longDescription,
	}

	group := root.Add("config", &actions.ActionDescriptorOptions{
		Command: groupCmd,
	})

	group.Add("list", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short: "Lists all configuration values",
			Long:  `Lists all configuration values in ` + userConfigPath + `.`,
		},
		ActionResolver: newConfigListAction,
		OutputFormats:  []output.Format{output.JsonFormat},
		DefaultFormat:  output.JsonFormat,
	})

	group.Add("get", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "get <path>",
			Short: "Gets a configuration",
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
			Short: "Sets a configuration",
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
			Short:   "Unsets a configuration",
			Long:    `Removes a configuration in ` + userConfigPath + `.`,
			Example: `$ azd config unset defaults.location`,
			Args:    cobra.ExactArgs(1),
		},
		ActionResolver: newConfigUnsetAction,
	})

	group.Add("reset", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short: "Resets configuration to default",
			Long:  `Resets all configuration in ` + userConfigPath + ` to the default.`,
		},
		ActionResolver: newConfigResetAction,
	})

	return group
}

// azd config list

type configListAction struct {
	configManager config.UserConfigManager
	formatter     output.Formatter
	writer        io.Writer
}

func newConfigListAction(
	configManager config.UserConfigManager, formatter output.Formatter, writer io.Writer,
) actions.Action {
	return &configListAction{
		configManager: configManager,
		formatter:     formatter,
		writer:        writer,
	}
}

// Executes the `azd config list` action
func (a *configListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
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

type configResetAction struct {
	configManager config.UserConfigManager
	args          []string
}

func newConfigResetAction(configManager config.UserConfigManager, args []string) actions.Action {
	return &configResetAction{
		configManager: configManager,
		args:          args,
	}
}

// Executes the `azd config reset` action
func (a *configResetAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	emptyConfig := config.NewConfig(nil)
	return nil, a.configManager.Save(emptyConfig)
}
