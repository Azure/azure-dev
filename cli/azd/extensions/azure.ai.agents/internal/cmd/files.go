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
	"strconv"
	"text/tabwriter"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// filesFlags holds the common flags shared by all file subcommands.
type filesFlags struct {
	agentName string // optional: agent name (matches azure.yaml service name)
	session   string // optional: explicit session ID override
}

// isVNextEnabled checks whether hosted agent vnext is enabled
// by looking at both the OS environment and the azd environment.
// An optional AzdClient can be passed to avoid creating a new connection;
// if nil, the function creates one internally.
func isVNextEnabled(ctx context.Context, client ...*azdext.AzdClient) bool {
	if v := os.Getenv("enableHostedAgentVNext"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil && enabled {
			return true
		}
	}

	// Use provided client or create one for best-effort azd env check
	var azdClient *azdext.AzdClient
	if len(client) > 0 && client[0] != nil {
		azdClient = client[0]
	} else {
		var err error
		azdClient, err = azdext.NewAzdClient()
		if err != nil {
			return false
		}
		defer azdClient.Close()
	}

	azdEnv, err := loadAzdEnvironment(ctx, azdClient)
	if err != nil {
		return false
	}

	if v := azdEnv["enableHostedAgentVNext"]; v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil && enabled {
			return true
		}
	}

	return false
}

func newFilesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "files",
		Short:  "Manage files in a hosted agent session.",
		Hidden: !isVNextEnabled(context.Background()),
		Long: `Manage files in a hosted agent session.

Upload, download, list, and remove files in the session-scoped filesystem
of a hosted agent. This is useful for debugging, seeding data, and agent setup.

Agent details (name, version, endpoint) are automatically resolved from the
azd environment. Use --agent-name to select a specific agent when the project
has multiple azure.ai.agent services. The session ID is automatically resolved
from the last invoke session, or can be overridden with --session.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Chain with root's PersistentPreRunE (root sets NoPrompt).
			// Note: cmd.Parent() would return the "files" command itself when
			// a subcommand runs, causing infinite recursion.
			if root := cmd.Root(); root != nil && root.PersistentPreRunE != nil {
				if err := root.PersistentPreRunE(cmd, args); err != nil {
					return err
				}
			}

			ctx := azdext.WithAccessToken(cmd.Context())
			if !isVNextEnabled(ctx) {
				return fmt.Errorf(
					"files commands require hosted agent vnext to be enabled\n\n" +
						"Set 'enableHostedAgentVNext' to 'true' in your azd environment or as an OS environment variable.",
				)
			}
			return nil
		},
	}

	cmd.AddCommand(newFilesUploadCommand())
	cmd.AddCommand(newFilesDownloadCommand())
	cmd.AddCommand(newFilesListCommand())
	cmd.AddCommand(newFilesRemoveCommand())
	cmd.AddCommand(newFilesMkdirCommand())
	cmd.AddCommand(newFilesStatCommand())

	return cmd
}

// addFilesFlags registers the common flags on a cobra command.
func addFilesFlags(cmd *cobra.Command, flags *filesFlags) {
	cmd.Flags().StringVarP(&flags.agentName, "agent-name", "n", "", "Agent name (matches azure.yaml service name; auto-detected when only one exists)")
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

	info, err := resolveAgentServiceFromProject(ctx, azdClient, flags.agentName, rootFlags.NoPrompt)
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

	endpoint, err := resolveAgentEndpoint(ctx, "", "")
	if err != nil {
		return nil, err
	}

	sessionID, err := resolveStoredID(ctx, azdClient, info.AgentName, flags.session, false, "sessions", false)
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
	file       string
	targetPath string
}

// FilesUploadAction handles uploading a file to a session.
type FilesUploadAction struct {
	*AgentContext
	flags     *filesUploadFlags
	sessionID string
}

func newFilesUploadCommand() *cobra.Command {
	flags := &filesUploadFlags{}

	cmd := &cobra.Command{
		Use:   "upload",
		Short: "Upload a file to a hosted agent session.",
		Long: `Upload a file to a hosted agent session.

Reads a local file and uploads it to the specified remote path
in the session's filesystem. If --target-path is not provided,
the remote path defaults to the local file path.

