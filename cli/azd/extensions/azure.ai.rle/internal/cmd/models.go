// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type supportedModel struct {
	Model     string   `json:"model"`
	Renderers []string `json:"renderers"`
}

func newModelsCommand(outputFormat *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List supported RLE models and renderers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&modelsAction{cmd: cmd, outputFormat: outputFormat}).Run()
		},
	}
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"default", "json"},
	})
	return cmd
}

type modelsAction struct {
	cmd          *cobra.Command
	outputFormat *string
}

func (a *modelsAction) Run() error {
	models := supportedModels()
	format, err := azdext.ParseOutputFormat(*a.outputFormat)
	if err != nil {
		return err
	}
	output := azdext.NewOutput(azdext.OutputOptions{
		Format:    format,
		Writer:    a.cmd.OutOrStdout(),
		ErrWriter: a.cmd.ErrOrStderr(),
	})
	if output.IsJSON() {
		return output.JSON(models)
	}

	rows := make([][]string, 0, len(models))
	for _, model := range models {
		rows = append(rows, []string{model.Model, strings.Join(model.Renderers, ", ")})
	}
	renderTableOrNoResults(output, []string{"MODEL", "RENDERERS"}, rows, "No supported models found.")
	return nil
}

func supportedModels() []supportedModel {
	return []supportedModel{
		{Model: "MAI/MAI-Code-1.1-Flash", Renderers: []string{"mai_harmony_internal"}},
		{Model: "Qwen/Qwen3-32B", Renderers: []string{"qwen3_ext"}},
	}
}
