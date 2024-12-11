// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appdetect

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/bmatcuk/doublestar/v4"
)

type Language string

const (
	DotNet        Language = "dotnet"
	DotNetAppHost Language = "dotnet-apphost"
	Java          Language = "java"
	JavaScript    Language = "js"
	TypeScript    Language = "ts"
	Python        Language = "python"
)

func (pt Language) Display() string {
	switch pt {
	case DotNet:
		return ".NET"
	case DotNetAppHost:
		return ".NET (Aspire)"
	case Java:
		return "Java"
	case JavaScript:
		return "JavaScript"
	case TypeScript:
		return "TypeScript"
	case Python:
		return "Python"
	}

	return ""
}

type Dependency string

const (
	JsReact   Dependency = "react"
	JsAngular Dependency = "angular"
	JsJQuery  Dependency = "jquery"
	JsVite    Dependency = "vite"
	JsNext    Dependency = "next"

	PyFlask   Dependency = "flask"
	PyDjango  Dependency = "django"
	PyFastApi Dependency = "fastapi"

	SpringFrontend Dependency = "springFrontend"
)

var WebUIFrameworks = map[Dependency]struct{}{
	JsReact:        {},
	JsAngular:      {},
	JsJQuery:       {},
	JsVite:         {},
	SpringFrontend: {},
}

func (f Dependency) Language() Language {
	switch f {
	case JsReact, JsAngular, JsJQuery, JsVite:
		return JavaScript
	}

	return ""
}

func (f Dependency) Display() string {
	switch f {
	case JsReact:
		return "React"
	case JsAngular:
		return "Angular"
	case JsJQuery:
		return "JQuery"
	case JsVite:
		return "Vite"
	case JsNext:
		return "Next.js"
	}

	return ""
}

func (f Dependency) IsWebUIFramework() bool {
	if _, ok := WebUIFrameworks[f]; ok {
		return true
	}

	return false
}

// A type of database that is inferred through heuristics while scanning project information.
type DatabaseDep string

const (
	// Database dependencies
	DbPostgres  DatabaseDep = "postgres"
	DbMongo     DatabaseDep = "mongo"
	DbMySql     DatabaseDep = "mysql"
	DbCosmos    DatabaseDep = "cosmos"
	DbSqlServer DatabaseDep = "sqlserver"
	DbRedis     DatabaseDep = "redis"
)

func (db DatabaseDep) Display() string {
	switch db {
	case DbPostgres:
		return "PostgreSQL"
	case DbMongo:
		return "MongoDB"
	case DbMySql:
		return "MySQL"
	case DbCosmos:
		return "Cosmos DB"
	case DbSqlServer:
		return "SQL Server"
	case DbRedis:
		return "Redis"
	}

	return ""
}

//type AzureDep string

type AzureDep interface {
	ResourceDisplay() string
}

type AzureDepServiceBus struct {
	Queues []string
	IsJms  bool
}

func (a AzureDepServiceBus) ResourceDisplay() string {
	return "Azure Service Bus"
}

type AzureDepEventHubs struct {
	Names             []string
	UseKafka          bool
	SpringBootVersion string
}

func (a AzureDepEventHubs) ResourceDisplay() string {
	return "Azure Event Hubs"
}

type AzureDepStorageAccount struct {
	ContainerNames []string
}

func (a AzureDepStorageAccount) ResourceDisplay() string {
	return "Azure Storage Account"
}

type Metadata struct {
	ApplicationName                                         string
	DatabaseNameInPropertySpringDatasourceUrl               map[DatabaseDep]string
	ContainsDependencySpringCloudAzureStarter               bool
	ContainsDependencySpringCloudAzureStarterJdbcPostgresql bool
	ContainsDependencySpringCloudAzureStarterJdbcMysql      bool
	ContainsDependencySpringCloudEurekaServer               bool
	ContainsDependencySpringCloudEurekaClient               bool
	ContainsDependencySpringCloudConfigServer               bool
	ContainsDependencySpringCloudConfigClient               bool
}

const UnknownSpringBootVersion string = "unknownSpringBootVersion"