Agent details are automatically resolved from the azd environment.`,
		Example: `  # Upload a file (remote path defaults to local path)
  azd ai agent files upload --file ./data/input.csv

  # Upload to a specific remote path
  azd ai agent files upload --file ./input.csv --target-path /data/input.csv

  # Upload with explicit agent name and session
  azd ai agent files upload --file ./input.csv --agent-name my-agent --session <session-id>`,
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
			}

			return action.Run(ctx)
		},
	}

	addFilesFlags(cmd, &flags.filesFlags)
	cmd.Flags().StringVarP(&flags.file, "file", "f", "", "Local file path to upload (required)")
	cmd.Flags().StringVarP(&flags.targetPath, "target-path", "t", "", "Remote destination path (defaults to local file path)")
	_ = cmd.MarkFlagRequired("file")

	return cmd
}

// Run executes the upload action.
func (a *FilesUploadAction) Run(ctx context.Context) error {
	remotePath := a.flags.targetPath
	if remotePath == "" {
		remotePath = a.flags.file
	}

	//nolint:gosec // G304: file path is provided by the user via CLI flag
	file, err := os.Open(a.flags.file)
	if err != nil {
		return fmt.Errorf("failed to open local file %q: %w", a.flags.file, err)
	}
	defer file.Close()

	agentClient, err := a.NewClient()
	if err != nil {
		return err
	}

	err = agentClient.UploadSessionFile(
		ctx,
		a.Name,
		a.sessionID,
		remotePath,
		DefaultVNextAgentAPIVersion,
		file,
	)
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}

	fmt.Printf("Uploaded %s → %s\n", a.flags.file, remotePath)
	return nil
}

// --- download ---

type filesDownloadFlags struct {
	filesFlags
	file       string
	targetPath string
}

// FilesDownloadAction handles downloading a file from a session.
type FilesDownloadAction struct {
	*AgentContext
	flags     *filesDownloadFlags
	sessionID string
}

func newFilesDownloadCommand() *cobra.Command {
	flags := &filesDownloadFlags{}

	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download a file from a hosted agent session.",
		Long: `Download a file from a hosted agent session.

Downloads a file from the specified remote path in the session's
filesystem and saves it locally. If --target-path is not provided,
the local path defaults to the basename of the remote file.

Agent details are automatically resolved from the azd environment.`,
		Example: `  # Download a file (local path defaults to remote filename)
  azd ai agent files download --file /data/output.csv

  # Download to a specific local path
  azd ai agent files download --file /data/output.csv --target-path ./output.csv

  # Download with explicit session
  azd ai agent files download --file /data/output.csv --session <session-id>`,
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
			}

			return action.Run(ctx)
		},
	}

	addFilesFlags(cmd, &flags.filesFlags)
	cmd.Flags().StringVarP(&flags.file, "file", "f", "", "Remote file path to download (required)")
	cmd.Flags().StringVarP(&flags.targetPath, "target-path", "t", "", "Local destination path (defaults to remote filename)")
	_ = cmd.MarkFlagRequired("file")

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
		a.sessionID,
		a.flags.file,
		DefaultVNextAgentAPIVersion,
	)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}
	defer body.Close()

	targetPath := a.flags.targetPath
	if targetPath == "" {
		targetPath = filepath.Base(a.flags.file)
	}

	//nolint:gosec // G304: targetPath is provided by the user via CLI flag
	outFile, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("failed to create output file %q: %w", targetPath, err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, body); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	fmt.Printf("Downloaded %s → %s\n", a.flags.file, targetPath)
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
	var filePath string

	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove a file or directory from a hosted agent session.",
		Long: `Remove a file or directory from a hosted agent session.

Removes the specified file or directory from the session's filesystem.
Use --recursive to remove directories and their contents.

Agent details are automatically resolved from the azd environment.`,
		Example: `  # Remove a file (agent auto-detected)
  azd ai agent files remove --file /data/old-file.csv

  # Remove a directory recursively
  azd ai agent files remove --file /data/temp --recursive

  # Remove with explicit session
  azd ai agent files remove --file /data/old-file.csv --session <session-id>`,
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
				remotePath:   filePath,
			}

			return action.Run(ctx)
		},
	}

	addFilesFlags(cmd, &flags.filesFlags)
	cmd.Flags().StringVarP(&filePath, "file", "f", "", "Remote file or directory path to remove")
	_ = cmd.MarkFlagRequired("file")
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

// --- mkdir ---

// FilesMkdirAction handles creating a directory in a session.
type FilesMkdirAction struct {
	*AgentContext
	sessionID  string
	remotePath string
}

func newFilesMkdirCommand() *cobra.Command {
	flags := &filesFlags{}
	var dirPath string

	cmd := &cobra.Command{
		Use:   "mkdir",
		Short: "Create a directory in a hosted agent session.",
		Long: `Create a directory in a hosted agent session.

