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
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/blang/semver/v4"
)

type DotNetCli interface {
	tools.ExternalTool
	Restore(ctx context.Context, project string) error
	Build(ctx context.Context, project string, configuration string, output string) error
	Publish(ctx context.Context, project string, configuration string, output string) error
	PublishContainer(
		ctx context.Context, project, configuration, imageName, server, username, password string,
	) (int, error)
	InitializeSecret(ctx context.Context, project string) error
	// PublishAppHostManifest runs the app host program with the correct configuration to generate an manifest. If dotnetEnv
	// is non-empty, it will be passed as environment variables (named `DOTNET_ENVIRONMENT`) when running the app host
	// program.
	PublishAppHostManifest(ctx context.Context, hostProject string, manifestPath string, dotnetEnv string) error
	SetSecrets(ctx context.Context, secrets map[string]string, project string) error
	GetMsBuildProperty(ctx context.Context, project string, propertyName string) (string, error)
}

type dotNetCli struct {
	commandRunner exec.CommandRunner
}

type responseContainerConfiguration struct {
	Config responseContainerConfigurationExpPorts `json:"config"`
}

type responseContainerConfigurationExpPorts struct {
	ExposedPorts map[string]interface{} `json:"ExposedPorts"`
}

type targetPort struct {
	port     string
	protocol string
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
	dotnetRes, err := cli.commandRunner.Run(ctx, newDotNetRunArgs("--version"))
	if err != nil {
		return fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}
	log.Printf("dotnet version: %s", dotnetRes.Stdout)
	dotnetSemver, err := tools.ExtractVersion(dotnetRes.Stdout)
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
	runArgs := newDotNetRunArgs("restore", project)
	_, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("dotnet restore on project '%s' failed: %w", project, err)
	}
	return nil
}

func (cli *dotNetCli) Build(ctx context.Context, project string, configuration string, output string) error {
	runArgs := newDotNetRunArgs("build", project)
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
	runArgs := newDotNetRunArgs("publish", project)
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
	ctx context.Context, hostProject string, manifestPath string, dotnetEnv string,
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

	// AppHosts may conditionalize their infrastructure based on the environment, so we need to pass the environment when we
	// are `dotnet run`ing the app host project to produce its manifest.
	var envArgs []string

	if dotnetEnv != "" {
		envArgs = append(envArgs, fmt.Sprintf("DOTNET_ENVIRONMENT=%s", dotnetEnv))
	}

	if envArgs != nil {
		runArgs = runArgs.WithEnv(envArgs)
	}

	_, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("dotnet run --publisher manifest on project '%s' failed: %w", hostProject, err)
	}

	return nil
}

// PublishContainer runs a `dotnet publish“ with `/t:PublishContainer`to build and publish the container.
// It also gets port number by using `--getProperty:GeneratedContainerConfiguration`
func (cli *dotNetCli) PublishContainer(
	ctx context.Context, project, configuration, imageName, server, username, password string,
) (int, error) {
	runArgs := newDotNetRunArgs("publish", project)

	runArgs = runArgs.AppendParams(
		"-r", "linux-x64",
		"-c", configuration,
		"/t:PublishContainer",
		fmt.Sprintf("-p:ContainerImageName=%s", imageName),
		fmt.Sprintf("-p:ContainerRegistry=%s", server),
		"--getProperty:GeneratedContainerConfiguration",
	)

	runArgs = runArgs.WithEnv([]string{
		fmt.Sprintf("SDK_CONTAINER_REGISTRY_UNAME=%s", username),
		fmt.Sprintf("SDK_CONTAINER_REGISTRY_PWORD=%s", password),
	})

	result, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return 0, fmt.Errorf("dotnet publish on project '%s' failed: %w", project, err)
	}

	port, err := cli.getTargetPort(result.Stdout, project)
	if err != nil {
		return 0, fmt.Errorf("failed to get dotnet target port: %w with dotnet publish output '%s'", err, result.Stdout)
	}

	return port, nil
}

func (cli *dotNetCli) getTargetPort(result, project string) (int, error) {
	var targetPorts []targetPort
	var configOutput responseContainerConfiguration

	// make sure result only contains json config output
	result = strings.Split(result, "\n\r\n\r")[0]

	// if empty string or there's no config output
	if result == "" || !strings.Contains(result, "{\"config\":") {
		return 0, &azcli.ErrorWithSuggestion{
			Err: fmt.Errorf("empty dotnet configuration output"),
			Suggestion: fmt.Sprintf("Ensure project '%s' is enabled for container support and try again. To enable SDK "+
				"container support, set the 'EnableSdkContainerSupport' property to true in your project file",
				project,
			),
		}
	}
	if err := json.Unmarshal([]byte(result), &configOutput); err != nil {
		return 0, fmt.Errorf("unmarshal dotnet configuration output: %w", err)
	}
	var exposedPortOutput []string
	for key := range configOutput.Config.ExposedPorts {
		exposedPortOutput = append(exposedPortOutput, key)
	}

	// exposedPortOutput format is <PORT_NUM>[/PORT_TYPE>]
	for _, value := range exposedPortOutput {
		split := strings.Split(value, "/")
		if len(split) > 1 {
			targetPorts = append(targetPorts, targetPort{port: split[0], protocol: split[1]})
		} else {
			// Provide a default tcp protocol if none is specified
			targetPorts = append(targetPorts, targetPort{port: split[0], protocol: "tcp"})
		}
	}

	if len(exposedPortOutput) < 1 {
		return 0, fmt.Errorf("multiple dotnet port %s detected", targetPorts)
	}

	port, err := strconv.Atoi(targetPorts[0].port)
	if err != nil {
		return 0, fmt.Errorf("convert port %s to integer: %w", targetPorts[0].port, err)
	}
	return port, nil
}

func (cli *dotNetCli) InitializeSecret(ctx context.Context, project string) error {
	runArgs := newDotNetRunArgs("user-secrets", "init", "--project", project)
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
	runArgs := newDotNetRunArgs("user-secrets", "set", "--project", project).
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
	runArgs := newDotNetRunArgs("msbuild", project, fmt.Sprintf("--getProperty:%s", propertyName))
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

// newDotNetRunArgs creates a new RunArgs to run the specified dotnet command. It sets the environment variable
// to disable output of workload update notifications, to make it easier for us to parse the output.
func newDotNetRunArgs(args ...string) exec.RunArgs {
	runArgs := exec.NewRunArgs("dotnet", args...)

	runArgs = runArgs.WithEnv([]string{
		"DOTNET_CLI_WORKLOAD_UPDATE_NOTIFY_DISABLE=1",
	})

	return runArgs
}
