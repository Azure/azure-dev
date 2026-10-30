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
	"runtime"
	"strings"
	"syscall"
	"time"

	"azure.ai.rle/internal/project"
	"azure.ai.rle/internal/ui"

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
		Short: "Build and run the local RLE environment container (gym only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&localRunAction{cmd: cmd, flags: flags}).Run()
		},
	}

	cmd.Flags().IntVar(
		&flags.port,
		"port",
		defaultPort,
		"Host port mapped to the local container (1-65535). Defaults to 8000.",
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

	config, err := loadLocalRunConfig(a.flags)
	if err != nil {
		return err
	}
	baseUrl, err := ensureLocalContainerEndpointForName(a.cmd, a.flags, config.Rle.Name)
	if err != nil {
		return err
	}
	defer func() {
		if err := stopLocalContainer(a.cmd, config.Rle.Name); err != nil {
			_, _ = fmt.Fprintf(a.cmd.ErrOrStderr(), "Warning: failed to stop local container: %v\n", err)
		} else {
			_, _ = fmt.Fprintln(a.cmd.ErrOrStderr(), "Local runtime container stopped successfully.")
		}
	}()

	runtimeSession := project.NewWebSocketRuntimeSession(baseUrl, 0, nil)
	defer runtimeSession.Close()

	watchDone := make(chan error, 1)
	if a.flags.watch {
		watchCtx, cancelWatch := context.WithCancel(ctx)
		defer cancelWatch()
		watchCmd := *a.cmd
		watchCmd.SetContext(watchCtx)
		go func() {
			watchDone <- watchLocalContainer(&watchCmd, a.flags, runtimeSession)
		}()
	}

	webUrl, stopPlayground, err := playgroundURLWithAuthorizationProvider(
		ctx,
		baseUrl,
		nil,
		runtimeSession,
	)
	if err != nil {
		return err
	}
	defer stopPlayground()
	_, err = fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"Local RLE environment is running at %s\nPlayground UI: %s\n",
		baseUrl,
		webUrl,
	)
	if err != nil {
		return err
	}
	if err := ui.OpenBrowser(webUrl); err != nil {
		_, _ = fmt.Fprintf(a.cmd.ErrOrStderr(), "Warning: failed to open playground UI: %v\n", err)
	}
	shellErr := project.RunWebSocketShellWithSession(
		ctx,
		a.cmd.InOrStdin(),
		a.cmd.OutOrStdout(),
		runtimeSession,
	)
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
	// Winsock uses a different code from syscall.EADDRINUSE.
	windowsAddressInUse = syscall.Errno(10048)
)

func ensureLocalContainerEndpoint(cmd *cobra.Command, flags *localRunFlags) (string, error) {
	config, err := loadLocalRunConfig(flags)
	if err != nil {
		return "", err
	}
	return ensureLocalContainerEndpointForName(cmd, flags, config.Rle.Name)
}

func ensureLocalContainerEndpointForName(
	cmd *cobra.Command,
	flags *localRunFlags,
	environmentName string,
) (string, error) {
	port := flags.port
	if port < 1 || port > 65535 {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("--port must be between 1 and 65535, got %d.", port),
			Code:       "rle_invalid_local_port",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Pass a valid host port, for example --port 8000.",
		}
	}
	containerRuntime := project.ContainerRuntime()
	if _, err := exec.LookPath(containerRuntime); err != nil {
		return "", &azdext.LocalError{
			Message:  fmt.Sprintf("Could not find container runtime %q.", containerRuntime),
			Code:     "rle_container_runtime_not_found",
			Category: azdext.LocalErrorCategoryUser,
			Suggestion: "Install the selected container runtime and check AZD_CONTAINER_RUNTIME " +
				"(defaults to docker). Put the executable on PATH or set its full path.",
		}
	}

	image := localRuntimeImageForRun(environmentName)
	container := localContainerName(environmentName)
	baseUrl := fmt.Sprintf("http://localhost:%d", port)

	if running, exists := project.ContainerStatus(cmd.Context(), container); exists {
		if running && flags.reuseRunning && !flags.restart {
			if err := project.WaitForHealth(baseUrl, 30*time.Second); err != nil {
				return "", err
			}
			return baseUrl, nil
		}
		_ = project.RunDocker(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), "rm", "-f", container)
	}
	if err := ensurePortAvailable(port); err != nil {
		return "", err
	}
	if err := project.BuildRuntimeImage(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), image, project.BuildOptions{
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
		"-p", portMapping,
		image,
	}
	if err := project.RunDocker(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), runArgs...); err != nil {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Failed to start local container %q with %q: %v", container, containerRuntime, err),
			Code:       "rle_local_docker_run_failed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: localPortSuggestion(port),
		}
	}
	started := true
	cleanupStartedContainer := func() {
		if started {
			_ = project.RunDocker(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), "rm", "-f", container)
		}
	}

	if err := project.WaitForHealth(baseUrl, 30*time.Second); err != nil {
		cleanupStartedContainer()
		return "", err
	}
	started = false
	return baseUrl, nil
}

