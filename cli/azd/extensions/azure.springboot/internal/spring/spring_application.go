// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
)

type springDependencies struct {
	database      bool
	h2            bool
	postgresql    bool
	azureIdentity bool
	azureJDBC     bool
}

type springArchive struct {
	reader       *zip.ReadCloser
	resources    map[string]*zip.File
	dependencies springDependencies
}

func openSpringArchive(filename string) (*springArchive, error) {
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() {
		return nil, configurationError("the packaged Spring application must be an existing regular JAR")
	}
	reader, err := zip.OpenReader(filename)
	if err != nil {
		return nil, configurationError("the packaged Spring application is not a readable JAR")
	}
	archive := &springArchive{reader: reader, resources: map[string]*zip.File{}}
	fail := func() (*springArchive, error) {
		return nil, errors.Join(configurationError("the packaged Spring application has invalid or ambiguous resources"),
			reader.Close())
	}
	if len(reader.File) > 50000 {
		return fail()
	}
	for _, file := range reader.File {
		if name, present := strings.CutPrefix(file.Name, "BOOT-INF/classes/"); present {
			if file.FileInfo().IsDir() {
				continue
			}
			if !fs.ValidPath(name) || !file.Mode().IsRegular() || archive.resources[name] != nil {
				return fail()
			}
			archive.resources[name] = file
		}
		if name, present := strings.CutPrefix(file.Name, "BOOT-INF/lib/"); present && strings.HasSuffix(name, ".jar") {
			archive.dependencies.database = archive.dependencies.database || isDatabaseDependency(name)
			switch {
			case strings.HasPrefix(name, "h2-"):
				archive.dependencies.h2 = true
			case strings.HasPrefix(name, "postgresql-"):
				archive.dependencies.postgresql = true
			case strings.HasPrefix(name, "azure-identity-extensions-"):
				archive.dependencies.azureIdentity = true
			case strings.HasPrefix(name, "spring-cloud-azure-starter-jdbc-postgresql-"):
				archive.dependencies.azureJDBC = true
			}
		}
	}
	if len(archive.resources) == 0 {
		return fail()
	}
	return archive, nil
}

func (a *springArchive) read(name string) ([]byte, error) {
	file := a.resources[name]
	if file == nil {
		return nil, fs.ErrNotExist
	}
	if file.UncompressedSize64 > 8*1024*1024 {
		return nil, configurationError("a packaged Spring configuration file exceeds the supported size")
	}
	reader, err := file.Open()
	if err != nil {
		return nil, configurationError("a packaged Spring configuration file cannot be opened")
	}
	data, readErr := readSpringConfigurationData(reader)
	return data, errors.Join(readErr, reader.Close())
}

func (a *springArchive) directory(name string) bool {
	prefix := strings.TrimSuffix(name, "/") + "/"
	if name == "" || name == "." {
		return true
	}
	for resource := range a.resources {
		if strings.HasPrefix(resource, prefix) {
			return true
		}
	}
	return false
}

func readSpringConfigurationData(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 8*1024*1024+1))
	if err != nil {
		return nil, configurationError("a Spring configuration file cannot be read")
	}
	if len(data) > 8*1024*1024 {
		return nil, configurationError("a Spring configuration file exceeds the supported size")
	}
	return data, nil
}

