// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type optimizeDownloadFlags struct {
	candidate  string
	file       string
	targetPath string
	optimizeConnectionFlags
}

func newOptimizeDownloadCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &optimizeDownloadFlags{}
	cmd := &cobra.Command{
		Use:   "download <operation-id>",
		Short: "Download a published optimization candidate file.",
		Long: `Download a file declared in a candidate's files list, such as a trace-analysis report.

Use 'optimize status <operation-id> --candidate <id> --output json' to inspect file paths.
Pass the declared path unchanged with --file. The local filename defaults to its basename.
Existing local files are never overwritten; choose another --target-path.`,
		Example: `  # Download a trace-analysis report
  azd ai agent optimize download opt_abc123 --candidate candidate_abc123 --file trace-analysis-hash.json

  # Choose a local filename
  azd ai agent optimize download opt_abc123 --candidate candidate_abc123 --file trace-analysis-hash.json --target-path report.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, name := range []string{"candidate", "file", "target-path", "endpoint", "project-endpoint"} {
				if cmd.Flags().Changed(name) {
					value, err := cmd.Flags().GetString(name)
					if err != nil {
						return err
					}
					if strings.TrimSpace(value) == "" {
						return exterrors.Validation(exterrors.CodeInvalidParameter,
							fmt.Sprintf("--%s must not be empty", name), "provide a non-empty value or omit the flag")
					}
				}
			}
			if strings.TrimSpace(args[0]) == "" || flags.candidate == "" || flags.file == "" {
				return exterrors.Validation(exterrors.CodeInvalidParameter,
					"operation ID, --candidate, and --file are required", "use a declared candidate file path")
			}
			if cmd.Flags().Changed("endpoint") && cmd.Flags().Changed("project-endpoint") {
				return exterrors.Validation(exterrors.CodeConflictingArguments,
					"--endpoint cannot be combined with --project-endpoint", "supply a single endpoint")
			}
			target, err := optimizeDownloadTarget(flags)
			if err != nil {
				return exterrors.Validation(exterrors.CodeInvalidParameter, err.Error(), "choose another --target-path")
			}
			ctx := azdext.WithAccessToken(cmd.Context())
			endpoint, err := flags.resolve(ctx, extCtx.Environment)
			if err != nil {
				return err
			}
			credential, err := newAgentCredential()
			if err != nil {
				return err
			}
			client := optimize_api.NewOptimizeClient(endpoint, credential)
			if err := downloadOptimizeCandidateFile(ctx, client, args[0], flags.candidate, flags.file, target); err != nil {
				return err
			}
			if extCtx.OutputFormat == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{
					"job_id": args[0], "candidate_id": flags.candidate, "path": flags.file, "target_path": target,
				})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Downloaded %s -> %s\n", flags.file, target)
			return err
		},
	}
	cmd.Flags().StringVar(&flags.candidate, "candidate", "", "Candidate ID (required)")
	cmd.Flags().StringVarP(&flags.file, "file", "f", "", "Declared candidate file path (required)")
	cmd.Flags().StringVarP(&flags.targetPath, "target-path", "t", "", "Local destination path (defaults to remote filename)")
	_ = cmd.MarkFlagRequired("candidate")
	_ = cmd.MarkFlagRequired("file")
	flags.optimizeConnectionFlags.register(cmd)
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})
	return cmd
}

func optimizeDownloadTarget(flags *optimizeDownloadFlags) (string, error) {
	target := flags.targetPath
	if target == "" {
		target = path.Base(strings.ReplaceAll(flags.file, "\\", "/"))
		if target == "." || !filepath.IsLocal(target) {
			return "", fmt.Errorf("candidate file path has no safe local filename")
		}
	}
	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("local destination %q already exists", target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("checking local destination: %w", err)
	}
	return target, nil
}

func downloadOptimizeCandidateFile(
	ctx context.Context,
	client *optimize_api.OptimizeClient,
	jobID, candidateID, remotePath, target string,
) error {
	candidate, err := client.GetCandidate(ctx, jobID, candidateID)
	if err != nil {
		return fmt.Errorf("loading candidate files: %w", err)
	}
	if !slices.ContainsFunc(candidate.Files, func(file optimize_api.CandidateFile) bool { return file.Path == remotePath }) {
		return fmt.Errorf("file %q is not declared by candidate %q; inspect 'optimize status --candidate'", remotePath, candidateID)
	}
	content, err := client.GetCandidateFile(ctx, jobID, candidateID, remotePath)
	if err != nil {
		return fmt.Errorf("downloading candidate file: %w", err)
	}
	// O_EXCL also protects against a destination created after input validation.
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating local destination %q: %w", target, err)
	}
	_, writeErr := io.WriteString(file, content)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return errors.Join(fmt.Errorf("saving candidate file: %w", err), os.Remove(target))
	}
	return nil
}
