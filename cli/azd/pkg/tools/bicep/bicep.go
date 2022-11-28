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
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/events"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

// cBicepVersion is the minimum version of bicep that we require (and the one we fetch when we fetch bicep on behalf of a
// user).
var cBicepVersion semver.Version = semver.MustParse("0.12.40")

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
	var releaseName string
	switch runtime.GOOS {
	case "windows":
		releaseName = "bicep-win-x64.exe"
	case "darwin":
		releaseName = "bicep-osx-x64"
	case "linux":
		if _, err := os.Stat("/lib/ld-musl-x86_64.so.1"); err == nil {
			releaseName = "bicep-linux-musl-x64"
		} else {
			releaseName = "bicep-linux-x64"
		}
	default:
		return fmt.Errorf("unsupported platform")
	}

	bicepReleaseUrl := fmt.Sprintf("https://downloads.bicep.azure.com/v%s/%s", bicepVersion, releaseName)

	log.Printf("downloading bicep release %s -> %s", bicepReleaseUrl, name)

	spanCtx, span := telemetry.GetTracer().Start(ctx, events.BicepInstallEvent)
	defer span.End()

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

	if err := os.Rename(f.Name(), name); err != nil {
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
			"failed running bicep build: %s (%w)",
			buildRes.String(),
			err,
		)
	}

	return buildRes.Stdout, nil
}

func (cli *bicepCli) runCommand(ctx context.Context, args ...string) (exec.RunResult, error) {
	runArgs := exec.NewRunArgs(cli.path, args...)
	return cli.runner.Run(ctx, runArgs)
}
