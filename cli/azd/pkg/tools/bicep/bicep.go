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
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

// cBicepVersion is the minimum version of bicep that we require (and the one we fetch when we fetch bicep on behalf of a
// user).
var cBicepVersion semver.Version = semver.MustParse("0.17.1")

type BicepCli interface {
	Build(ctx context.Context, file string) (string, error)
}

// NewBicepCli creates a new BicepCli. Azd manages its own copy of the bicep CLI, stored in `$AZD_CONFIG_DIR/bin`. If
// bicep is not present at this location, or if it is present but is older than the minimum supported version, it is
// downloaded.
func NewBicepCli(
	ctx context.Context,
	console input.Console,
	commandRunner exec.CommandRunner,
) (BicepCli, error) {
	return newBicepCliWithTransporter(ctx, console, commandRunner, http.DefaultClient)
}

// newBicepCliWithTransporter is like NewBicepCli but allows providing a custom transport to use when downloading the
// bicep CLI, for testing purposes.
func newBicepCliWithTransporter(
	ctx context.Context,
	console input.Console,
	commandRunner exec.CommandRunner,
	transporter policy.Transporter,
) (BicepCli, error) {
	if override := os.Getenv("AZD_BICEP_TOOL_PATH"); override != "" {
		log.Printf("using external bicep tool: %s", override)

		return &bicepCli{
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
				return downloadBicep(ctx, transporter, cBicepVersion, bicepPath)
			},
		); err != nil {
			return nil, fmt.Errorf("downloading bicep: %w", err)
		}
	}

	cli := &bicepCli{
		path:   bicepPath,
		runner: commandRunner,
	}

	ver, err := cli.version(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking bicep version: %w", err)
	}

	log.Printf("bicep version: %s", ver)

	if ver.LT(cBicepVersion) {
		log.Printf("installed bicep version %s is older than %s; updating.", ver.String(), cBicepVersion.String())

		if err := runStep(
			ctx, console, "Upgrading Bicep", func() error {
				return downloadBicep(ctx, transporter, cBicepVersion, bicepPath)
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

type bicepCli struct {
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
		if _, err := os.Stat("/lib/ld-musl-x86_64.so.1"); err == nil {
			// As of 0.14.46, there is no version of for AM64 on musl based systems.
			if arch == "arm64" {
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

	var err error
	spanCtx, span := tracing.Start(ctx, events.BicepInstallEvent)
	defer span.EndWithStatus(err)

	req, err := http.NewRequestWithContext(spanCtx, "GET", bicepReleaseUrl, nil)
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

func (cli *bicepCli) version(ctx context.Context) (semver.Version, error) {
	bicepRes, err := cli.runCommand(ctx, "--version")
	if err != nil {
		return semver.Version{}, err
	}

	bicepSemver, err := tools.ExtractVersion(bicepRes.Stdout)
	if err != nil {
		return semver.Version{}, err
	}

	return bicepSemver, nil

}

func (cli *bicepCli) Build(ctx context.Context, file string) (string, error) {
	args := []string{"build", file, "--stdout"}
	buildRes, err := cli.runCommand(ctx, args...)

	if err != nil {
		return "", fmt.Errorf(
			"failed running bicep build: %w",
			err,
		)
	}

	return buildRes.Stdout, nil
}

func (cli *bicepCli) runCommand(ctx context.Context, args ...string) (exec.RunResult, error) {
	runArgs := exec.NewRunArgs(cli.path, args...)
	return cli.runner.Run(ctx, runArgs)
}
