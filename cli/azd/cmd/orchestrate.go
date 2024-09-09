// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"os"
	"path/filepath"
)

func newOrchestrateFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *orchestrateFlags {
	flags := &orchestrateFlags{}
	return flags
}

func newOrchestrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "orchestrate",
		Short: "Orchestrate an existing application. (Beta)",
	}
}

type orchestrateFlags struct {
	global *internal.GlobalCommandOptions
}

type orchestrateAction struct {
}

func (action orchestrateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	azureYamlFile, err := os.Create("azure.yaml")
	if err != nil {
		return nil, fmt.Errorf("creating azure.yaml: %w", err)
	}
	defer azureYamlFile.Close()

	files, err := findPomFiles(".")
	if err != nil {
		fmt.Println("Error:", err)
		return nil, fmt.Errorf("find pom files: %w", err)
	}

	for _, file := range files {
		if _, err := azureYamlFile.WriteString(file + "\n"); err != nil {
			return nil, fmt.Errorf("writing azure.yaml: %w", err)
		}
	}

	if err := azureYamlFile.Sync(); err != nil {
		return nil, fmt.Errorf("saving azure.yaml: %w", err)
	}
	return nil, nil
}

func newOrchestrateAction() actions.Action {
	return &orchestrateAction{}
}

func getCmdOrchestrateHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription("Orchestrate an existing application in your current directory.",
		[]string{
			formatHelpNote(
				fmt.Sprintf("Running %s without flags specified will prompt "+
					"you to orchestrate using your existing code.",
					output.WithHighLightFormat("orchestrate"),
				)),
		})
}

func getCmdOrchestrateHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Orchestrate a existing project.": fmt.Sprintf("%s",
			output.WithHighLightFormat("azd orchestrate"),
		),
	})
}

func findPomFiles(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Base(path) == "pom.xml" {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
