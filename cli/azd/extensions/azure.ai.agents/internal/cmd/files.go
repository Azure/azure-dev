// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// filesFlags holds the common flags shared by all file subcommands.
type filesFlags struct {
	service string // optional: azure.yaml service name for resolution
	session string // optional: explicit session ID override
}

func newFilesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "files",
		Short: "Manage files in a hosted agent session.",
		Long: `Manage files in a hosted agent session.

Upload, download, list, and remove files in the session-scoped filesystem
of a hosted agent. This is useful for debugging, seeding data, and agent setup.

Agent details (name, version, endpoint) are automatically resolved from the
azd environment. Use --service to select a specific service when the project
has multiple azure.ai.agent services. The session ID is automatically resolved
from the last invoke session, or can be overridden with --session.`,
	}

	cmd.AddCommand(newFilesUploadCommand())
	cmd.AddCommand(newFilesDownloadCommand())
	cmd.AddCommand(newFilesListCommand())
	cmd.AddCommand(newFilesRemoveCommand())

	return cmd
}

// addFilesFlags registers the common flags on a cobra command.
func addFilesFlags(cmd *cobra.Command, flags *filesFlags) {
	cmd.Flags().StringVar(&flags.service, "service", "", "Azure.yaml service name (auto-detected when only one exists)")
	cmd.Flags().StringVarP(&flags.session, "session", "s", "", "Session ID override (defaults to last invoke session)")
}

// filesContext holds the resolved agent context and session for file operations.
type filesContext struct {
	*AgentContext
	sessionID string
}

// resolveFilesContext resolves agent details and session from the azd environment.
func resolveFilesContext(ctx context.Context, flags *filesFlags) (*filesContext, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	info, err := resolveAgentServiceFromProject(ctx, azdClient, flags.service, rootFlags.NoPrompt)
	if err != nil {
		return nil, err
	}

	if info.AgentName == "" {
		return nil, fmt.Errorf(
			"agent name not found in azd environment for service %q\n\n"+
				"Run 'azd deploy' to deploy the agent, or check that the service is configured in azure.yaml",
			info.ServiceName,
		)
	}
	if info.Version == "" {
		return nil, fmt.Errorf(
			"agent version not found in azd environment for service %q\n\n"+
				"Run 'azd deploy' to deploy the agent, or check that the service is configured in azure.yaml",
			info.ServiceName,
		)
	}

	endpoint, err := resolveAgentEndpoint(ctx, "", "")
	if err != nil {
		return nil, err
	}

	sessionID, err := resolveSessionID(ctx, azdClient, info.AgentName, flags.session, false)
	if err != nil {
		return nil, err
	}

	return &filesContext{
		AgentContext: &AgentContext{
			ProjectEndpoint: endpoint,
			Name:            info.AgentName,
			Version:         info.Version,
		},
		sessionID: sessionID,
	}, nil
}

// --- upload ---

type filesUploadFlags struct {
	filesFlags
	localPath string
}

// FilesUploadAction handles uploading a file to a session.
type FilesUploadAction struct {
	*AgentContext
	flags      *filesUploadFlags
	sessionID  string
	remotePath string
}

