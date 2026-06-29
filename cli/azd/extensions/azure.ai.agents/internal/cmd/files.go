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
	"slices"
	"text/tabwriter"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// filesFlags holds the common flags shared by all file subcommands.
type filesFlags struct {
	userIdentityFlags
	agentName string // optional: agent name (matches azure.yaml service name)
	session   string // optional: explicit session ID override
}

func newFilesCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "files",
		Short: "Manage files in a hosted agent session.",
		Long: `Manage files in a hosted agent session.

Upload, download, list, and delete files in the session-scoped filesystem
of a hosted agent. This is useful for debugging, seeding data, and agent setup.

Agent details (name, endpoint) are automatically resolved from the
azd environment. Use --agent-name to select a specific agent when the project
has multiple azure.ai.agent services. The session ID is automatically resolved
from the last invoke session, or can be overridden with --session-id.

For agents configured with header-based isolation, pass --user-identity
on each file operation.`,
	}

	cmd.AddCommand(newFilesUploadCommand(extCtx))
	cmd.AddCommand(newFilesDownloadCommand(extCtx))
	cmd.AddCommand(newFilesListCommand(extCtx))
	cmd.AddCommand(newFilesRemoveCommand(extCtx))
	cmd.AddCommand(newFilesMkdirCommand(extCtx))
	cmd.AddCommand(newFilesStatCommand(extCtx))

	return cmd
}

// addFilesFlags registers the common flags on a cobra command.
func addFilesFlags(cmd *cobra.Command, flags *filesFlags) {
	cmd.Flags().StringVarP(&flags.agentName, "agent-name", "n", "", "Agent name (matches azure.yaml service name; auto-detected when only one exists)")
	cmd.Flags().StringVarP(&flags.session, "session-id", "s", "", "Session ID override (defaults to last invoke session)")
	addUserIdentityFlag(cmd, &flags.userIdentityFlags)
}

// filesContext holds the resolved agent context and session for file operations.
type filesContext struct {
	*AgentContext
	sessionID string
}

