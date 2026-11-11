// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/javac"
)

//go:embed gradle.init.gradle
var gradleInitScript string

type gradleReport struct {
	Version      int      `json:"version"`
	Root         string   `json:"root"`
	Dependencies []string `json:"dependencies"`
	Archive      string   `json:"archive"`
}

type gradleProject struct {
	runner azdexec.CommandRunner
	env    *environment.Environment
}

func (g *gradleProject) Initialize(_ context.Context, service *project.ServiceConfig) error {
	return validateGradleProject(service.Path())
}

func (g *gradleProject) RequiredExternalTools(
	_ context.Context, _ *project.ServiceConfig,
) []tools.ExternalTool {
	return []tools.ExternalTool{javac.NewCli(g.runner)}
}

func (g *gradleProject) Requirements() project.FrameworkRequirements {
	return project.FrameworkRequirements{}
}

func (g *gradleProject) Restore(
	ctx context.Context, service *project.ServiceConfig, _ *project.ServiceContext,
	progress *async.Progress[project.ServiceProgress],
) (*project.ServiceRestoreResult, error) {
	progress.SetProgress(project.NewServiceProgress("Resolving Gradle runtime dependencies"))
	if _, err := runGradle(ctx, g.runner, service.Path(), g.env.Environ(), "restore"); err != nil {
		return nil, err
	}
	return &project.ServiceRestoreResult{Artifacts: gradleArtifacts(service.Path())}, nil
}

func (g *gradleProject) Build(
	ctx context.Context, service *project.ServiceConfig, _ *project.ServiceContext,
	progress *async.Progress[project.ServiceProgress],
) (*project.ServiceBuildResult, error) {
	progress.SetProgress(project.NewServiceProgress("Compiling Gradle project"))
	if _, err := runGradle(ctx, g.runner, service.Path(), g.env.Environ(), "build"); err != nil {
		return nil, err
	}
	return &project.ServiceBuildResult{Artifacts: gradleArtifacts(service.Path())}, nil
}

func (g *gradleProject) Package(
	ctx context.Context, service *project.ServiceConfig, _ *project.ServiceContext,
	progress *async.Progress[project.ServiceProgress],
) (*project.ServicePackageResult, error) {
	progress.SetProgress(project.NewServiceProgress("Packaging Gradle bootJar"))
	if service.OutputPath != "" {
		report, err := runGradle(ctx, g.runner, service.Path(), g.env.Environ(), "restore")
		if err != nil {
			return nil, err
		}
		if err := validateGradleOutput(service.OutputPath, report); err != nil {
			return nil, err
		}
	}
	report, err := runGradle(ctx, g.runner, service.Path(), g.env.Environ(), "package")
	if err != nil {
		return nil, err
	}
	jar, err := containedGradleFile(service.Path(), report.Archive)
	if err != nil {
		return nil, err
	}
	if err := validateGradleOutput(service.OutputPath, report); err != nil {
		return nil, err
	}
	if err := validateGradleArchive(ctx, jar); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "azd-springboot-gradle-package-")
	if err != nil {
		return nil, fmt.Errorf("creating Gradle package directory: %w", err)
	}
	if _, _, err := copyFileWithContext(ctx, jar, filepath.Join(directory, stagedJarName)); err != nil {
		return nil, errors.Join(err, removeGradleDirectory(directory))
	}
	return &project.ServicePackageResult{Artifacts: gradleArtifacts(directory)}, nil
}

func validateGradleOutput(output string, report gradleReport) error {
	if output == "" {
		return nil
	}
	if !filepath.IsAbs(output) {
		output = filepath.Join(report.Root, output)
	}
	for _, candidate := range []string{report.Archive, filepath.Dir(report.Archive)} {
		if relative, err := filepath.Rel(output, candidate); err == nil && relative == "." {
			return nil
		}
	}
	return validationError("conflicting_gradle_output", "dist/output does not identify the bootJar output",
		"remove dist/output or set it to the bootJar archive file or its directory")
}

func gradleArtifacts(directory string) project.ArtifactCollection {
	return project.ArtifactCollection{{
		Kind: project.ArtifactKindDirectory, LocationKind: project.LocationKindLocal, Location: directory,
		Metadata: map[string]string{"framework": string(gradleBuild)},
	}}
}