func ensurePortAvailable(port int) error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return localPortBindError(port, err)
	}
	return listener.Close()
}

func localPortBindError(port int, err error) error {
	if errors.Is(err, syscall.EADDRINUSE) ||
		(runtime.GOOS == "windows" && errors.Is(err, windowsAddressInUse)) {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("Port %d is already in use.", port),
			Code:       "rle_local_port_in_use",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: localPortSuggestion(port),
		}
	}
	return fmt.Errorf("%w: %w", &azdext.LocalError{
		Message:    fmt.Sprintf("Could not bind local port %d.", port),
		Code:       "rle_local_port_bind_failed",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Check the bind error and local network permissions, or select another port with --port.",
	}, err)
}

func localPortSuggestion(port int) string {
	return fmt.Sprintf(
		"Check containers using this port with %q: ps --filter \"publish=%d\" --format \"table {{.Names}}\\t{{.Ports}}\"\n"+
			"Stop the container with the same runtime: rm -f <container>\n"+
			"Then rerun: azd ai rle run --port %d\n"+
			"If the runtime does not show a container, check which process is using port %d.",
		project.ContainerRuntime(),
		port,
		port,
		port,
	)
}

func stopLocalContainer(cmd *cobra.Command, environmentName string) error {
	container := localContainerName(environmentName)
	return project.RunDocker(cmd.Context(), io.Discard, cmd.ErrOrStderr(), "rm", "-f", container)
}

func loadLocalRunConfig(flags *localRunFlags) (project.RleConfig, error) {
	source := strings.TrimSpace(flags.source)
	if source == "" {
		source = "."
	}
	config, err := project.LoadRleConfig(source)
	if err != nil {
		return project.RleConfig{}, err
	}
	if err := ensureLocalRunSupported(config); err != nil {
		return project.RleConfig{}, err
	}
	return config, nil
}

// ensureLocalRunSupported rejects a manifest this command cannot drive.
//
// run starts the container and then talks to it over the gym
// WebSocket session, which only a Gym: OpenEnv container serves. A harness
// container exposes plain HTTP routes that RLE itself calls, and the harness
// that would drive them is a separate deployment this command knows nothing
// about, so there is no local loop for run to run. Without this check the
// command builds the image, starts the container, and only then fails at the
// WebSocket handshake, reporting a connection error that says nothing about
// the real problem.
//
// The check is here rather than in the action so that the watch/restart path,
// which reloads the manifest through this same function, cannot bypass it.
func ensureLocalRunSupported(config project.RleConfig) error {
	if config.Harness.Type == project.HarnessTypeGym {
		return nil
	}
	return &azdext.LocalError{
		Message: fmt.Sprintf(
			"azd ai rle run supports only %s environments, and rle.toml declares %s.",
			project.HarnessTypeGym,
			config.Harness.Type,
		),
		Code:     "rle_run_unsupported_type",
		Category: azdext.LocalErrorCategoryUser,
		Suggestion: "run drives the container over the OpenEnv WebSocket session, which a harness " +
			"container does not serve. To exercise a byoh harness, deploy the agent, point rle.toml's " +
			"harness endpoint at it, run azd ai rle publish, then azd ai rle rollout.",
	}
}

func localRuntimeImageForRun(environmentName string) string {
	return project.Slug(environmentName) + ":local"
}

func watchLocalContainer(
	cmd *cobra.Command,
	flags *localRunFlags,
	runtimeSession *project.WebSocketRuntimeSession,
) error {
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
			runtimeSession.Restart(baseUrl)
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

func localContainerName(envName string) string {
	return "azd-rle-" + project.Slug(firstNonEmpty(envName, "environment"))
}
