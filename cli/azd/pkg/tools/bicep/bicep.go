// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

// Version is the minimum version of bicep that we require (and the one we fetch when we fetch bicep on behalf of a
// user).
var Version semver.Version = semver.MustParse("0.41.2")

// Cli is a wrapper around the bicep CLI.
// The CLI automatically ensures bicep is installed before executing commands.
//
// Concurrency notes: The sync.Once is per-instance, not global. In normal operation, the IoC
// container registers Cli as a singleton, so one shared instance is used. However, some code paths
// (e.g., service_target_containerapp.go) create new instances inline. If multiple instances race to
// install bicep concurrently, this is safe because downloadBicep uses atomic file operations
// (temp file + rename). The only downside is potentially redundant downloads, which is rare and harmless.
type Cli struct {
	path        string
	runner      exec.CommandRunner
	console     input.Console
	transporter policy.Transporter

	installOnce sync.Once
	installErr  error
}

// NewCli creates a new Bicep CLI wrapper.
// The CLI automatically ensures bicep is installed when Build or BuildBicepParam is called.
func NewCli(console input.Console, commandRunner exec.CommandRunner) *Cli {
	return newCliWithTransporter(console, commandRunner, http.DefaultClient)
}

// newCliWithTransporter is like NewCli but allows providing a custom transport for testing.
func newCliWithTransporter(
	console input.Console,
	commandRunner exec.CommandRunner,
	transporter policy.Transporter,
) *Cli {
	return &Cli{
		runner:      commandRunner,
		console:     console,
		transporter: transporter,
	}
}

// ensureInstalledOnce checks if bicep is available and downloads/upgrades if needed.
// This is safe to call multiple times; installation only happens once.
func (cli *Cli) ensureInstalledOnce(ctx context.Context) error {
	cli.installOnce.Do(func() {
		cli.installErr = cli.ensureInstalled(ctx)
	})
	return cli.installErr
}

func (cli *Cli) ensureInstalled(ctx context.Context) error {
	if override := os.Getenv("AZD_BICEP_TOOL_PATH"); override != "" {
		log.Printf("using external bicep tool: %s", override)
		cli.path = override
		return nil
	}

	bicepPath, err := azdBicepPath()
	if err != nil {
		return fmt.Errorf("finding bicep: %w", err)
	}

	if _, err = os.Stat(bicepPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("finding bicep: %w", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(bicepPath), osutil.PermissionDirectory); err != nil {
			return fmt.Errorf("downloading bicep: %w", err)
		}

		if err := runStep(
			ctx, cli.console, "Downloading Bicep", func() error {
				return downloadBicep(ctx, cli.transporter, Version, bicepPath)
			},
		); err != nil {
			return fmt.Errorf("downloading bicep: %w", err)
		}
	}

	cli.path = bicepPath

	ver, err := cli.version(ctx)
	if err != nil {
		return fmt.Errorf("checking bicep version: %w", err)
	}

	log.Printf("bicep version: %s", ver)

	if ver.LT(Version) {
		log.Printf("installed bicep version %s is older than %s; updating.", ver.String(), Version.String())

		if err := runStep(
			ctx, cli.console, "Upgrading Bicep", func() error {
				return downloadBicep(ctx, cli.transporter, Version, bicepPath)
			},
		); err != nil {
			return fmt.Errorf("upgrading bicep: %w", err)
		}
	}

	log.Printf("using local bicep: %s", bicepPath)

	return nil
}

// runStep runs a long running operation, using the console to show a spinner for progress and status.
func runStep(ctx context.Context, console input.Console, title string, action func() error) error {
	console.ShowSpinner(ctx, title, input.Step)
	err := action()

	if err != nil {
		console.StopSpinner(ctx, title, input.StepFailed)
		return err
	}

	console.StopSpinner(ctx, title, input.StepDone)
	return nil
}

// azdBicepPath returns the path where we store our local copy of bicep ($AZD_CONFIG_DIR/bin).
func azdBicepPath() (string, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return "", err
	}

	if runtime.GOOS == "windows" {
		return filepath.Join(configDir, "bin", "bicep.exe"), nil
	}

	return filepath.Join(configDir, "bin", "bicep"), nil
}