func runGradle(
	ctx context.Context, runner azdexec.CommandRunner, root string, env []string, operation string,
) (report gradleReport, err error) {
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := validateGradleProject(root); err != nil {
		return report, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return report, err
	}
	directory, err := os.MkdirTemp("", "azd-springboot-gradle-report-")
	if err != nil {
		return report, fmt.Errorf("creating Gradle metadata context: %w", err)
	}
	defer func() { err = errors.Join(err, removeGradleDirectory(directory)) }()
	script := filepath.Join(directory, "init.gradle")
	reportFile := filepath.Join(directory, "report.json")
	if err := os.WriteFile(script, []byte(gradleInitScript), osutil.PermissionFileOwnerOnly); err != nil {
		return report, fmt.Errorf("writing Gradle metadata script: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	args := azdexec.NewRunArgs(filepath.Join(root, gradleWrapperName()),
		"--no-daemon", "--no-configuration-cache", "--console=plain", "--init-script", script,
		"-Dazd.spring.operation="+operation, "-Dazd.spring.report="+reportFile, "azdSpringReport", "-x", "test",
	).WithCwd(root).WithEnv(env).WithDebugLogging(false).WithStdOut(io.Discard).WithStdErr(io.Discard)
	result, runErr := runner.Run(ctx, args)
	if ctx.Err() != nil {
		return report, ctx.Err()
	}
	if runErr != nil {
		return report, nonDisclosureError(fmt.Errorf("running Gradle %s: %w", operation, runErr))
	}
	if result.ExitCode != 0 {
		return report, validationError("gradle_build_failed",
			fmt.Sprintf("Gradle %s failed with exit code %d", operation, result.ExitCode),
			"run the project Gradle Wrapper locally and verify a single-project Spring Boot build with an enabled bootJar")
	}
	filename, err := containedGradleFile(directory, reportFile)
	if err != nil {
		return report, validationError("invalid_gradle_report", "Gradle did not produce a regular metadata report",
			"verify the Gradle Wrapper supports the Spring Boot bootJar and runtimeClasspath tasks")
	}
	file, err := os.Open(filename)
	if err != nil {
		return report, fmt.Errorf("opening Gradle metadata: %w", err)
	}
	data, readErr := readSpringConfigurationData(file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return report, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return report, invalidGradleReport()
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return report, invalidGradleReport()
	}
	expectedRoot, expectedErr := os.Stat(root)
	actualRoot, rootErr := os.Stat(report.Root)
	if report.Version != 1 || rootErr != nil || expectedErr != nil || !os.SameFile(actualRoot, expectedRoot) ||
		!filepath.IsAbs(report.Root) || !filepath.IsAbs(report.Archive) ||
		!osutil.IsPathContained(report.Root, report.Archive) ||
		report.Dependencies == nil || len(report.Dependencies) > 10000 {
		return report, invalidGradleReport()
	}
	for _, coordinate := range report.Dependencies {
		if len(coordinate) > 1024 || strings.Count(coordinate, ":") != 1 {
			return report, invalidGradleReport()
		}
	}
	return report, nil
}

func invalidGradleReport() error {
	return validationError("invalid_gradle_report", "Gradle returned invalid or mismatched application metadata",
		"verify a single-project Spring Boot build with the Gradle Wrapper")
}

func removeGradleDirectory(directory string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return osutil.RemoveAll(ctx, directory)
}

func validateGradleArchive(ctx context.Context, jar string) (err error) {
	if !strings.EqualFold(filepath.Ext(jar), ".jar") {
		return validationError("invalid_gradle_jar", "bootJar did not produce a JAR",
			"configure bootJar to produce an executable Spring Boot JAR")
	}
	archive, err := openSpringArchive(jar)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, archive.reader.Close()) }()
	for _, entry := range archive.reader.File {
		if entry.Name != "META-INF/MANIFEST.MF" {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		data, readErr := readSpringConfigurationData(reader)
		if err := errors.Join(readErr, reader.Close()); err != nil {
			return err
		}
		manifest := "\n" + strings.ReplaceAll(string(data), "\r\n", "\n")
		if strings.Contains(manifest, "\nMain-Class: org.springframework.boot.loader.") &&
			strings.Contains(manifest, "\nStart-Class: ") {
			return validateSpringJarRuntime(ctx, jar)
		}
	}
	return validationError("invalid_gradle_jar", "bootJar output is not an executable Spring Boot JAR",
		"enable the Spring Boot bootJar task rather than selecting a plain JAR")
}
