// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"azure.ai.customtraining/internal/utils"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newJobValidateCommand() *cobra.Command {
	var filePath string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a job YAML definition file offline without submitting",
		// Override parent's PersistentPreRunE — validate is offline and needs no Azure setup.
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error { return nil },
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if filePath == "" {
				return fmt.Errorf("--file is required: provide a path to a YAML job definition file")
			}

			// Read and parse the YAML file
			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("failed to read job file '%s': %w", filePath, err)
			}

			var jobDef utils.JobDefinition
			if err := yaml.Unmarshal(data, &jobDef); err != nil {
				return fmt.Errorf("failed to parse job YAML: %w", err)
			}

			// Run offline validation — collects all findings
			yamlDir := filepath.Dir(filePath)
			result := utils.ValidateJobOffline(&jobDef, yamlDir)

			// Print findings
			if len(result.Findings) == 0 {
				fmt.Printf("✓ Validation passed: %s\n", filePath)
				return nil
			}

			fmt.Printf("Validation results for: %s\n\n", filePath)

			for _, f := range result.Findings {
				prefix := "⚠"
				if f.Severity == utils.SeverityError {
					prefix = "✗"
				}
				fmt.Printf("  %s [%s] %s: %s\n", prefix, f.Severity, f.Field, f.Message)
			}

			fmt.Println()
			fmt.Printf("  Errors: %d, Warnings: %d\n", result.ErrorCount(), result.WarningCount())

			if result.HasErrors() {
				fmt.Printf("\n✗ Validation failed.\n")
				return fmt.Errorf("validation failed with %d error(s)", result.ErrorCount())
			}

			fmt.Printf("\n✓ Validation passed with warnings.\n")
			return nil
		},
	}

	cmd.Flags().StringVar(&filePath, "file", "", "Path to YAML job definition file (required)")

	return cmd
}
