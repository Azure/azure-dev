// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azureaieval/internal/messages"

	"github.com/spf13/cobra"
)

type reconciledArtifact struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Published bool   `json:"published"`
}

func reportCreatePartial(
	cmd *cobra.Command, ec *evalContext, name, path string, artifacts []reconciledArtifact, cause error,
) error {
	retry, err := commandInProject(
		"azd ai eval create "+messages.ShellArg(name)+" --from-file "+messages.ShellArg(path),
		ec.endpoint, ec.envName)
	if err != nil {
		return err
	}
	if isJSON(cmd) {
		return emitJSON(cmd.OutOrStdout(), map[string]any{
			"status": "failed", "name": name, "artifacts": artifacts,
			"error": jsonErrorBody{Message: cause.Error()}, "recovery_command": retry,
		})
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), messages.CreateDependenciesRetained(retry))
	return err
}