Creates the specified directory in the session's filesystem.
Parent directories are created as needed.

Agent details are automatically resolved from the azd environment.`,
		Example: `  # Create a directory (agent auto-detected)
  azd ai agent files mkdir --dir /data/output

  # Create with explicit session
  azd ai agent files mkdir --dir /data/output --session <session-id>`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			fc, err := resolveFilesContext(ctx, flags)
			if err != nil {
				return err
			}

			action := &FilesMkdirAction{
				AgentContext: fc.AgentContext,
				sessionID:    fc.sessionID,
				remotePath:   dirPath,
			}

			return action.Run(ctx)
		},
	}

	addFilesFlags(cmd, flags)
	cmd.Flags().StringVarP(&dirPath, "dir", "d", "", "Remote directory path to create")
	_ = cmd.MarkFlagRequired("dir")

	return cmd
}

// Run executes the mkdir action.
func (a *FilesMkdirAction) Run(ctx context.Context) error {
	agentClient, err := a.NewClient()
	if err != nil {
		return err
	}

	err = agentClient.MkdirSessionFile(
		ctx,
		a.Name,
		a.sessionID,
		a.remotePath,
		DefaultVNextAgentAPIVersion,
	)
	if err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	fmt.Printf("Created %s\n", a.remotePath)
	return nil
}

// --- stat ---

type filesStatFlags struct {
	filesFlags
	output string
}

// FilesStatAction handles getting file/directory metadata from a session.
type FilesStatAction struct {
	*AgentContext
	flags      *filesStatFlags
	sessionID  string
	remotePath string
}

func newFilesStatCommand() *cobra.Command {
	flags := &filesStatFlags{}

	cmd := &cobra.Command{
		Use:   "stat <remote-path>",
		Short: "Get file or directory metadata in a hosted agent session.",
		Long: `Get file or directory metadata in a hosted agent session.

Returns metadata about the specified file or directory in the session's filesystem.

Agent details are automatically resolved from the azd environment.`,
		Example: `  # Get metadata for a file
  azd ai agent files stat /data/output.csv

  # Get metadata in table format
  azd ai agent files stat /data/output.csv --output table

  # Get metadata with explicit session
  azd ai agent files stat /data/output.csv --session <session-id>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			fc, err := resolveFilesContext(ctx, &flags.filesFlags)
			if err != nil {
				return err
			}

			action := &FilesStatAction{
				AgentContext: fc.AgentContext,
				flags:        flags,
				sessionID:    fc.sessionID,
				remotePath:   args[0],
			}

			return action.Run(ctx)
		},
	}

	addFilesFlags(cmd, &flags.filesFlags)
	cmd.Flags().StringVarP(&flags.output, "output", "o", "json", "Output format (json or table)")

	return cmd
}

// Run executes the stat action.
func (a *FilesStatAction) Run(ctx context.Context) error {
	agentClient, err := a.NewClient()
	if err != nil {
		return err
	}

	fileInfo, err := agentClient.StatSessionFile(
		ctx,
		a.Name,
		a.sessionID,
		a.remotePath,
		DefaultVNextAgentAPIVersion,
	)
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	if a.flags.output == "table" {
		return printFileInfoTable(fileInfo)
	}

	output, err := json.MarshalIndent(fileInfo, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to format output: %w", err)
	}

	fmt.Println(string(output))
	return nil
}

func printFileInfoTable(f *agent_api.SessionFileInfo) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPATH\tTYPE\tSIZE\tLAST MODIFIED")
	fmt.Fprintln(w, "----\t----\t----\t----\t-------------")

	fileType := "file"
	if f.IsDirectory {
		fileType = "dir"
	}
	modified := ""
	if f.LastModified != nil {
		modified = *f.LastModified
	}
	fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", f.Name, f.Path, fileType, f.Size, modified)

	return w.Flush()
}
