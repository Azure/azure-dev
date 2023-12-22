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
	JsVue     Dependency = "vuejs"
	JsJQuery  Dependency = "jquery"

	PyFlask   Dependency = "flask"
	PyDjango  Dependency = "django"
	PyFastApi Dependency = "fastapi"
)

var WebUIFrameworks = map[Dependency]struct{}{
	JsReact:   {},
	JsAngular: {},
	JsVue:     {},
	JsJQuery:  {},
}

func (f Dependency) Language() Language {
	switch f {
	case JsReact, JsAngular, JsVue, JsJQuery:
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
	case JsVue:
		return "Vue.js"
	case JsJQuery:
		return "JQuery"
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
	case DbSqlServer:
		return "SQL Server"
	case DbRedis:
		return "Redis"
	}

	return ""
}

type Project struct {
	// The language associated with the project.
	Language Language

	// Dependencies scanned in the project.
	Dependencies []Dependency

	// Experimental: Database dependencies inferred through heuristics while scanning dependencies in the project.
	DatabaseDeps []DatabaseDep

	// The path to the project directory.
	Path string

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

type Docker struct {
	Path string
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
		dotnetCli: dotnet.NewDotNetCli(exec.NewCommandRunner(nil)),
	},
	&dotNetDetector{
		dotnetCli: dotnet.NewDotNetCli(exec.NewCommandRunner(nil)),
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

			// docker is an optional property of a project, and thus is different than other detectors
			docker, err := detectDocker(path, entries)
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

// walkDirectories walks the file tree rooted at root, calling fn for each directory in the tree, including root.
// The directories are walked in lexical order.
func walkDirectories(root string, fn walkDirFunc) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}

	return walkDirRecursive(root, fs.FileInfoToDirEntry(info), fn)
}

func walkDirRecursive(path string, d fs.DirEntry, fn walkDirFunc) error {
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
			err = walkDirRecursive(dir, entry, fn)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
