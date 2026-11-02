// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"
	"azure.ai.content-understanding/internal/schema"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newAnalyzerValidateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	var path, version string
	var strict, spec bool
	cmd := &cobra.Command{
		Use: "validate [SCHEMA]", Short: "Validate a local analyzer schema without host or service access",
		Long: "Validate UTF-8 JSON using curated structural and authoring checks.\n" +
			"--strict treats authoring warnings as errors. " +
			"--spec also checks the bundled service create contract offline.\n" +
			"Uses the explicit --api-version or GA default; environment credentials and configuration are not required.",
		Example: `  azd ai cu analyzer validate schema.json
    Check a local analyzer schema without authentication or service calls.

  azd ai cu analyzer validate schema.json --strict --spec --output json
    Also check the bundled service contract and treat warnings as errors.`,
		Args: cobra.MaximumNArgs(1),
	}
	cmd.Flags().StringVarP(&path, "schema", "s", "", "Path to a local UTF-8 JSON analyzer schema")
	cmd.Flags().StringVar(&version, "api-version", "", "CU API version: 2025-11-01 or YYYY-MM-DD-preview (default: GA)")
	cmd.Flags().BoolVar(&strict, "strict", false, "Treat authoring warnings as validation failures")
	cmd.Flags().BoolVar(&spec, "spec", false, "Also validate against the bundled offline CU create contract")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := cmd.Context().Err(); err != nil {
			return exterrors.FromError(err)
		}
		selected := path
		if len(args) != 0 {
			if cmd.Flags().Changed("schema") {
				return exterrors.FromError(commandError("conflicting_schema",
					"Specify the schema only once.", "Use a positional path or --schema, not both."))
			}
			selected = args[0]
		}
		if strings.TrimSpace(selected) == "" {
			return exterrors.FromError(commandError("missing_schema", "Specify a schema file.", "Use SCHEMA or --schema."))
		}
		resolved, err := cuclient.NormalizeAPIVersion(version)
		if err != nil {
			return exterrors.FromError(err)
		}
		data, err := os.ReadFile(filepath.Clean(selected))
		if err != nil {
			return exterrors.FromError(commandError("schema_read_failed",
				"The schema file could not be read.", "Specify a readable UTF-8 JSON file relative to --cwd."))
		}
		result, err := schema.Validate(data, resolved, spec)
		if ctxErr := cmd.Context().Err(); ctxErr != nil {
			return exterrors.FromError(ctxErr)
		}
		if err != nil {
			return exterrors.FromError(err)
		}
		findings := append([]schema.Finding{}, result.Errors...)
		if strict {
			findings = append(findings, result.Warnings...)
		}
		if len(findings) != 0 {
			var message strings.Builder
			message.WriteString("Schema validation failed:")
			for _, finding := range findings {
				message.WriteString("\n" + finding.DiagnosticPath() + ": " + finding.Message)
			}
			return exterrors.FromError(commandError("invalid_analyzer_schema", message.String(),
				"Correct the reported constraints, or omit --strict to allow authoring warnings."))
		}
		return exterrors.FromError(writeServiceResult(cmd, extCtx.OutputFormat, struct {
			schema.ValidationResult
			Strict     bool   `json:"strict"`
			APIVersion string `json:"apiVersion"`
		}{result, strict, resolved}))
	}
	return jsonCommand(cmd)
}
