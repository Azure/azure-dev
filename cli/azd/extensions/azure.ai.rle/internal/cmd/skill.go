// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

var installRleSkillsFunc = project.InstallRleSkills
var findRleProjectRootFunc = findRleProjectRoot

func newSkillCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "skill",
		Short: "Manage RLE project skills",
		Args:  cobra.NoArgs,
	}
	command.AddCommand(newSkillInstallCommand())
	return command
}

func newSkillInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install or update RLE project skills",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, args []string) error {
			projectRoot, err := findRleProjectRootFunc()
			if err != nil {
				return err
			}
			skillNames, err := installRleSkillsFunc(projectRoot)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(
				command.OutOrStdout(),
				"Installed or updated RLE project skills in %s: %s\n",
				strings.Join(project.RleSkillsPaths, " and "),
				strings.Join(skillNames, ", "),
			)
			return err
		},
	}
}

func findRleProjectRoot() (string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return findRleProjectRootFrom(currentDir)
}

func findRleProjectRootFrom(currentDir string) (string, error) {
	for {
		if info, err := os.Stat(filepath.Join(currentDir, project.RleConfigFile)); err == nil && info.Mode().IsRegular() {
			if _, err := project.LoadRleConfig(currentDir); err != nil {
				return "", err
			}
			return currentDir, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return "", &azdext.LocalError{
				Message: fmt.Sprintf(
					"Could not find %s in the current directory or any parent directory.",
					project.RleConfigFile,
				),
				Code:       "rle_project_not_found",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: "Run this command from an initialized RLE project.",
			}
		}
		currentDir = parentDir
	}
}
