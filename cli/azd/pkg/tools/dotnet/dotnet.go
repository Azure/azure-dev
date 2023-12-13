// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dotnet

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

type DotNetCli interface {
	tools.ExternalTool
	Restore(ctx context.Context, project string) error
	Build(ctx context.Context, project string, configuration string, output string) error
	Publish(ctx context.Context, project string, configuration string, output string) error
	PublishContainer(ctx context.Context, project string, configuration string, imageName string, server string) error
	InitializeSecret(ctx context.Context, project string) error
	PublishAppHostManifest(ctx context.Context, hostProject string, manifestPath string) error
	SetSecrets(ctx context.Context, secrets map[string]string, project string) error
	GetMsBuildProperty(ctx context.Context, project string, propertyName string) (string, error)
}

type dotNetCli struct {
	commandRunner exec.CommandRunner
}

func (cli *dotNetCli) Name() string {
	return ".NET CLI"
}

func (cli *dotNetCli) InstallUrl() string {
	return "https://dotnet.microsoft.com/download"
}

func (cli *dotNetCli) versionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 6,
			Minor: 0,
			Patch: 3},
		UpdateCommand: "Visit https://docs.microsoft.com/en-us/dotnet/core/releases-and-support to upgrade",
	}
}

func (cli *dotNetCli) CheckInstalled(ctx context.Context) error {
	err := tools.ToolInPath("dotnet")
	if err != nil {
		return err
	}
	dotnetRes, err := tools.ExecuteCommand(ctx, cli.commandRunner, "dotnet", "--version")
	if err != nil {
		return fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}
	log.Printf("dotnet version: %s", dotnetRes)
	dotnetSemver, err := tools.ExtractVersion(dotnetRes)
	if err != nil {
		return fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := cli.versionInfo()
	if dotnetSemver.LT(updateDetail.MinimumVersion) {
		return &tools.ErrSemver{ToolName: cli.Name(), VersionInfo: updateDetail}
	}
	return nil
}

func (cli *dotNetCli) Restore(ctx context.Context, project string) error {
	runArgs := exec.NewRunArgs("dotnet", "restore", project)
	_, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("dotnet restore on project '%s' failed: %w", project, err)
	}
	return nil
}

func (cli *dotNetCli) Build(ctx context.Context, project string, configuration string, output string) error {
	runArgs := exec.NewRunArgs("dotnet", "build", project)
	if configuration != "" {
		runArgs = runArgs.AppendParams("-c", configuration)
	}

	if output != "" {
		runArgs = runArgs.AppendParams("--output", output)
	}

	_, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("dotnet build on project '%s' failed: %w", project, err)
	}
	return nil
}

func (cli *dotNetCli) Publish(ctx context.Context, project string, configuration string, output string) error {
	runArgs := exec.NewRunArgs("dotnet", "publish", project)
	if configuration != "" {
		runArgs = runArgs.AppendParams("-c", configuration)
	}

	if output != "" {
		runArgs = runArgs.AppendParams("--output", output)
	}

	_, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("dotnet publish on project '%s' failed: %w", project, err)
	}
	return nil
}

func (cli *dotNetCli) PublishAppHostManifest(
	ctx context.Context, hostProject string, manifestPath string,
) error {

	// TODO(ellismg): Before we GA manifest support, we should remove this debug tool, but being able to control what
	// manifest is used is helpful, while the manifest/generator is still being built.  So if
	// `AZD_DEBUG_DOTNET_APPHOST_USE_FIXED_MANIFEST` is set, then we will expect to find apphost-manifest.json SxS with the host
	// project, and we just use that instead.
	if enabled, err := strconv.ParseBool(os.Getenv("AZD_DEBUG_DOTNET_APPHOST_USE_FIXED_MANIFEST")); err == nil && enabled {
		m, err := os.ReadFile(filepath.Join(filepath.Dir(hostProject), "apphost-manifest.json"))
		if err != nil {
			return fmt.Errorf(
				"reading apphost-manifest.json (did you mean to have AZD_DEBUG_DOTNET_APPHOST_USE_FIXED_MANIFEST set?): %w",
				err,
			)
		}

		return os.WriteFile(manifestPath, m, osutil.PermissionFile)
	}

	runArgs := exec.NewRunArgs(
		"dotnet", "run", "--project", filepath.Base(hostProject), "--publisher", "manifest", "--output-path", manifestPath)

	runArgs = runArgs.WithCwd(filepath.Dir(hostProject))

	_, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("dotnet run --publisher manifest on project '%s' failed: %w", hostProject, err)
	}

	return nil
}

// PublishContainer runs a `dotnet publish“ with `PublishProfile=DefaultContainer` to build and publish the container.
func (cli *dotNetCli) PublishContainer(
	ctx context.Context, project string, configuration string, imageName string, server string,
) error {
	runArgs := exec.NewRunArgs("dotnet", "publish", project)
	if configuration != "" {
		runArgs = runArgs.AppendParams("-c", configuration)
	}

	if imageName != "" {
		runArgs = runArgs.AppendParams(fmt.Sprintf("-p:ContainerImageName=%s", imageName))
	}

	runArgs = runArgs.AppendParams(
		"-r", "linux-x64",
		"-p:PublishProfile=DefaultContainer",
		fmt.Sprintf("-p:ContainerRegistry=%s", server),
	)

	_, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("dotnet publish on project '%s' failed: %w", project, err)
	}
	return nil
}

func (cli *dotNetCli) InitializeSecret(ctx context.Context, project string) error {
	runArgs := exec.NewRunArgs("dotnet", "user-secrets", "init", "--project", project)
	_, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed to initialize secrets at project '%s': %w", project, err)
	}
	return nil
}

func (cli *dotNetCli) SetSecrets(ctx context.Context, secrets map[string]string, project string) error {
	secretsJson, err := json.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("failed to marshal secrets: %w", err)
	}

	// dotnet user-secrets now support setting multiple values at once
	//https://learn.microsoft.com/en-us/aspnet/core/security/app-secrets?view=aspnetcore-7.0&tabs=windows#set-multiple-secrets
	runArgs := exec.
		NewRunArgs("dotnet", "user-secrets", "set", "--project", project).
		WithStdIn(strings.NewReader(string(secretsJson)))

	_, err = cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed running %s secret set: %w", cli.Name(), err)
	}
	return nil
}

// GetMsBuildProperty uses -getProperty to fetch a property after evaluation, without executing the build.
//
// This only works for versions dotnet >= 8, MSBuild >= 17.8.
// On older tool versions, this will return an error.
func (cli *dotNetCli) GetMsBuildProperty(ctx context.Context, project string, propertyName string) (string, error) {
	runArgs := exec.NewRunArgs("dotnet", "msbuild", project, fmt.Sprintf("--getProperty:%s", propertyName))
	res, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return "", err
	}
	return res.Stdout, nil
}

func NewDotNetCli(commandRunner exec.CommandRunner) DotNetCli {
	return &dotNetCli{
		commandRunner: commandRunner,
	}
}
