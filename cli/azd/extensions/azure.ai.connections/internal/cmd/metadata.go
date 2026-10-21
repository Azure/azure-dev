// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newMetadataCommand(rootCmd *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:    "metadata",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			metadata := azdext.GenerateExtensionMetadata(
				"1.0",
				"azure.ai.connections",
				rootCmd,
			)
			data, err := json.Marshal(metadata)
			if err != nil {
				return fmt.Errorf("encoding extension metadata: %w", err)
			}
			data, err = markNoAzdEnvironmentCommand(data, []string{"add"})
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(data)); err != nil {
				return fmt.Errorf("writing extension metadata: %w", err)
			}
			return nil
		},
	}
}

// The pinned SDK predates launch.noAzdEnvironment command metadata.
func markNoAzdEnvironmentCommand(data []byte, target []string) ([]byte, error) {
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("decoding extension metadata: %w", err)
	}

	rawCommands, found := metadata["commands"]
	if !found {
		return nil, fmt.Errorf("extension metadata has no commands")
	}
	var commands []json.RawMessage
	if err := json.Unmarshal(rawCommands, &commands); err != nil {
		return nil, fmt.Errorf("decoding extension commands: %w", err)
	}

	marked, err := markNoAzdEnvironmentCommandInTree(commands, target)
	if err != nil {
		return nil, err
	}
	if !marked {
		return nil, fmt.Errorf("extension metadata command %q was not found", target)
	}
	metadata["commands"], err = json.Marshal(commands)
	if err != nil {
		return nil, fmt.Errorf("encoding extension commands: %w", err)
	}

	encoded, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding extension metadata: %w", err)
	}
	return encoded, nil
}

func markNoAzdEnvironmentCommandInTree(
	commands []json.RawMessage,
	target []string,
) (bool, error) {
	for index, rawCommand := range commands {
		var command map[string]json.RawMessage
		if err := json.Unmarshal(rawCommand, &command); err != nil {
			return false, fmt.Errorf("decoding extension command metadata: %w", err)
		}

		var name []string
		if err := json.Unmarshal(command["name"], &name); err != nil {
			return false, fmt.Errorf("decoding extension command name: %w", err)
		}
		if slices.Equal(name, target) {
			var launch map[string]json.RawMessage
			if rawLaunch, found := command["launch"]; found {
				if err := json.Unmarshal(rawLaunch, &launch); err != nil {
					return false, fmt.Errorf("decoding extension command launch metadata: %w", err)
				}
			}
			if launch == nil {
				launch = make(map[string]json.RawMessage)
			}
			launch["noAzdEnvironment"] = json.RawMessage("true")
			rawLaunch, err := json.Marshal(launch)
			if err != nil {
				return false, fmt.Errorf("encoding extension command launch metadata: %w", err)
			}
			command["launch"] = rawLaunch
			encoded, err := json.Marshal(command)
			if err != nil {
				return false, fmt.Errorf("encoding extension command metadata: %w", err)
			}
			commands[index] = encoded
			return true, nil
		}

		rawSubcommands, found := command["subcommands"]
		if !found {
			continue
		}
		var subcommands []json.RawMessage
		if err := json.Unmarshal(rawSubcommands, &subcommands); err != nil {
			return false, fmt.Errorf("decoding extension subcommands: %w", err)
		}
		marked, err := markNoAzdEnvironmentCommandInTree(subcommands, target)
		if err != nil {
			return false, err
		}
		if !marked {
			continue
		}

		command["subcommands"], err = json.Marshal(subcommands)
		if err != nil {
			return false, fmt.Errorf("encoding extension subcommands: %w", err)
		}
		commands[index], err = json.Marshal(command)
		if err != nil {
			return false, fmt.Errorf("encoding extension command metadata: %w", err)
		}
		return true, nil
	}

	return false, nil
}
