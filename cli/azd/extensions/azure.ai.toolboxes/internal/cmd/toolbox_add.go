// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"os"

	"azure.ai.toolboxes/internal/definition"
	"azure.ai.toolboxes/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type localAddFlags struct {
	file string
}

type localConnectionAddFlags struct {
	localAddFlags
	index        string
	instanceName string
}

func newToolboxAddCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <type> <name>",
		Short: "Add a reference to a local toolbox definition.",
		Long: `Add a connection or skill reference to a local toolbox definition.

This command edits toolbox.yaml only. Run 'azd ai toolbox deploy' to create a
new toolbox version from the updated definition.`,
	}
	cmd.AddCommand(newToolboxAddSkillCommand(extCtx))
	cmd.AddCommand(newToolboxAddConnectionCommand(extCtx))
	return cmd
}

func newToolboxAddSkillCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &localAddFlags{}
	cmd := &cobra.Command{
		Use:   "skill <name>[@<version>]",
		Short: "Add a skill reference to toolbox.yaml.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLocalSkillAdd(args[0], *flags, readToolboxFlags(cmd, extCtx))
		},
	}
	registerLocalDefinitionFlag(cmd, &flags.file)
	registerToolboxOutputFlag(cmd)
	return cmd
}

func newToolboxAddConnectionCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &localConnectionAddFlags{}
	cmd := &cobra.Command{
		Use:   "connection <name>",
		Short: "Add a connection reference to toolbox.yaml.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLocalConnectionAdd(args[0], *flags, readToolboxFlags(cmd, extCtx))
		},
	}
	registerLocalDefinitionFlag(cmd, &flags.file)
	cmd.Flags().StringVar(
		&flags.index, "index", "",
		"Search index name used by a CognitiveSearch connection.",
	)
	cmd.Flags().StringVar(
		&flags.instanceName, "instance-name", "",
		"Custom search configuration used by a GroundingWithCustomSearch connection.",
	)
	registerToolboxOutputFlag(cmd)
	return cmd
}

func registerLocalDefinitionFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVar(
		target, "file", definition.DefaultPath,
		"Path to the local toolbox definition.",
	)
}

func runLocalSkillAdd(rawSkill string, flags localAddFlags, parent toolboxFlags) error {
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}
	skill, err := parseSkillFlag(rawSkill)
	if err != nil {
		return err
	}
	toolbox, err := loadLocalToolboxDefinition(flags.file)
	if err != nil {
		return err
	}
	if err := toolbox.AddSkill(definition.SkillReference{
		Name: skill.Name, Version: skill.Version,
	}); err != nil {
		if errors.Is(err, definition.ErrDuplicateSkill) {
			return exterrors.Validation(
				exterrors.CodeSkillAlreadyAttached,
				fmt.Sprintf("skill %q is already referenced in %q", skill.Name, flags.file),
				"remove the existing reference before adding a different version",
			)
		}
		return classifyLocalDefinitionError(flags.file, err)
	}
	if err := saveLocalToolboxDefinition(flags.file, toolbox); err != nil {
		return err
	}
	return emitLocalAddResult(parent.output, flags.file, "skill", skill.Name)
}

func runLocalConnectionAdd(name string, flags localConnectionAddFlags, parent toolboxFlags) error {
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}
	if err := validateToolName(name); err != nil {
		return err
	}
	toolbox, err := loadLocalToolboxDefinition(flags.file)
	if err != nil {
		return err
	}
	if err := toolbox.AddConnection(definition.ConnectionReference{
		Name: name, Index: flags.index, InstanceName: flags.instanceName,
	}); err != nil {
		if errors.Is(err, definition.ErrDuplicateConnection) {
			return exterrors.Validation(
				exterrors.CodeDuplicateConnection,
				fmt.Sprintf("connection %q is already referenced in %q", name, flags.file),
				"remove the existing reference before adding it again",
			)
		}
		return classifyLocalDefinitionError(flags.file, err)
	}
	if err := saveLocalToolboxDefinition(flags.file, toolbox); err != nil {
		return err
	}
	return emitLocalAddResult(parent.output, flags.file, "connection", name)
}

func loadLocalToolboxDefinition(path string) (*definition.Definition, error) {
	toolbox, err := definition.Load(path)
	if err == nil {
		return toolbox, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, exterrors.Dependency(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("toolbox definition %q was not found", path),
			"run the command from a directory containing toolbox.yaml or pass --file <path>",
		)
	}
	return nil, classifyLocalDefinitionError(path, err)
}

func saveLocalToolboxDefinition(path string, toolbox *definition.Definition) error {
	if err := definition.Save(path, toolbox); err != nil {
		return exterrors.Dependency(
			exterrors.CodePendingToolboxStoreFailed,
			fmt.Sprintf("failed to save toolbox definition %q: %s", path, err),
			"verify the file and directory permissions, then retry",
		)
	}
	return nil
}

func classifyLocalDefinitionError(path string, err error) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf("toolbox definition %q is invalid: %s", path, err),
		"fix the toolbox definition and retry",
	)
}

func emitLocalAddResult(output, path, referenceType, name string) error {
	if output == "json" {
		return emitJSON(map[string]string{
			"definition": path,
			"type":       referenceType,
			"name":       name,
		})
	}
	fmt.Printf("Added %s reference %q to %s.\n", referenceType, name, path)
	return nil
}