type Project struct {
	// The language associated with the project.
	Language Language

	// Dependencies scanned in the project.
	Dependencies []Dependency

	// Experimental: Database dependencies inferred through heuristics while scanning dependencies in the project.
	DatabaseDeps []DatabaseDep

	// Experimental: Azure dependencies inferred through heuristics while scanning dependencies in the project.
	AzureDeps []AzureDep

	// Experimental: Metadata inferred through heuristics while scanning the project.
	Metadata Metadata

	// The path to the project directory.
	Path string

	Options map[string]interface{}

	// A short description of the detection rule applied.
	DetectionRule string

	// If true, the project uses Docker for packaging. This is inferred through the presence of a Dockerfile.
	Docker *Docker
}

func (p *Project) HasWebUIFramework() bool {
	for _, f := range p.Dependencies {
		if f.IsWebUIFramework() {
			return true
		}
	}

	return false
}

type Port struct {
	Number   int
	Protocol string
}

type Docker struct {
	Path  string
	Ports []Port
}

type projectDetector interface {
	Language() Language
	DetectProject(ctx context.Context, path string, entries []fs.DirEntry) (*Project, error)
}

var allDetectors = []projectDetector{
	// Order here determines precedence when two projects are in the same directory.
	// This is unlikely to occur in practice, but reordering could help to break the tie in these cases.
	&javaDetector{},
	&dotNetAppHostDetector{
		// TODO(ellismg): Remove ambient authority.
		dotnetCli: dotnet.NewCli(exec.NewCommandRunner(nil)),
	},
	&dotNetDetector{
		dotnetCli: dotnet.NewCli(exec.NewCommandRunner(nil)),
	},
	&pythonDetector{},
	&javaScriptDetector{},
}

// Detect detects projects located under a directory.
func Detect(ctx context.Context, root string, options ...DetectOption) ([]Project, error) {
	config := newConfig(options...)
	return detectUnder(ctx, root, config)
}

// DetectDirectory detects the project located in a directory.
func DetectDirectory(ctx context.Context, directory string, options ...DetectDirectoryOption) (*Project, error) {
	config := newDirectoryConfig(options...)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("reading directory: %w", err)
	}

	return detectAny(ctx, config.detectors, directory, entries)
}

func detectUnder(ctx context.Context, root string, config detectConfig) ([]Project, error) {
	projects := []Project{}

	walkFunc := func(path string, entries []fs.DirEntry) error {
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		// Normalize all paths to use forward slash '/' for glob matching
		relativePathForMatch := filepath.ToSlash(relativePath)

		for _, p := range config.ExcludePatterns {
			match, err := doublestar.Match(p, relativePathForMatch)
			if err != nil {
				return err
			}
			if match {
				return filepath.SkipDir
			}
		}

		project, err := detectAny(ctx, config.detectors, path, entries)
		if err != nil {
			return err
		}

		if project != nil {
			// Once a project is detected, we skip possible inner projects.
			projects = append(projects, *project)
			return filepath.SkipDir
		}

		return nil
	}

	err := walkDirectories(root, walkFunc)
	if err != nil {
		return nil, fmt.Errorf("scanning directories: %w", err)
	}

	return projects, nil
}

// Detects if a directory belongs to any projects.
func detectAny(ctx context.Context, detectors []projectDetector, path string, entries []fs.DirEntry) (*Project, error) {
	log.Printf("Detecting projects in directory: %s", path)
	for _, detector := range detectors {
		project, err := detector.DetectProject(ctx, path, entries)
		if err != nil {
			return nil, fmt.Errorf("detecting %s project: %w", string(detector.Language()), err)
		}

		if project != nil {
			log.Printf("Found project %s at %s", project.Language, path)

			// docker is an optional property of a project, and thus is different from other detectors
			docker, err := detectDockerInDirectory(path, entries)
			if err != nil {
				return nil, fmt.Errorf("detecting docker project: %w", err)
			}
			project.Docker = docker

			return project, nil
		}
	}

	return nil, nil
}

// walkDirFunc is the type of function that is called whenever a directory is visited by WalkDirectories.
//
// path is the directory being visited. entries are the file entries (including directories) in that directory.
type walkDirFunc func(path string, entries []fs.DirEntry) error

// walkDirectories recursively descends the file tree located at path, calling fn for each directory in the tree.
// The directories are walked in lexical order.
func walkDirectories(path string, fn walkDirFunc) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("reading directory: %w", err)
	}

	err = fn(path, entries)
	if errors.Is(err, filepath.SkipDir) {
		// skip the directory
		return nil
	}
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			dir := filepath.Join(path, entry.Name())
			err = walkDirectories(dir, fn)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
