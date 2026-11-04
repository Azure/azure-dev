// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"

	"azure.ai.content-understanding/internal/config"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newConfigCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	group := &cobra.Command{
		Use: "config", Short: "Manage CU user defaults without an azd project",
		Long: "Manage CU defaults in azd user configuration under extensions.ai-content-understanding.\n" +
			"Uses the same store as azd config, without requiring an azd project or sign-in.\n" +
			"Supported names: CU_ENDPOINT, CU_DEFAULT_ANALYZER, CU_API_VERSION (case-sensitive).\n" +
			"These commands show stored user defaults, not effective settings; use doctor for effective values and sources.",
	}
	for _, spec := range []struct {
		name, use, short, example string
		args                      cobra.PositionalArgs
	}{
		{"set", "set <name> <value>", "Validate and save one CU user default",
			"  azd ai cu config set CU_ENDPOINT https://resource.services.ai.azure.com/\n" +
				"    Save a user default endpoint for use from any directory.", cobra.ExactArgs(2)},
		{"get", "get <name>", "Show one stored CU user default",
			"  azd ai cu config get CU_ENDPOINT\n" +
				"    Show the stored endpoint without applying overrides.", cobra.ExactArgs(1)},
		{"list", "list", "List stored CU user defaults",
			"  azd ai cu config list\n" +
				"    Show stored CU user defaults, without process or environment overrides.", cobra.NoArgs},
		{"unset", "unset <name>", "Remove one CU user default",
			"  azd ai cu config unset CU_ENDPOINT\n" +
				"    Remove the user default without changing project settings.", cobra.ExactArgs(1)},
	} {
		cmd := &cobra.Command{Use: spec.use, Short: spec.short, Args: spec.args, Example: spec.example}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("environment") {
				return commandError("conflicting_arguments", "--environment cannot be used with CU user configuration.",
					"Use azd env set for project-environment settings, or omit --environment for user defaults.")
			}
			if spec.name != "list" {
				if err := config.ValidateSettingName(args[0]); err != nil {
					return err
				}
			}
			if spec.name == "set" {
				if err := config.ValidateSetting(args[0], args[1]); err != nil {
					return err
				}
			}
			client, err := openHostClient(cmd.Context())
			if err != nil {
				return err
			}
			defer client.Close()
			ctx := azdext.WithAccessToken(cmd.Context())
			switch spec.name {
			case "set":
				value, err := json.Marshal(args[1])
				if err != nil {
					return err
				}
				_, err = client.UserConfig().Set(ctx, &azdext.SetUserConfigRequest{
					Path: config.UserConfigPath + "." + args[0], Value: value,
				})
				if err != nil {
					return err
				}
				return writeServiceResult(cmd, extCtx.OutputFormat, map[string]string{args[0]: args[1]})
			case "unset":
				_, err := client.UserConfig().Unset(ctx, &azdext.UnsetUserConfigRequest{
					Path: config.UserConfigPath + "." + args[0],
				})
				return err
			default:
				settings, err := config.ReadUserDefaults(ctx, client)
				if err != nil {
					return err
				}
				names := config.SettingNames
				if spec.name == "get" {
					names = args[:1]
				}
				result := make(map[string]string)
				for _, name := range names {
					value, found, err := config.UserSetting(settings, name)
					if err != nil {
						return err
					}
					if !found {
						if spec.name == "get" {
							return commandError("setting_not_found", "The requested CU user default is not set.",
								"Set it with azd ai cu config set <name> <value>.")
						}
						continue
					}
					result[name] = value
				}
				return writeServiceResult(cmd, extCtx.OutputFormat, result)
			}
		}
		group.AddCommand(jsonCommand(cmd))
	}
	return group
}
