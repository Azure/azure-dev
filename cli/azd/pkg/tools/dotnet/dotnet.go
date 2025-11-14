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
	"slices"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/internal"
	"github.com/azure/azure-dev/pkg/exec"
	"github.com/azure/azure-dev/pkg/osutil"
	"github.com/azure/azure-dev/pkg/tools"
	"github.com/blang/semver/v4"
)

var _ tools.ExternalTool = (*Cli)(nil)

// Constants for Aspire AppHost detection
const (
	// SingleFileAspireHostName is the required filename for single-file Aspire AppHosts
	SingleFileAspireHostName = "apphost.cs"
	// AspireSdkDirective is the SDK directive that must be present in single-file Aspire AppHosts
	AspireSdkDirective = "#:sdk Aspire.AppHost.Sdk"
)

// DotNetProjectExtensions lists all valid .NET project file extensions
var DotNetProjectExtensions = []string{".csproj", ".fsproj", ".vbproj"}

type Cli struct {
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

func (cli *Cli) Name() string {
	return ".NET CLI"
}

func (cli *Cli) InstallUrl() string {
	return "https://dotnet.microsoft.com/download"
}

func (cli *Cli) versionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 6,
			Minor: 0,
			Patch: 3},
		UpdateCommand: "Visit https://docs.microsoft.com/en-us/dotnet/core/releases-and-support to upgrade",
	}
}

func (cli *Cli) CheckInstalled(ctx context.Context) error {
	err := cli.commandRunner.ToolInPath("dotnet")
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

func (cli *Cli) Restore(ctx context.Context, project string) error {
	runArgs := newDotNetRunArgs("restore", project)
	_, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("dotnet restore on project '%s' failed: %w", project, err)
	}
	return nil
}

