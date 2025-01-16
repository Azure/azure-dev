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
var Version semver.Version = semver.MustParse("0.32.4")

// NewCli creates a new Bicep CLI. Azd manages its own copy of the bicep CLI, stored in `$AZD_CONFIG_DIR/bin`. If
// bicep is not present at this location, or if it is present but is older than the minimum supported version, it is
// downloaded.
func NewCli(
	ctx context.Context,
	console input.Console,
	commandRunner exec.CommandRunner,
) (*Cli, error) {
	return newCliWithTransporter(ctx, console, commandRunner, http.DefaultClient)
}

// newCliWithTransporter is like NewBicepCli but allows providing a custom transport to use when downloading the
// Bicep CLI, for testing purposes.
func newCliWithTransporter(
	ctx context.Context,
	console input.Console,
	commandRunner exec.CommandRunner,
	transporter policy.Transporter,
) (*Cli, error) {
	if override := os.Getenv("AZD_BICEP_TOOL_PATH"); override != "" {
		log.Printf("using external bicep tool: %s", override)

		return &Cli{
			path:   override,
			runner: commandRunner,
		}, nil
	}

	bicepPath, err := azdBicepPath()
	if err != nil {
		return nil, fmt.Errorf("finding bicep: %w", err)
	}

	if _, err = os.Stat(bicepPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("finding bicep: %w", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(bicepPath), osutil.PermissionDirectory); err != nil {
			return nil, fmt.Errorf("downloading bicep: %w", err)
		}

		if err := runStep(
			ctx, console, "Downloading Bicep", func() error {
				return downloadBicep(ctx, transporter, Version, bicepPath)
			},
		); err != nil {
			return nil, fmt.Errorf("downloading bicep: %w", err)
		}
	}

	cli := &Cli{
		path:   bicepPath,
		runner: commandRunner,
	}

	ver, err := cli.version(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking bicep version: %w", err)
	}

	log.Printf("bicep version: %s", ver)

	if ver.LT(Version) {
		log.Printf("installed bicep version %s is older than %s; updating.", ver.String(), Version.String())

		if err := runStep(
			ctx, console, "Upgrading Bicep", func() error {
				return downloadBicep(ctx, transporter, Version, bicepPath)
			},
		); err != nil {
			return nil, fmt.Errorf("upgrading bicep: %w", err)
		}
	}

	log.Printf("using local bicep: %s", bicepPath)

	return cli, nil
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

type Cli struct {
	path   string
	runner exec.CommandRunner
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