func readSpringDependencies(ctx context.Context, root string) (springDependencies, error) {
	if err := ctx.Err(); err != nil {
		return springDependencies{}, err
	}
	system, err := detectJavaBuild(root)
	if err != nil {
		return springDependencies{}, err
	}
	if system == gradleBuild {
		runner := azdexec.NewCommandRunner(&azdexec.RunnerOptions{
			Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard,
		})
		report, err := runGradle(ctx, runner, root, nil, "restore")
		if err != nil {
			return springDependencies{}, err
		}
		return springDependenciesFromCoordinates(report.Dependencies), nil
	}
	file, err := os.Open(filepath.Join(root, "pom.xml"))
	if err != nil {
		return springDependencies{}, configurationError("the application Maven dependency declaration cannot be read")
	}
	defer file.Close()
	data, err := readSpringConfigurationData(file)
	if err != nil {
		return springDependencies{}, err
	}
	type dependency struct {
		Group    string `xml:"groupId"`
		Artifact string `xml:"artifactId"`
		Scope    string `xml:"scope"`
	}
	var pom struct {
		Parent struct {
			Group    string `xml:"groupId"`
			Artifact string `xml:"artifactId"`
		} `xml:"parent"`
		Dependencies []dependency `xml:"dependencies>dependency"`
		Profiles     []struct {
			Dependencies []dependency `xml:"dependencies>dependency"`
		} `xml:"profiles>profile"`
	}
	if err := xml.Unmarshal(data, &pom); err != nil {
		return springDependencies{}, configurationError("the application Maven dependency declaration is invalid")
	}
	for _, profile := range pom.Profiles {
		for _, dependency := range profile.Dependencies {
			if dependency.Scope != "test" {
				return springDependencies{}, configurationError(
					"Maven profile dependencies require packaged JAR evidence",
				)
			}
		}
	}
	var coordinates []string
	for _, dependency := range pom.Dependencies {
		if dependency.Scope == "test" || dependency.Scope == "provided" || dependency.Scope == "system" {
			continue
		}
		if dependency.Group == "" || dependency.Artifact == "" ||
			strings.Contains(dependency.Group+dependency.Artifact+dependency.Scope, "${") {
			return springDependencies{}, configurationError(
				"unresolved Maven runtime dependencies require packaged JAR evidence",
			)
		}
		coordinates = append(coordinates, dependency.Group+":"+dependency.Artifact)
	}
	result := springDependenciesFromCoordinates(coordinates)
	if !result.h2 && !result.postgresql && pom.Parent.Artifact != "" &&
		(pom.Parent.Group != "org.springframework.boot" || pom.Parent.Artifact != "spring-boot-starter-parent") {
		return springDependencies{}, configurationError(
			"inherited Maven runtime dependencies require packaged JAR evidence",
		)
	}
	return result, nil
}

func springDependenciesFromCoordinates(coordinates []string) springDependencies {
	var result springDependencies
	for _, coordinate := range coordinates {
		_, artifact, _ := strings.Cut(coordinate, ":")
		result.database = result.database || isDatabaseDependency(artifact)
		switch coordinate {
		case "com.h2database:h2":
			result.h2 = true
		case "org.postgresql:postgresql":
			result.postgresql = true
		case "com.azure:azure-identity-extensions":
			result.azureIdentity = true
		case "com.azure.spring:spring-cloud-azure-starter-jdbc-postgresql":
			result.azureJDBC, result.azureIdentity, result.postgresql = true, true, true
		}
	}
	return result
}

func isDatabaseDependency(artifact string) bool {
	name := strings.ToLower(artifact)
	if strings.Contains(name, "jdbc") || strings.Contains(name, "r2dbc") {
		return true
	}
	for _, prefix := range []string{
		"spring-boot-starter-data-", "spring-data-", "hibernate-", "mybatis-", "hikaricp", "commons-dbcp",
		"mysql-connector", "mariadb-java-client", "hsqldb", "derby", "ojdbc", "mongodb-driver",
		"mongo-java-driver", "lettuce-core", "jedis", "neo4j-java-driver", "spring-boot-starter-jooq",
		"jooq", "flyway-", "liquibase-", "c3p0",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func springArchiveResource(location, relative string) (string, error) {
	name, classpath := strings.CutPrefix(location, "classpath:")
	if classpath {
		name = strings.TrimPrefix(name, "/")
	} else if after, ok := strings.CutPrefix(relative, "classpath:/"); ok {
		name = path.Join(path.Dir(after), location)
	} else {
		return "", configurationError("the packaged Spring configuration location cannot be mapped")
	}
	name = path.Clean(name)
	if (name != "." && !fs.ValidPath(name)) || strings.ContainsAny(name, `\:*`) {
		return "", configurationError("a packaged Spring configuration path is unsupported or outside the application")
	}
	if name == "." {
		name = ""
	}
	return "classpath:/" + name, nil
}