func (cli *Cli) Build(ctx context.Context, project string, configuration string, output string) error {
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

func (cli *Cli) Publish(ctx context.Context, project string, configuration string, output string) error {
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

func (cli *Cli) PublishAppHostManifest(
	ctx context.Context, hostProject string, manifestPath string, dotnetEnv string,
) error {
	// TODO(ellismg): Before we GA manifest support, we should remove this debug tool, but being able to control what
	// manifest is used is helpful, while the manifest/generator is still being built.  So if
	// `AZD_DEBUG_DOTNET_APPHOST_USE_FIXED_MANIFEST` is set, then we will expect to find apphost-manifest.json SxS with the
	// host project, and we just use that instead.
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

	// For single-file apphost, we need to use the .cs file directly
	var runArgs exec.RunArgs
	if filepath.Ext(hostProject) == ".cs" {
		// Single-file apphost: use the .cs file directly as the argument
		runArgs = exec.NewRunArgs(
			"dotnet", "run", filepath.Base(hostProject), "--publisher", "manifest", "--output-path", manifestPath)
	} else {
		// Project-based apphost: use --project flag
		runArgs = exec.NewRunArgs(
			"dotnet",
			"run",
			"--project",
			filepath.Base(hostProject),
			"--publisher",
			"manifest",
			"--output-path",
			manifestPath)
	}

	runArgs = runArgs.WithCwd(filepath.Dir(hostProject))

	// AppHost may conditionalize their infrastructure based on the environment, so we need to pass the environment when we
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

// PublishContainer runs a `dotnet publish" with `/t:PublishContainer`to build and publish the container.
// It also gets port number by using `--getProperty:GeneratedContainerConfiguration`.
// For single-file apps (.cs files), it runs from the file's directory to properly resolve relative references.
// BuildContainerLocal runs `dotnet publish` with `/t:PublishContainer` to build a container image locally
// without pushing to a registry. Returns the port number and image name.
func (cli *Cli) BuildContainerLocal(
	ctx context.Context, project, configuration, imageName string,
) (int, string, error) {
	if !strings.Contains(imageName, ":") {
		imageName = fmt.Sprintf("%s:latest", imageName)
	}

	imageParts := strings.Split(imageName, ":")

	// For single-file apps, use the basename and set the working directory
	// For project-based apps, use the full path
	var runArgs exec.RunArgs
	if filepath.Ext(project) == ".cs" {
		// Single-file app: use just the filename and set working directory
		runArgs = newDotNetRunArgs("publish", filepath.Base(project))
		runArgs = runArgs.WithCwd(filepath.Dir(project))
	} else {
		// Project-based app: use the full path
		runArgs = newDotNetRunArgs("publish", project)
	}

	runArgs = runArgs.AppendParams(
		"-r", "linux-x64",
		"-c", configuration,
		"/t:PublishContainer",
		fmt.Sprintf("-p:ContainerRepository=%s", imageParts[0]),
		fmt.Sprintf("-p:ContainerImageTag=%s", imageParts[1]),
		"-p:PublishProfile=DefaultContainer", // Use default container profile for local build
		"--getProperty:GeneratedContainerConfiguration",
	)

	result, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return 0, "", fmt.Errorf("dotnet publish on project '%s' failed: %w", project, err)
	}

	port, err := cli.getTargetPort(result.Stdout, project)
	if err != nil {
		return 0, "", fmt.Errorf("failed to get dotnet target port: %w with dotnet publish output '%s'", err, result.Stdout)
	}

	return port, imageName, nil
}

func (cli *Cli) PublishContainer(
	ctx context.Context, project, configuration, imageName, server, username, password string,
) (int, error) {
	if !strings.Contains(imageName, ":") {
		imageName = fmt.Sprintf("%s:latest", imageName)
	}

	imageParts := strings.Split(imageName, ":")

	// For single-file apps, use the basename and set the working directory
	// For project-based apps, use the full path
	var runArgs exec.RunArgs
	if filepath.Ext(project) == ".cs" {
		// Single-file app: use just the filename and set working directory
		runArgs = newDotNetRunArgs("publish", filepath.Base(project))
		runArgs = runArgs.WithCwd(filepath.Dir(project))
	} else {
		// Project-based app: use the full path
		runArgs = newDotNetRunArgs("publish", project)
	}

	runArgs = runArgs.AppendParams(
		"-r", "linux-x64",
		"-c", configuration,
		"/t:PublishContainer",
		fmt.Sprintf("-p:ContainerRepository=%s", imageParts[0]),
		fmt.Sprintf("-p:ContainerImageTag=%s", imageParts[1]),
		fmt.Sprintf("-p:ContainerRegistry=%s", server),
		"--getProperty:GeneratedContainerConfiguration",
	)

	runArgs = runArgs.WithEnv([]string{
		fmt.Sprintf("DOTNET_CONTAINER_REGISTRY_UNAME=%s", username),
		fmt.Sprintf("DOTNET_CONTAINER_REGISTRY_PWORD=%s", password),
		// legacy variables for dotnet SDK version < 8.0.400
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

// getTargetPort parses the output of `dotnet publish` with `/t:PublishContainer` to get the port the container exposes.
func (cli *Cli) getTargetPort(result, project string) (int, error) {
	// Ensure the output is a JSON object and it has a property named "config". If not, the project needs to be configured
	// to produce a container.
	//
	// We use json.NewDecoder instead of json.Unmarshal because sometimes the `dotnet` tool will put "helpful" messages like
	// a workload being out of date at the end of stdout, which would confuse us if we tried to Unmarshal all of result.
	var obj map[string]json.RawMessage
	_ = json.NewDecoder(strings.NewReader(result)).Decode(&obj)

	// if empty string or there's no config output
	if result == "" || obj["config"] == nil {
		return 0, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf("empty dotnet configuration output"),
			Suggestion: fmt.Sprintf("Ensure project '%s' is enabled for container support and try again. To enable SDK "+
				"container support, set the 'EnableSdkContainerSupport' property to true in your project file",
				project,
			),
		}
	}

	var targetPorts []targetPort
	var configOutput responseContainerConfiguration

	if err := json.NewDecoder(strings.NewReader(result)).Decode(&configOutput); err != nil {
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

func (cli *Cli) InitializeSecret(ctx context.Context, project string) error {
	runArgs := newDotNetRunArgs("user-secrets", "init", "--project", project)
	_, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed to initialize secrets at project '%s': %w", project, err)
	}
	return nil
}

func (cli *Cli) SetSecrets(ctx context.Context, secrets map[string]string, project string) error {
	secretsJson, err := json.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("failed to marshal secrets: %w", err)
	}

	// dotnet user-secrets now support setting multiple values at once
	// learn.microsoft.com/aspnet/core/security/app-secrets?view=aspnetcore-7.0&tabs=windows#set-multiple-secrets
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
func (cli *Cli) GetMsBuildProperty(ctx context.Context, project string, propertyName string) (string, error) {
	runArgs := newDotNetRunArgs("msbuild",
		project, "--ignore:.sln", fmt.Sprintf("--getProperty:%s", propertyName))
	res, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return "", err
	}
	return res.Stdout, nil
}

// IsAspireHostProject returns true if the project at the given path has an MS Build Property named "IsAspireHost" which is
// set to true or has a ProjectCapability named "Aspire".
// For single-file apphost (apphost.cs), it validates them without running msbuild.
func (cli *Cli) IsAspireHostProject(ctx context.Context, projectPath string) (bool, error) {
	// Check if this is a single-file apphost first (to avoid running msbuild on .cs files)
	if filepath.Ext(projectPath) == ".cs" {
		return cli.IsSingleFileAspireHost(projectPath)
	}

	runArgs := newDotNetRunArgs("msbuild",
		projectPath, "--ignore:.sln", "--getProperty:IsAspireHost", "--getItem:ProjectCapability")
	res, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return false, fmt.Errorf("running dotnet msbuild on project '%s': %w", projectPath, err)
	}

	var result struct {
		Properties struct {
			IsAspireHost string `json:"IsAspireHost"`
		} `json:"Properties"`
		Items struct {
			ProjectCapability []struct {
				Identity string `json:"Identity"`
			} `json:"ProjectCapability"`
		} `json:"Items"`
	}

	if err := json.Unmarshal([]byte(res.Stdout), &result); err != nil {
		return false, fmt.Errorf("unmarshal dotnet msbuild output: %w", err)
	}

	hasAspireCapability := false

	for _, capability := range result.Items.ProjectCapability {
		if capability.Identity == "Aspire" {
			hasAspireCapability = true
			break
		}
	}

	return result.Properties.IsAspireHost == "true" || hasAspireCapability, nil
}

// IsSingleFileAspireHost checks if the given file is a single-file Aspire AppHost.
// The caller should verify the filename matches SingleFileAspireHostName before calling this function.
// A file-based Aspire AppHost must meet all of these conditions:
// 1. File name: Must be named apphost.cs (case-insensitive) - verified by caller
// 2. No sibling .NET project files: The directory must NOT contain .csproj, .fsproj, or .vbproj files
// 3. SDK Directive: Must contain the AspireSdkDirective in the file content
func (cli *Cli) IsSingleFileAspireHost(filePath string) (bool, error) {
	// Verify filename as a safety check (case-insensitive)
	fileName := filepath.Base(filePath)
	if !strings.EqualFold(fileName, SingleFileAspireHostName) {
		return false, nil
	}

	// Check if there are no sibling .NET project files
	dir := filepath.Dir(filePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("reading directory '%s': %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && slices.Contains(DotNetProjectExtensions, filepath.Ext(entry.Name())) {
			return false, nil
		}
	}

	// Check if the file contains the SDK directive
	content, err := os.ReadFile(filePath)
	if err != nil {
		return false, fmt.Errorf("reading file '%s': %w", filePath, err)
	}

	return strings.Contains(string(content), AspireSdkDirective), nil
}

func NewCli(commandRunner exec.CommandRunner) *Cli {
	return &Cli{
		commandRunner: commandRunner,
	}
}

type GitIgnoreIfExistsStrategy string

const (
	// GitIgnoreIfExistsStrategySkip skips creating the .gitignore file if it already exists. This is the default behavior.
	GitIgnoreIfExistsStrategySkip GitIgnoreIfExistsStrategy = "skip"
	// GitIgnoreIfExistsStrategyOverwrite overwrites the existing .gitignore file.
	GitIgnoreIfExistsStrategyOverwrite GitIgnoreIfExistsStrategy = "overwrite"
)

type GitIgnoreOptions struct {
	// IfExistsStrategy determines what to do if the .gitignore file already exists. Default is Skip.
	IfExistsStrategy GitIgnoreIfExistsStrategy
}

func (cli *Cli) GitIgnore(ctx context.Context, projectPath string, options *GitIgnoreOptions) error {
	if options == nil {
		options = &GitIgnoreOptions{
			IfExistsStrategy: GitIgnoreIfExistsStrategySkip,
		}
	}
	if !slices.Contains([]GitIgnoreIfExistsStrategy{
		GitIgnoreIfExistsStrategySkip,
		GitIgnoreIfExistsStrategyOverwrite,
	}, options.IfExistsStrategy) {
		return fmt.Errorf("invalid IfExistsStrategy option: %s", options.IfExistsStrategy)
	}

	if _, err := os.Stat(filepath.Join(projectPath, ".gitignore")); err == nil {
		if options.IfExistsStrategy == GitIgnoreIfExistsStrategySkip {
			log.Printf("Skipping creation of .gitignore file at %s because it already exists.", projectPath)
			return nil
		}
	}

	runArgs := newDotNetRunArgs("new", "gitignore", "--project", projectPath)
	if options.IfExistsStrategy == GitIgnoreIfExistsStrategyOverwrite {
		// force argument prevents the command from failing if the .gitignore file already exists.
		runArgs.Args = append(runArgs.Args, "--force")
	}

	_, err := cli.commandRunner.Run(ctx, runArgs)
	return err
}

// newDotNetRunArgs creates a new RunArgs to run the specified dotnet command. It sets the environment variable
// to disable output of workload update notifications, to make it easier for us to parse the output.
func newDotNetRunArgs(args ...string) exec.RunArgs {
	runArgs := exec.NewRunArgs("dotnet", args...)

	runArgs = runArgs.WithEnv([]string{
		"DOTNET_CLI_WORKLOAD_UPDATE_NOTIFY_DISABLE=1",
		"DOTNET_NOLOGO=1",
	})

	return runArgs
}