func newFilesUploadCommand() *cobra.Command {
	flags := &filesUploadFlags{}

	cmd := &cobra.Command{
		Use:   "upload <remote-path>",
		Short: "Upload a file to a hosted agent session.",
		Long: `Upload a file to a hosted agent session.

Reads a local file and uploads it to the specified remote path
in the session's filesystem.

Agent details are automatically resolved from the azd environment.`,
		Example: `  # Upload a file to the session (agent auto-detected from azure.yaml)
  azd ai agent files upload /data/input.csv --path ./input.csv

  # Upload with explicit service and session
  azd ai agent files upload /data/input.csv --path ./input.csv --service my-agent --session <session-id>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			fc, err := resolveFilesContext(ctx, &flags.filesFlags)
			if err != nil {
				return err
			}

			action := &FilesUploadAction{
				AgentContext: fc.AgentContext,
				flags:        flags,
				sessionID:    fc.sessionID,
				remotePath:   args[0],
			}

			return action.Run(ctx)
		},
	}

	addFilesFlags(cmd, &flags.filesFlags)
	cmd.Flags().StringVar(&flags.localPath, "path", "", "Local file path to upload (required)")
	_ = cmd.MarkFlagRequired("path")

	return cmd
}

// Run executes the upload action.
func (a *FilesUploadAction) Run(ctx context.Context) error {
	//nolint:gosec // G304: localPath is provided by the user via CLI flag
	file, err := os.Open(a.flags.localPath)
	if err != nil {
		return fmt.Errorf("failed to open local file %q: %w", a.flags.localPath, err)
	}
	defer file.Close()

	agentClient, err := a.NewClient()
	if err != nil {
		return err
	}

	err = agentClient.UploadSessionFile(
		ctx,
		a.Name,
		a.Version,
		a.sessionID,
		a.remotePath,
		DefaultVNextAgentAPIVersion,
		file,
	)
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}

	fmt.Printf("Uploaded %s → %s\n", a.flags.localPath, a.remotePath)
	return nil
}

// --- download ---

type filesDownloadFlags struct {
	filesFlags
	outputPath string
}

// FilesDownloadAction handles downloading a file from a session.
type FilesDownloadAction struct {
	*AgentContext
	flags      *filesDownloadFlags
	sessionID  string
	remotePath string
}

func newFilesDownloadCommand() *cobra.Command {
	flags := &filesDownloadFlags{}

	cmd := &cobra.Command{
		Use:   "download <remote-path>",
		Short: "Download a file from a hosted agent session.",
		Long: `Download a file from a hosted agent session.

Downloads a file from the specified remote path in the session's
filesystem and saves it locally.

Agent details are automatically resolved from the azd environment.`,
		Example: `  # Download a file from the session (agent auto-detected)
  azd ai agent files download /data/output.csv -o ./output.csv

  # Download to current directory (uses remote filename)
  azd ai agent files download /data/output.csv

  # Download with explicit session
  azd ai agent files download /data/output.csv -o ./output.csv --session <session-id>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			fc, err := resolveFilesContext(ctx, &flags.filesFlags)
			if err != nil {
				return err
			}

			action := &FilesDownloadAction{
				AgentContext: fc.AgentContext,
				flags:        flags,
				sessionID:    fc.sessionID,
				remotePath:   args[0],
			}

			return action.Run(ctx)
		},
	}

	addFilesFlags(cmd, &flags.filesFlags)
	cmd.Flags().StringVarP(&flags.outputPath, "output", "o", "", "Local output file path (defaults to remote filename)")

	return cmd
}

// Run executes the download action.
func (a *FilesDownloadAction) Run(ctx context.Context) error {
	agentClient, err := a.NewClient()
	if err != nil {
		return err
	}

	body, err := agentClient.DownloadSessionFile(
		ctx,
		a.Name,
		a.Version,
		a.sessionID,
		a.remotePath,
		DefaultVNextAgentAPIVersion,
	)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}
	defer body.Close()

	outputPath := a.flags.outputPath
	if outputPath == "" {
		outputPath = filepath.Base(a.remotePath)
	}

	//nolint:gosec // G304: outputPath is provided by the user via CLI flag
	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file %q: %w", outputPath, err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, body); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	fmt.Printf("Downloaded %s → %s\n", a.remotePath, outputPath)
	return nil
}

// --- list ---

type filesListFlags struct {
	filesFlags
	output string
}

// FilesListAction handles listing files in a session.
type FilesListAction struct {
	*AgentContext
	flags      *filesListFlags
	sessionID  string
	remotePath string
}

