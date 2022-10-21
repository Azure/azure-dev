package cmd

import (
	"context"
	"fmt"
	"io"

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

	root.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", root.Name()))

	return root
}

func configListCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *struct{}) {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Lists all configuration values",
	}

	output.AddOutputParam(
		cmd,
		[]output.Format{output.JsonFormat},
		output.JsonFormat,
	)

	return cmd, &struct{}{}
}

func configGetCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *struct{}) {
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Gets a configuration",
	}

	output.AddOutputParam(
		cmd,
		[]output.Format{output.JsonFormat},
		output.JsonFormat,
	)

	cmd.Args = cobra.ExactArgs(1)
	return cmd, &struct{}{}
}

func configSetCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *struct{}) {
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Sets a configuration",
	}
	cmd.Args = cobra.ExactArgs(2)
	return cmd, &struct{}{}
}

func configUnsetCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *struct{}) {
	cmd := &cobra.Command{
		Use:   "unset <key>",
		Short: "Unsets a configuration",
	}
	cmd.Args = cobra.ExactArgs(1)
	return cmd, &struct{}{}
}

type configListAction struct {
	config    config.Config
	formatter output.Formatter
	writer    io.Writer
}

func newConfigListAction(config config.Config, formatter output.Formatter, writer io.Writer) *configListAction {
	return &configListAction{
		config:    config,
		formatter: formatter,
		writer:    writer,
	}
}

func (a *configListAction) Run(ctx context.Context) error {
	values := a.config.Raw()

	if a.formatter.Kind() == output.JsonFormat {
		err := a.formatter.Format(values, a.writer, nil)
		if err != nil {
			return fmt.Errorf("failing formatting config values: %w", err)
		}
	}

	return nil
}

type configGetAction struct {
	config    config.Config
	formatter output.Formatter
	writer    io.Writer
	args      []string
}

func newConfigGetAction(config config.Config, formatter output.Formatter, writer io.Writer, args []string) *configGetAction {
	return &configGetAction{
		config:    config,
		formatter: formatter,
		writer:    writer,
		args:      args,
	}
}

func (a *configGetAction) Run(ctx context.Context) error {
	key := a.args[0]
	value, ok := a.config.Get(key)

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

type configSetAction struct {
	config config.Config
	args   []string
}

func newConfigSetAction(config config.Config, args []string) *configSetAction {
	return &configSetAction{
		config: config,
		args:   args,
	}
}

func (a *configSetAction) Run(ctx context.Context) error {
	path := a.args[0]
	value := a.args[1]

	err := a.config.Set(path, value)
	if err != nil {
		return fmt.Errorf("failed setting configuration value '%s' to '%s'. %w", path, value, err)
	}

	err = a.config.Save()
	if err != nil {
		return fmt.Errorf("failed saving configuration. %w", err)
	}

	return nil
}

type configUnsetAction struct {
	config config.Config
	args   []string
}

func newConfigUnsetAction(config config.Config, args []string) *configUnsetAction {
	return &configUnsetAction{
		config: config,
		args:   args,
	}
}

func (a *configUnsetAction) Run(ctx context.Context) error {
	path := a.args[0]

	err := a.config.Unset(path)
	if err != nil {
		return fmt.Errorf("failed removing configuration with path '%s'. %w", path, err)
	}

	err = a.config.Save()
	if err != nil {
		return fmt.Errorf("failed saving configuration. %w", err)
	}

	return nil
}
