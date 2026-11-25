// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package build

import (
	"fmt"
	"os"
	"path/filepath"

	"azurespringboot/internal/springmodel"
)

func validateJavaBuildInProject(projectRoot, serviceRoot, serviceName string) error {
	for _, name := range []string{"pom.xml", "src"} {
		if _, err := os.Stat(filepath.Join(serviceRoot, name)); err != nil {
			return validationError("unsupported_spring_project",
				fmt.Sprintf("service %q is missing required Maven project path %q", serviceName, name),
				"use a Maven Wrapper Spring Boot project")
		}
	}
	if err := findContainedMavenWrapper(projectRoot, serviceRoot, serviceName); err != nil {
		return err
	}
	return nil
}

func findContainedMavenWrapper(projectRoot, serviceRoot, serviceName string) error {
	resolvedProject, err := filepath.Abs(projectRoot)
	if err != nil {
		return err
	}
	resolvedProject, err = filepath.EvalSymlinks(resolvedProject)
	if err != nil {
		return err
	}
	resolvedService, err := filepath.Abs(serviceRoot)
	if err != nil {
		return err
	}
	resolvedService, err = filepath.EvalSymlinks(resolvedService)
	if err != nil {
		return err
	}
	projectInfo, err := os.Stat(resolvedProject)
	if err != nil {
		return err
	}

	wrapperProps := filepath.Join(".mvn", "wrapper", "maven-wrapper.properties")
	missingPath := "mvnw"
	for current := resolvedService; ; current = filepath.Dir(current) {
		info, statErr := os.Lstat(current)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			break
		}
		hasMvnw := springmodel.RegularContainedFile(resolvedProject, filepath.Join(current, "mvnw"))
		hasProps := springmodel.RegularContainedFile(resolvedProject, filepath.Join(current, wrapperProps))
		if hasMvnw && hasProps {
			return nil
		}
		if hasMvnw && !hasProps {
			missingPath = wrapperProps
		}
		if os.SameFile(info, projectInfo) || filepath.Dir(current) == current {
			break
		}
	}
	return validationError("unsupported_spring_project",
		fmt.Sprintf("service %q is missing required Maven project path %q", serviceName, missingPath),
		"use a Maven Wrapper Spring Boot project")
}