func newFilesListCommand() *cobra.Command {
	flags := &filesListFlags{}

	cmd := &cobra.Command{
		Use:   "list [remote-path]",
		Short: "List files in a hosted agent session.",
		Long: `List files in a hosted agent session.

Lists files and directories at the specified path in the session's filesystem.
When no path is provided, lists the root directory.

Agent details are automatically resolved from the azd environment.`,
		Example: `  # List files in the root directory (agent auto-detected)
  azd ai agent files list

  # List files in a specific directory
  azd ai agent files list /data

  # List files in table format
  azd ai agent files list /data --output table

  # List with explicit session
  azd ai agent files list --session <session-id>`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			fc, err := resolveFilesContext(ctx, &flags.filesFlags)
			if err != nil {
				return err
			}

			remotePath := ""
			if len(args) > 0 {
				remotePath = args[0]
			}

			action := &FilesListAction{
				AgentContext: fc.AgentContext,
				flags:        flags,
				sessionID:    fc.sessionID,
				remotePath:   remotePath,
			}

			return action.Run(ctx)
		},
	}

	addFilesFlags(cmd, &flags.filesFlags)
	cmd.Flags().StringVar(&flags.output, "output", "json", "Output format (json or table)")

	return cmd
}

// Run executes the list action.
func (a *FilesListAction) Run(ctx context.Context) error {
	agentClient, err := a.NewClient()
	if err != nil {
		return err
	}

	fileList, err := agentClient.ListSessionFiles(
		ctx,
		a.Name,
		a.Version,
		a.sessionID,
		a.remotePath,
		DefaultVNextAgentAPIVersion,
	)
	if err != nil {
		return fmt.Errorf("failed to list files: %w", err)
	}

	switch a.flags.output {
	case "table":
		return printFileListTable(fileList)
	default:
		return printFileListJSON(fileList)
	}
}

func printFileListJSON(fileList *agent_api.SessionFileList) error {
	jsonBytes, err := json.MarshalIndent(fileList, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal file list to JSON: %w", err)
	}
	fmt.Println(string(jsonBytes))
	return nil
}

func printFileListTable(fileList *agent_api.SessionFileList) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPATH\tTYPE\tSIZE\tLAST MODIFIED")
	fmt.Fprintln(w, "----\t----\t----\t----\t-------------")

	for _, f := range fileList.Entries {
		fileType := "file"
		if f.IsDirectory {
			fileType = "dir"
		}
		modified := ""
		if f.LastModified != nil {
			modified = *f.LastModified
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", f.Name, f.Path, fileType, f.Size, modified)
	}

	return w.Flush()
}

// --- remove ---

type filesRemoveFlags struct {
	filesFlags
	recursive bool
}

// FilesRemoveAction handles removing a file or directory from a session.
type FilesRemoveAction struct {
	*AgentContext
	flags      *filesRemoveFlags
	sessionID  string
	remotePath string
}

func newFilesRemoveCommand() *cobra.Command {
	flags := &filesRemoveFlags{}

	cmd := &cobra.Command{
		Use:   "remove <remote-path>",
		Short: "Remove a file or directory from a hosted agent session.",
		Long: `Remove a file or directory from a hosted agent session.

Removes the specified file or directory from the session's filesystem.
Use --recursive to remove directories and their contents.

Agent details are automatically resolved from the azd environment.`,
		Example: `  # Remove a file (agent auto-detected)
  azd ai agent files remove /data/old-file.csv

  # Remove a directory recursively
  azd ai agent files remove /data/temp --recursive

  # Remove with explicit session
  azd ai agent files remove /data/old-file.csv --session <session-id>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			fc, err := resolveFilesContext(ctx, &flags.filesFlags)
			if err != nil {
				return err
			}

			action := &FilesRemoveAction{
				AgentContext: fc.AgentContext,
				flags:        flags,
				sessionID:    fc.sessionID,
				remotePath:   args[0],
			}

			return action.Run(ctx)
		},
	}

	addFilesFlags(cmd, &flags.filesFlags)
	cmd.Flags().BoolVar(&flags.recursive, "recursive", false, "Recursively remove directories and their contents")

	return cmd
}

// Run executes the remove action.
func (a *FilesRemoveAction) Run(ctx context.Context) error {
	agentClient, err := a.NewClient()
	if err != nil {
		return err
	}

	err = agentClient.RemoveSessionFile(
		ctx,
		a.Name,
		a.Version,
		a.sessionID,
		a.remotePath,
		a.flags.recursive,
		DefaultVNextAgentAPIVersion,
	)
	if err != nil {
		return fmt.Errorf("failed to remove file: %w", err)
	}

	fmt.Printf("Removed %s\n", a.remotePath)
	return nil
}
