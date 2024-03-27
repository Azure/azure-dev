package cmd

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func toolActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("tool", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "tool",
			Short: "Manage tools.",
		},
	})

	group.Add("run", &actions.ActionDescriptorOptions{
		Command:        newToolRunCmd(),
		FlagsResolver:  newToolRunFlags,
		ActionResolver: newToolRunAction,
		OutputFormats:  []output.Format{output.NoneFormat},
	})

	group.Add("install", &actions.ActionDescriptorOptions{
		Command:        newToolInstallCmd(),
		FlagsResolver:  newToolInstallFlags,
		ActionResolver: newToolInstallAction,
		OutputFormats:  []output.Format{output.NoneFormat},
	})

	return group
}

func newToolRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run a tool.",
		Args:  cobra.MinimumNArgs(1),
	}
}

type toolRunFlags struct {
	global *internal.GlobalCommandOptions
}

func newToolRunFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *toolRunFlags {
	flags := &toolRunFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func (f *toolRunFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
}

func newToolRunAction(_ auth.LoggedInGuard, commandRunner exec.CommandRunner, args []string) actions.Action {
	return &toolRunAction{commandRunner: commandRunner}
}

type toolRunAction struct {
	commandRunner exec.CommandRunner
	args          []string
}

func (a *toolRunAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	userCfgDir, err := config.GetUserConfigDir()
	if err != nil {
		return nil, err
	}

	toolPath := filepath.Join(userCfgDir, "tools", a.args[0], fmt.Sprintf("azd-tool-%s", a.args[0]))
	if runtime.GOOS == "windows" {
		toolPath += ".exe"
	}

	_, err = os.Stat(toolPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf(
			"tool %s not found. You can try to install it with `azd tool install %s`", a.args[0], a.args[0])
	} else if err != nil {
		return nil, err
	}

	runArgs := exec.NewRunArgs(toolPath, a.args[:1]...)

	_, err = a.commandRunner.Run(ctx, runArgs)
	return nil, err
}

func newToolInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install a tool.",
		Args:  cobra.ExactArgs(1),
	}
}

type toolInstallFlags struct {
	global *internal.GlobalCommandOptions
}

func newToolInstallFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *toolInstallFlags {
	flags := &toolInstallFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func (f *toolInstallFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
}

func newToolInstallAction(console input.Console, args []string) actions.Action {
	return &toolInstallAction{console: console, args: args}
}

type toolInstallAction struct {
	console input.Console
	args    []string
}

func (a *toolInstallAction) Run(ctx context.Context) (_ *actions.ActionResult, retErr error) {
	toolUrl := os.Getenv("AZD_DEBUG_TOOL_ARCHIVE_URL")
	if toolUrl == "" {
		return nil,
			errors.New("tool discovery is not supported yet, set the environment variaible AZD_DEBUG_TOOL_ARCHIVE_URL to" +
				" URL where the tool archive is located")
	}

	a.console.ShowSpinner(ctx, fmt.Sprintf("Downloading %s", a.args[0]), input.Step)
	defer func() {
		a.console.StopSpinner(ctx, fmt.Sprintf("Downloading %s", a.args[0]), input.GetStepResultFormat(retErr))
	}()

	archivePath, err := os.MkdirTemp("", fmt.Sprintf("%s.tmp*", a.args[0]))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "GET", toolUrl, nil)
	if err != nil {
		return nil, err
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	toolArchive, err := os.Create(archivePath)
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = toolArchive.Close()
		_ = os.Remove(archivePath)
	}()

	size, err := io.Copy(toolArchive, req.Body)
	if err != nil {
		return nil, err
	}

	toolArchive.Seek(0, 0)
	zipReader, err := zip.NewReader(toolArchive, size)
	if err != nil {
		return nil, err
	}

	userCfgDir, err := config.GetUserConfigDir()
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Join(userCfgDir, "tools"), osutil.PermissionDirectoryOwnerOnly); err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp(filepath.Join(userCfgDir, "tools"), fmt.Sprintf("%s.tmp*", a.args[0]))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = os.RemoveAll(dir)
	}()

	for _, file := range zipReader.File {
		// In a function so `defer` runs once per turn of the loop instead of when `Run` exits.
		err := func() error {
			// nolint: gosec
			filePath := filepath.Join(dir, file.Name)
			if file.FileInfo().IsDir() {
				err := os.MkdirAll(filePath, file.Mode())
				if err != nil {
					return err
				}
				return nil
			}

			fileReader, err := file.Open()
			if err != nil {
				return err
			}

			defer fileReader.Close()

			fileToWrite, err := os.OpenFile(filePath, os.O_CREATE|os.O_TRUNC, file.Mode())
			if err != nil {
				return err
			}

			defer fileToWrite.Close()

			// nolint: gosec
			_, err = io.Copy(fileToWrite, fileReader)
			if err != nil {
				return err
			}

			return nil
		}()

		if err != nil {
			return nil, err
		}
	}

	return nil, os.Rename(dir, filepath.Join(userCfgDir, "tools", a.args[0]))
}
