// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"path/filepath"

	"azureaieval/internal/messages"
	"azureaieval/internal/urlsafe"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
		"azd ai eval create "+messages.ShellArg(name)+" --from-file "+quoteForShell(path),
		ec.endpoint, ec.envName)
	if err != nil {
		return err
	}
	guidance := ""
	if !messages.CanInlineShellArg(name) || !messages.CanInlineShellArg(filepath.ToSlash(path)) ||
		!messages.CanInlineShellArg(ec.envName) {
		guidance = messages.CreateRetryQuotingGuidance()
	}
	if isJSON(cmd) {
		document := map[string]any{
			"status": "failed", "name": name, "artifacts": artifacts,
			"error": jsonErrorBody{
				Message: jsonMessage(cause), Code: errorCode(cause),
				Suggestion: urlsafe.Text(azdext.ErrorSuggestion(cause)),
			},
			"recovery_command": retry,
		}
		if guidance != "" {
			document["retry_guidance"] = guidance
		}
		return emitJSON(cmd.OutOrStdout(), document)
	}
	text := messages.CreateDependenciesRetained(retry)
	if guidance != "" {
		text += guidance + "\n"
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), text)
	return err
}
