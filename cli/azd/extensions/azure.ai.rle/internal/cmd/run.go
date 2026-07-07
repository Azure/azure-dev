// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	rleproject "azure.ai.rle/internal/project"
	rleui "azure.ai.rle/internal/ui"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type localRunFlags struct {
	port         int
	source       string
	dockerfile   string
	watch        bool
	restart      bool
	reuseRunning bool
}

type localRunAction struct {
	cmd   *cobra.Command
	flags *localRunFlags
}

func newRunCommand() *cobra.Command {
	flags := &localRunFlags{
		reuseRunning: true,
	}

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Build and run the local RLE environment container",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&localRunAction{cmd: cmd, flags: flags}).Run()
		},
	}

	cmd.Flags().IntVar(
		&flags.port,
		"port",
		0,
		"Host port mapped to the local Docker container. Defaults to 8000.",
	)
	cmd.Flags().StringVar(
		&flags.dockerfile,
		"dockerfile",
		"",
		"Dockerfile path relative to the current folder. Defaults to Dockerfile at the source root or server/Dockerfile.",
	)
	cmd.Flags().BoolVar(
		&flags.watch,
		"watch",
		false,
		"Watch source files and rebuild/restart the local container when they change.",
	)
	return cmd
}

func (a *localRunAction) Run() error {
	ctx, stopSignals := signal.NotifyContext(a.cmd.Context(), os.Interrupt)
	defer stopSignals()

	baseUrl, err := ensureLocalContainerEndpoint(a.cmd, a.flags)
	if err != nil {
		return err
	}
	state, err := loadLocalRunState(a.flags, a.cmd.OutOrStdout())
	if err != nil {
		return err
	}
	defer func() {
		if err := stopLocalContainer(a.cmd, state.Name); err != nil {
			_, _ = fmt.Fprintf(a.cmd.ErrOrStderr(), "Warning: failed to stop local container: %v\n", err)
		}
	}()

	watchDone := make(chan error, 1)
	if a.flags.watch {
		watchCtx, cancelWatch := context.WithCancel(ctx)
		defer cancelWatch()
		watchCmd := *a.cmd
		watchCmd.SetContext(watchCtx)
		go func() {
			watchDone <- watchLocalContainer(&watchCmd, a.flags)
		}()
	}

	webUrl := baseUrl + "/web"
	_, err = fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"Local RLE environment is running at %s\nPlayground UI: %s\n",
		baseUrl,
		webUrl,
	)
	if err != nil {
		return err
	}
	if err := rleui.OpenBrowser(webUrl); err != nil {
		_, _ = fmt.Fprintf(a.cmd.ErrOrStderr(), "Warning: failed to open playground UI: %v\n", err)
	}
	shellErr := rleproject.RunShellWithContext(ctx, a.cmd.InOrStdin(), a.cmd.OutOrStdout(), baseUrl, 0)
	if a.flags.watch {
		select {
		case err := <-watchDone:
			if err != nil && shellErr == nil {
				return err
			}
		default:
		}
	}
	return shellErr
}

const (
	defaultPort              = 8000
	localContainerImageLabel = "azd.ai.rle.local-image"
)

func ensureLocalContainerEndpoint(cmd *cobra.Command, flags *localRunFlags) (string, error) {
	state, err := loadLocalRunState(flags, cmd.OutOrStdout())
	if err != nil {
		return "", err
	}
	port := resolvePort(flags)
	if port <= 0 {
		return "", &azdext.LocalError{
			Message:    "--port must be greater than 0.",
			Code:       "rle_invalid_local_port",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Pass a valid host port, for example --port 8000.",
		}
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return "", &azdext.LocalError{
			Message:    "Could not find \"docker\" on PATH.",
			Code:       "rle_docker_not_found",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Install/start Docker Desktop, then retry the command.",
		}
	}

	image := localRuntimeImageForRun(flags, state)
	container := localContainerName(state.Name)
	baseUrl := fmt.Sprintf("http://localhost:%d", port)

	if running, exists := rleproject.ContainerStatus(cmd.Context(), container); exists {
		if running && flags.reuseRunning && !flags.restart {
			if err := rleproject.WaitForHealth(baseUrl, 30*time.Second); err != nil {
				return "", err
			}
			return baseUrl, nil
		}
		_ = rleproject.RunDocker(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), "rm", "-f", container)
	}
	if err := ensurePortAvailable(port); err != nil {
		return "", err
	}
	if err := rleproject.BuildRuntimeImage(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), image, rleproject.BuildOptions{
		Source:     flags.source,
		Dockerfile: flags.dockerfile,
	}); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintf(
		cmd.ErrOrStderr(),
		"Starting local container %s on port %d ...\n",
		container,
		port,
	); err != nil {
		return "", err
	}
	portMapping := fmt.Sprintf("%d:8000", port)
	runArgs := []string{
		"run", "-d",
		"--name", container,
		"--label", localContainerImageLabel + "=" + image,
		"-e", "ENABLE_WEB_INTERFACE=true",
		"-p", portMapping,
		image,
	}
	if err := rleproject.RunDocker(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), runArgs...); err != nil {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Failed to start local Docker container %q: %v", container, err),
			Code:       "rle_local_docker_run_failed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: localPortSuggestion(port),
		}
	}
	started := true
	cleanupStartedContainer := func() {
		if started {
			_ = rleproject.RunDocker(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), "rm", "-f", container)
		}
	}

	if err := rleproject.WaitForHealth(baseUrl, 30*time.Second); err != nil {
		cleanupStartedContainer()
		return "", err
	}
	started = false
	return baseUrl, nil
}

