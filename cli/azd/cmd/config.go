package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

// Setup account command category
func configCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	root := &cobra.Command{
		Use:   "config",
		Short: "Manage Azure Developer CLI configuration",
	}

	root.AddCommand(BuildCmd(rootOptions, configListCmdDesign, initConfigListAction, nil))
	root.AddCommand(BuildCmd(rootOptions, configGetCmdDesign, initConfigGetAction, nil))
	root.AddCommand(BuildCmd(rootOptions, configSetCmdDesign, initConfigSetAction, nil))
	root.AddCommand(BuildCmd(rootOptions, configUnsetCmdDesign, initConfigUnsetAction, nil))
	root.AddCommand(BuildCmd(rootOptions, configResetCmdDesign, initConfigResetAction, nil))

	root.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", root.Name()))

	return root
}

// azd config list

func configListCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *struct{}) {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Lists all configuration values",
		Long:  "Lists all configuration values",
	}

	output.AddOutputParam(
		cmd,
		[]output.Format{output.JsonFormat},
		output.JsonFormat,
	)

	return cmd, &struct{}{}
}

type configListAction struct {
	configManager config.Manager
	formatter     output.Formatter
	writer        io.Writer
}

func newConfigListAction(configManager config.Manager, formatter output.Formatter, writer io.Writer) *configListAction {
	return &configListAction{
		configManager: configManager,
		formatter:     formatter,
		writer:        writer,
	}
}

// Executes the `azd config list` action
func (a *configListAction) Run(ctx context.Context) error {
	azdConfig, err := getUserConfig(a.configManager)
	if err != nil {
		return err
	}

	values := azdConfig.Raw()

	if a.formatter.Kind() == output.JsonFormat {
		err := a.formatter.Format(values, a.writer, nil)
		if err != nil {
			return fmt.Errorf("failing formatting config values: %w", err)
		}
	}

	return nil
}

// azd config get <path>

func configGetCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *struct{}) {
	cmd := &cobra.Command{
		Use:   "get <path>",
		Short: "Gets a configuration",
		Long:  "Gets a configuration",
	}

	output.AddOutputParam(
		cmd,
		[]output.Format{output.JsonFormat},
		output.JsonFormat,
	)

	cmd.Args = cobra.ExactArgs(1)
	return cmd, &struct{}{}
}

type configGetAction struct {
	configManager config.Manager
	formatter     output.Formatter
	writer        io.Writer
	args          []string
}

func newConfigGetAction(
	configManager config.Manager,
	formatter output.Formatter,
	writer io.Writer,
	args []string,
) *configGetAction {
	return &configGetAction{
		configManager: configManager,
		formatter:     formatter,
		writer:        writer,
		args:          args,
	}
}

// Executes the `azd config get <path>` action
func (a *configGetAction) Run(ctx context.Context) error {
	azdConfig, err := getUserConfig(a.configManager)
	if err != nil {
		return err
	}

	key := a.args[0]
	value, ok := azdConfig.Get(key)

	if !ok {
		return fmt.Errorf("no value stored at path '%s'", key)
	}

	if a.formatter.Kind() == output.JsonFormat {
		err := a.formatter.Format(value, a.writer, nil)
		if err != nil {
			return fmt.Errorf("failing formatting config values: %w", err)
		}
	}

	return nil
}

// azd config set <path> <value>

func configSetCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *struct{}) {
	cmd := &cobra.Command{
		Use:   "set <path> <value>",
		Short: "Sets a configuration",
		Long:  "Sets a configuration",
	}
	cmd.Args = cobra.ExactArgs(2)
	return cmd, &struct{}{}
}

type configSetAction struct {
	configManager config.Manager
	args          []string
}

func newConfigSetAction(configManager config.Manager, args []string) *configSetAction {
	return &configSetAction{
		configManager: configManager,
		args:          args,
	}
}

// Executes the `azd config set <path> <value>` action
func (a *configSetAction) Run(ctx context.Context) error {
	azdConfig, err := getUserConfig(a.configManager)
	if err != nil {
		return err
	}

	path := a.args[0]
	value := a.args[1]

	err = azdConfig.Set(path, value)
	if err != nil {
		return fmt.Errorf("failed setting configuration value '%s' to '%s'. %w", path, value, err)
	}

	userConfigFilePath, err := config.GetUserConfigFilePath()
	if err != nil {
		return fmt.Errorf("failed getting user config file path. %w", err)
	}

	err = a.configManager.Save(azdConfig, userConfigFilePath)
	if err != nil {
		return fmt.Errorf("failed saving configuration. %w", err)
	}

	return nil
}

// azd config unset <path>

func configUnsetCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *struct{}) {
	cmd := &cobra.Command{
		Use:   "unset <path>",
		Short: "Unsets a configuration",
		Long:  "Unsets a configuration",
	}
	cmd.Args = cobra.ExactArgs(1)
	return cmd, &struct{}{}
}

type configUnsetAction struct {
	configManager config.Manager
	args          []string
}

func newConfigUnsetAction(configManager config.Manager, args []string) *configUnsetAction {
	return &configUnsetAction{
		configManager: configManager,
		args:          args,
	}
}

// Executes the `azd config unset <path>` action
func (a *configUnsetAction) Run(ctx context.Context) error {
	azdConfig, err := getUserConfig(a.configManager)
	if err != nil {
		return err
	}

	path := a.args[0]

	err = azdConfig.Unset(path)
	if err != nil {
		return fmt.Errorf("failed removing configuration with path '%s'. %w", path, err)
	}

	userConfigFilePath, err := config.GetUserConfigFilePath()
	if err != nil {
		return fmt.Errorf("failed getting user config file path. %w", err)
	}

	err = a.configManager.Save(azdConfig, userConfigFilePath)
	if err != nil {
		return fmt.Errorf("failed saving configuration. %w", err)
	}

	return nil
}

// azd config reset

func configResetCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *struct{}) {
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Resets configuration to default",
		Long:  "Resets configuration to default",
	}

	return cmd, &struct{}{}
}

type configResetAction struct {
	configManager config.Manager
	args          []string
}

func newConfigResetAction(configManager config.Manager, args []string) *configResetAction {
	return &configResetAction{
		configManager: configManager,
		args:          args,
	}
}

// Executes the `azd config reset` action
func (a *configResetAction) Run(ctx context.Context) error {
	userConfigFilePath, err := config.GetUserConfigFilePath()
	if err != nil {
		return fmt.Errorf("failed getting user config file path. %w", err)
	}

	emptyConfig := config.NewConfig(nil)

	err = a.configManager.Save(emptyConfig, userConfigFilePath)
	if err != nil {
		return fmt.Errorf("failed saving configuration. %w", err)
	}

	return nil
}

func getUserConfig(configManager config.Manager) (config.Config, error) {
	var azdConfig config.Config

	configFilePath, err := config.GetUserConfigFilePath()
	if err != nil {
		return nil, err
	}

	azdConfig, err = configManager.Load(configFilePath)
	if err != nil {
		// Ignore missing file errors
		// File will automatically be created on first `set` operation
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("failed loading azd user config from '%s'. %s\n", configFilePath, err.Error())
			return config.NewConfig(nil), nil
		}

		return nil, fmt.Errorf("failed loading azd user config from '%s'. %w", configFilePath, err)
	}

	return azdConfig, nil
}