// resolveFilesContext resolves agent details and session from the azd environment.
func resolveFilesContext(ctx context.Context, flags *filesFlags, noPrompt bool) (*filesContext, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	info, err := resolveAgentServiceFromProject(ctx, azdClient, flags.agentName, noPrompt)
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

	var sessionID string
	if info.AgentEndpoint != "" {
		sessionID, err = resolveStoredID(
			ctx, azdClient, buildRemoteAgentKeyFromEndpoint(info.AgentEndpoint),
			flags.session, false, "sessions", false,
			legacyKeysForRemote(info.AgentName)...,
		)
		if err != nil {
			return nil, err
		}
	} else if flags.session != "" {
		sessionID = flags.session
	}

	return &filesContext{
		AgentContext: &AgentContext{
			ProjectEndpoint: endpoint,
			Name:            info.AgentName,
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

// agentNameMisusedAsFilePositional reports whether the positional value passed
// to `files upload` looks like an agent name rather than a file path: it does
// not exist as a local file but matches one of the declared agent service
// names. This is the common mistake of mirroring `azd ai agent invoke <agent>`,
// where the positional is the agent. For `files upload` the positional is the
// file to upload, so this lets us fail fast with a hint instead of falling
// through to agent auto-detect and hanging on the interactive service picker.
func agentNameMisusedAsFilePositional(positional string, agentNames []string) bool {
	if positional == "" {
		return false
	}

	// A real, existing local file is always a valid upload target.
	if _, err := os.Stat(positional); err == nil {
		return false
	}

	return slices.Contains(agentNames, positional)
}

// errAgentNameAsFilePositional builds the corrective validation error returned
// when a user passes an agent name where `files upload` expects a file path.
func errAgentNameAsFilePositional(positional string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidPositionalArg,
		fmt.Sprintf("%q is an agent name, but the positional argument to `files upload` is the file to upload", positional),
		fmt.Sprintf("Pass the agent with -n and the file with -f, e.g. azd ai agent files upload -n %s -f <file>", positional),
	)
}

// checkUploadPositionalNotAgentName guards `files upload` against the common
// mistake of passing an agent name as the positional argument. When the
// positional does not exist locally but matches an azure.ai.agent service in
// azure.yaml, it returns a fail-fast validation error. Otherwise it returns nil
// (including when the project cannot be loaded), letting the normal flow run.
func checkUploadPositionalNotAgentName(ctx context.Context, positional string) error {
	if positional == "" {
		return nil
	}

	// A real, existing local file is always a valid upload target; skip the
	// project lookup entirely so the happy path stays fast.
	if _, err := os.Stat(positional); err == nil {
		return nil
	}

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		// Can't verify against the project; let the normal flow surface a
		// clearer error (e.g. "failed to open local file").
		return nil
	}
	defer azdClient.Close()

	if !agentNameMisusedAsFilePositional(positional, agentServiceNames(ctx, azdClient)) {
		return nil
	}

	return errAgentNameAsFilePositional(positional)
}

func newFilesUploadCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &filesUploadFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "upload [file]",
		Short: "Upload a file to a hosted agent session.",
		Long: `Upload a file to a hosted agent session.

Reads a local file and uploads it to the specified remote path
in the session's filesystem. If --target-path is not provided,
the remote path defaults to the local filename.

The positional argument is the local FILE to upload, not the agent name.
Select the agent with --agent-name/-n when the project has multiple
azure.ai.agent services.

Agent details are automatically resolved from the azd environment.`,
		Example: `  # Upload a file (remote path defaults to filename)
  azd ai agent files upload ./data/input.csv

  # Upload to a specific remote path
  azd ai agent files upload ./input.csv --target-path /data/input.csv

  # Upload selecting a specific agent (file via -f, agent via -n)
  azd ai agent files upload --file ./input.csv --agent-name my-agent`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			// Guard against the common mistake of passing an agent name as the
			// positional (mirroring `azd ai agent invoke <agent>`). The upload
			// positional is the FILE to upload, so fail fast with a hint instead
			// of falling through to agent auto-detect and hanging on the
			// interactive service picker in multi-service projects. Inspect the
			// raw positional so the check still fires when the file came from -f.
			if len(args) > 0 {
				if err := checkUploadPositionalNotAgentName(ctx, args[0]); err != nil {
					return err
				}
			}

			if len(args) > 0 && flags.file == "" {
				flags.file = args[0]
			}
			if flags.file == "" {
				return fmt.Errorf(
					"file path is required as a positional argument " +
						"or via --file",
				)
			}

			fc, err := resolveFilesContext(ctx, &flags.filesFlags, extCtx.NoPrompt)
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
	cmd.Flags().StringVarP(&flags.file, "file", "f", "", "Local file path to upload")
	cmd.Flags().StringVarP(&flags.targetPath, "target-path", "t", "", "Remote destination path (defaults to local filename)")

	return cmd
}

// Run executes the upload action.
func (a *FilesUploadAction) Run(ctx context.Context) error {
	remotePath := a.flags.targetPath
	if remotePath == "" {
		remotePath = filepath.Base(a.flags.file)
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
		DefaultAgentAPIVersion,
		file,
		a.flags.sessionRequestOptions(),
	)
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}

	fmt.Printf("Uploaded %s -> %s\n", a.flags.file, remotePath)
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

func newFilesDownloadCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &filesDownloadFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "download [file]",
		Short: "Download a file from a hosted agent session.",
		Long: `Download a file from a hosted agent session.

Downloads a file from the specified remote path in the session's
filesystem and saves it locally. If --target-path is not provided,
the local path defaults to the basename of the remote file.

Agent details are automatically resolved from the azd environment.`,
		Example: `  # Download a file (local path defaults to remote filename)
  azd ai agent files download /data/output.csv

  # Download to a specific local path
  azd ai agent files download /data/output.csv --target-path ./output.csv

  # Download with flags
  azd ai agent files download --file /data/output.csv --session-id <session-id>`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			if len(args) > 0 && flags.file == "" {
				flags.file = args[0]
			}
			if flags.file == "" {
				return fmt.Errorf(
					"file path is required as a positional argument " +
						"or via --file",
				)
			}

			fc, err := resolveFilesContext(ctx, &flags.filesFlags, extCtx.NoPrompt)
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
	cmd.Flags().StringVarP(&flags.file, "file", "f", "", "Remote file path to download")
	cmd.Flags().StringVarP(&flags.targetPath, "target-path", "t", "", "Local destination path (defaults to remote filename)")

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
		DefaultAgentAPIVersion,
		a.flags.sessionRequestOptions(),
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

	fmt.Printf("Downloaded %s -> %s\n", a.flags.file, targetPath)
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

func newFilesListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &filesListFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:     "list [remote-path]",
		Aliases: []string{"ls"},
		Short:   "List files in a hosted agent session.",
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
  azd ai agent files list --session-id <session-id>`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.output = extCtx.OutputFormat

			ctx := azdext.WithAccessToken(cmd.Context())

			fc, err := resolveFilesContext(ctx, &flags.filesFlags, extCtx.NoPrompt)
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
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "table"},
		Default:       "json",
	})

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
		DefaultAgentAPIVersion,
		a.flags.sessionRequestOptions(),
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
			modified = f.LastModified.String()
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

func newFilesRemoveCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &filesRemoveFlags{}
	var filePath string
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:     "delete [file]",
		Aliases: []string{"remove", "rm"},
		Short:   "Delete a file or directory from a hosted agent session.",
		Long: `Delete a file or directory from a hosted agent session.

Deletes the specified file or directory from the session's filesystem.
Use --recursive to delete directories and their contents.

Agent details are automatically resolved from the azd environment.`,
		Example: `  # Delete a file (agent auto-detected)
  azd ai agent files delete /data/old-file.csv

  # Delete a directory recursively
  azd ai agent files delete /data/temp --recursive

  # Delete with flags
  azd ai agent files delete --file /data/old-file.csv --session-id <session-id>`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			if len(args) > 0 && filePath == "" {
				filePath = args[0]
			}
			if filePath == "" {
				return fmt.Errorf(
					"file path is required as a positional argument " +
						"or via --file",
				)
			}

			fc, err := resolveFilesContext(ctx, &flags.filesFlags, extCtx.NoPrompt)
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
	cmd.Flags().StringVarP(&filePath, "file", "f", "", "Remote file or directory path to delete")
	cmd.Flags().BoolVar(&flags.recursive, "recursive", false, "Recursively delete directories and their contents")

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
		DefaultAgentAPIVersion,
		a.flags.sessionRequestOptions(),
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
	sessionID      string
	remotePath     string
	requestOptions *agent_api.SessionRequestOptions
}

func newFilesMkdirCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &filesFlags{}
	var dirPath string
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "mkdir [dir]",
		Short: "Create a directory in a hosted agent session.",
		Long: `Create a directory in a hosted agent session.

Creates the specified directory in the session's filesystem.
Parent directories are created as needed.

Agent details are automatically resolved from the azd environment.`,
		Example: `  # Create a directory (agent auto-detected)
  azd ai agent files mkdir /data/output

  # Create with flags
  azd ai agent files mkdir --dir /data/output --session-id <session-id>`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			if len(args) > 0 && dirPath == "" {
				dirPath = args[0]
			}
			if dirPath == "" {
				return fmt.Errorf(
					"directory path is required as a positional " +
						"argument or via --dir",
				)
			}

			fc, err := resolveFilesContext(ctx, flags, extCtx.NoPrompt)
			if err != nil {
				return err
			}

			action := &FilesMkdirAction{
				AgentContext:   fc.AgentContext,
				sessionID:      fc.sessionID,
				remotePath:     dirPath,
				requestOptions: flags.sessionRequestOptions(),
			}

			return action.Run(ctx)
		},
	}

	addFilesFlags(cmd, flags)
	cmd.Flags().StringVarP(&dirPath, "dir", "d", "", "Remote directory path to create")

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
		DefaultAgentAPIVersion,
		a.requestOptions,
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

func newFilesStatCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &filesStatFlags{}
	extCtx = ensureExtensionContext(extCtx)

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
  azd ai agent files stat /data/output.csv --session-id <session-id>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.output = extCtx.OutputFormat

			ctx := azdext.WithAccessToken(cmd.Context())

			fc, err := resolveFilesContext(ctx, &flags.filesFlags, extCtx.NoPrompt)
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
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "table"},
		Default:       "json",
	})

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
		DefaultAgentAPIVersion,
		a.flags.sessionRequestOptions(),
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
		modified = f.LastModified.String()
	}
	fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", f.Name, f.Path, fileType, f.Size, modified)

	return w.Flush()
}