// downloadBicep downloads a given version of bicep from the release site, writing the output to name.
func downloadBicep(ctx context.Context, transporter policy.Transporter, bicepVersion semver.Version, name string) error {
	var arch string
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	default:
		return fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}

	var releaseName string
	switch runtime.GOOS {
	case "windows":
		releaseName = fmt.Sprintf("bicep-win-%s.exe", arch)
	case "darwin":
		releaseName = fmt.Sprintf("bicep-osx-%s", arch)
	case "linux":
		if preferMuslBicep(os.Stat) {
			if runtime.GOARCH != "arm64" {
				return fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
			}

			releaseName = "bicep-linux-musl-x64"
		} else {
			releaseName = fmt.Sprintf("bicep-linux-%s", arch)
		}
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	bicepReleaseUrl := fmt.Sprintf("https://downloads.bicep.azure.com/v%s/%s", bicepVersion, releaseName)

	log.Printf("downloading bicep release %s -> %s", bicepReleaseUrl, name)

	req, err := http.NewRequestWithContext(ctx, "GET", bicepReleaseUrl, nil)
	if err != nil {
		return err
	}

	resp, err := transporter.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http error %d", resp.StatusCode)
	}

	f, err := os.CreateTemp(filepath.Dir(name), fmt.Sprintf("%s.tmp*", filepath.Base(name)))
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}

	if err := f.Chmod(osutil.PermissionExecutableFile); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	if err := osutil.Rename(ctx, f.Name(), name); err != nil {
		return err
	}

	return nil
}

type stater func(name string) (os.FileInfo, error)

// preferMuslBicep determines if we should install the version of bicep that used musl instead of glibc. We prefer
// musl bicep on linux systems that have musl installed and do not have glibc installed. If both musl and glibc are
// installed, we prefer the glibc based version.  This behavior matches the `az` CLI (see: Azure/azure-cli#23040)
func preferMuslBicep(stat stater) bool {
	if _, err := stat("/lib/ld-musl-x86_64.so.1"); err == nil {
		if _, err := stat("/lib/x86_64-linux-gnu/libc.so.6"); err == nil {
			return false
		}

		return true
	}

	return false
}

func (cli *Cli) version(ctx context.Context) (semver.Version, error) {
	bicepRes, err := cli.runCommand(ctx, nil, "--version")
	if err != nil {
		return semver.Version{}, err
	}

	bicepSemver, err := tools.ExtractVersion(bicepRes.Stdout)
	if err != nil {
		return semver.Version{}, err
	}

	return bicepSemver, nil

}

type BuildResult struct {
	// The compiled ARM template
	Compiled string

	// Lint error message, if any
	LintErr string
}

func (cli *Cli) Build(ctx context.Context, file string) (BuildResult, error) {
	if err := cli.ensureInstalledOnce(ctx); err != nil {
		return BuildResult{}, fmt.Errorf("ensuring bicep is installed: %w", err)
	}

	args := []string{"build", file, "--stdout"}
	buildRes, err := cli.runCommand(ctx, nil, args...)

	if err != nil {
		return BuildResult{}, fmt.Errorf(
			"failed running bicep build: %w",
			err,
		)
	}

	return BuildResult{
		Compiled: buildRes.Stdout,
		LintErr:  buildRes.Stderr,
	}, nil
}

func (cli *Cli) BuildBicepParam(ctx context.Context, file string, env []string) (BuildResult, error) {
	if err := cli.ensureInstalledOnce(ctx); err != nil {
		return BuildResult{}, fmt.Errorf("ensuring bicep is installed: %w", err)
	}

	args := []string{"build-params", file, "--stdout"}
	buildRes, err := cli.runCommand(ctx, env, args...)

	if err != nil {
		return BuildResult{}, fmt.Errorf(
			"failed running bicep build: %w",
			err,
		)
	}

	return BuildResult{
		Compiled: buildRes.Stdout,
		LintErr:  buildRes.Stderr,
	}, nil
}

func (cli *Cli) runCommand(ctx context.Context, env []string, args ...string) (exec.RunResult, error) {
	runArgs := exec.NewRunArgs(cli.path, args...)
	if env != nil {
		runArgs = runArgs.WithEnv(env)
	}
	return cli.runner.Run(ctx, runArgs)
}
