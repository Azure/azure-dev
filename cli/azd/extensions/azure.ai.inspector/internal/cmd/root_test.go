// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"reflect"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
)

func TestRootCommandLaunchIsExplicitSubcommand(t *testing.T) {
	root := NewRootCommand()

	if root.Run != nil || root.RunE != nil {
		t.Fatal("root command should not launch the inspector directly")
	}
	if root.Flags().Lookup("port") != nil {
		t.Fatal("root command should not accept launch flags")
	}

	launch, _, err := root.Find([]string{"launch"})
	if err != nil {
		t.Fatalf("find launch command: %v", err)
	}
	if launch == root {
		t.Fatal("launch should be a subcommand")
	}
	if launch.RunE == nil {
		t.Fatal("launch command should run the inspector")
	}
	if launch.Flags().Lookup("port") == nil {
		t.Fatal("launch command should accept the agent port flag")
	}
}

func TestMetadataExposesLaunchCommand(t *testing.T) {
	metadata := azdext.GenerateExtensionMetadata("1.0", "azure.ai.inspector", NewRootCommand())

	launch := findCommand(metadata.Commands, []string{"launch"})
	if launch == nil {
		t.Fatal("metadata should expose launch command")
	}
	if findFlag(launch.Flags, "port") == nil {
		t.Fatal("launch metadata should expose the agent port flag")
	}
	if findCommand(metadata.Commands, []string{"inspector"}) != nil {
		t.Fatal("metadata should not expose a root inspector command")
	}
}

func findCommand(commands []extensions.Command, name []string) *extensions.Command {
	for i := range commands {
		if reflect.DeepEqual(commands[i].Name, name) {
			return &commands[i]
		}
		if cmd := findCommand(commands[i].Subcommands, name); cmd != nil {
			return cmd
		}
	}

	return nil
}

func findFlag(flags []extensions.Flag, name string) *extensions.Flag {
	for i := range flags {
		if flags[i].Name == name {
			return &flags[i]
		}
	}

	return nil
}
