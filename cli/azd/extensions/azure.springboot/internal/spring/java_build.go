// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type javaBuildSystem string

const (
	mavenBuild  javaBuildSystem = "maven"
	gradleBuild javaBuildSystem = "gradle"
)

func detectJavaBuild(root string) (javaBuildSystem, error) {
	for _, candidate := range []struct {
		name   string
		system javaBuildSystem
	}{
		{"pom.xml", mavenBuild},
		{"build.gradle", gradleBuild},
		{"build.gradle.kts", gradleBuild},
	} {
		if _, err := os.Lstat(filepath.Join(root, candidate.name)); err == nil {
			return candidate.system, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspecting Java build inputs: %w", err)
		}
	}
	// Preserve Maven's existing missing-project diagnostics.
	return mavenBuild, nil
}

func gradleWrapperName() string {
	if runtime.GOOS == "windows" {
		return "gradlew.bat"
	}
	return "gradlew"
}

func validateJavaBuild(root, serviceName string) error {
	system, err := detectJavaBuild(root)
	if err != nil {
		return err
	}
	if system == gradleBuild {
		return validateGradleProject(root)
	}
	for _, name := range []string{
		"pom.xml", "mvnw", filepath.Join(".mvn", "wrapper", "maven-wrapper.properties"), "src",
	} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			return validationError("unsupported_spring_project",
				fmt.Sprintf("service %q is missing required Maven project path %q", serviceName, name),
				"use a Maven Wrapper project, or a single-project Gradle Wrapper Spring Boot application")
		}
	}
	return nil
}

func validateGradleProject(root string) error {
	foundBuild := false
	for _, name := range []string{
		gradleWrapperName(), filepath.Join("gradle", "wrapper", "gradle-wrapper.properties"),
		filepath.Join("gradle", "wrapper", "gradle-wrapper.jar"), "src",
	} {
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil {
			return validationError("unsupported_gradle_project", "the Gradle project is missing required path "+name,
				"check in the Gradle Wrapper and src directory at the service project root")
		}
		if (name == "src" && !info.IsDir()) || (name != "src" && !info.Mode().IsRegular()) {
			return validationError("unsupported_gradle_project", "the Gradle project has an invalid required path "+name,
				"use regular wrapper files and a source directory, not links or special files")
		}
		if _, err := containedGradleFile(root, filepath.Join(root, name)); err != nil {
			return err
		}
		if name == gradleWrapperName() && runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			return validationError("gradle_wrapper_not_executable", "the Gradle Wrapper is not executable",
				"run chmod +x ./gradlew and retain its executable bit in version control")
		}
	}
	for _, name := range []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"} {
		filename := filepath.Join(root, name)
		info, err := os.Lstat(filename)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return validationError("unsupported_gradle_project", "a Gradle build or settings script is not a regular file",
				"use contained regular Gradle build and settings scripts")
		}
		if _, err := containedGradleFile(root, filename); err != nil {
			return err
		}
		foundBuild = foundBuild || name == "build.gradle" || name == "build.gradle.kts"
	}
	if !foundBuild {
		return validationError("unsupported_gradle_project", "the Gradle service has no build.gradle or build.gradle.kts",
			"set project to the single Gradle application root containing its build script and wrapper")
	}
	return nil
}

func containedGradleFile(root, filename string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	filename, err = filepath.Abs(filename)
	if err != nil {
		return "", err
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	// Compare ancestor identities as Windows short and long paths can name the same directory.
	for current := filename; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return "", fmt.Errorf("reading Gradle input or output: %w", err)
		}
		if os.SameFile(info, rootInfo) {
			return filename, nil
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return "", validationError("invalid_gradle_path", "a Gradle input or output contains a link or special file",
				"use contained regular files and directories for Gradle inputs and bootJar output")
		}
		if filepath.Dir(current) == current {
			return "", validationError("invalid_gradle_path", "a Gradle input or output is outside the service project",
				"keep wrapper files, build scripts, and bootJar output within the service project")
		}
	}
}