func ensurePortAvailable(port int) error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("Port %d is already in use.", port),
			Code:       "rle_local_port_in_use",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: localPortSuggestion(port),
		}
	}
	return listener.Close()
}

func localPortSuggestion(port int) string {
	return fmt.Sprintf(
		"Check containers using this port: docker ps --filter \"publish=%d\" --format \"table {{.Names}}\\t{{.Ports}}\"\n"+
			"Stop the container: docker rm -f <container>\n"+
			"Then rerun: azd ai rle run --port %d\n"+
			"If Docker does not show a container, check the process with: netstat -ano | findstr :%d",
		port,
		port,
		port,
	)
}

func stopLocalContainer(cmd *cobra.Command, environmentName string) error {
	container := localContainerName(environmentName)
	return rleproject.RunDocker(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), "rm", "-f", container)
}

func loadLocalRunState(flags *localRunFlags, output io.Writer) (rleState, error) {
	state, err := loadRleState()
	if err != nil {
		if localErr, ok := errors.AsType[*azdext.LocalError](err); !ok ||
			localErr.Code != "rle_project_not_initialized" {
			return rleState{}, err
		}
		state = defaultRleState(defaultSourceName(flags.source))
		if _, err := fmt.Fprintf(output, "No %s found; using current folder as the RLE source.\n", rleStateFile); err != nil {
			return rleState{}, err
		}
		if err := saveRleState(state); err != nil {
			return rleState{}, err
		}
		if _, err := fmt.Fprintf(output, "Created %s with name %q.\n", rleStateFile, state.Name); err != nil {
			return rleState{}, err
		}
	}

	state.Name = firstNonEmpty(state.Name, defaultSourceName(flags.source))
	return state, nil
}

func localRuntimeImageForRun(flags *localRunFlags, state rleState) string {
	return rleproject.Slug(firstNonEmpty(state.Name, defaultSourceName(flags.source))) + ":local"
}

func defaultSourceName(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "."
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return "rle_env"
	}
	name := filepath.Base(abs)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "rle_env"
	}
	return rleproject.Slug(name)
}

func watchLocalContainer(cmd *cobra.Command, flags *localRunFlags) error {
	last, err := sourceSnapshot(flags.source)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Watching for source changes. Press Ctrl+C to stop."); err != nil {
		return err
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-cmd.Context().Done():
			return nil
		case <-ticker.C:
			current, err := sourceSnapshot(flags.source)
			if err != nil {
				return err
			}
			if current == last {
				continue
			}
			last = current
			if _, err := fmt.Fprintln(
				cmd.OutOrStdout(),
				"Source change detected; rebuilding local container ...",
			); err != nil {
				return err
			}
			restartFlags := *flags
			restartFlags.restart = true
			baseUrl, err := ensureLocalContainerEndpoint(cmd, &restartFlags)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Local RLE environment restarted at %s\n", baseUrl); err != nil {
				return err
			}
		}
	}
}

func sourceSnapshot(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "."
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	var latest int64
	var count int
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && shouldSkipWatchDir(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		count++
		if modified := info.ModTime().UnixNano(); modified > latest {
			latest = modified
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", latest, count), nil
}

func shouldSkipWatchDir(name string) bool {
	switch name {
	case ".azd", ".git", ".venv", "__pycache__", "node_modules", "venv":
		return true
	default:
		return false
	}
}

func resolvePort(flags *localRunFlags) int {
	if flags.port > 0 {
		return flags.port
	}
	return defaultPort
}

func localContainerName(envName string) string {
	return "azd-rle-" + rleproject.Slug(firstNonEmpty(envName, "environment"))
}
