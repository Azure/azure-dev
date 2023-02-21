// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appdetect

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
)

type ProjectType string

const (
	DotNet     ProjectType = "dotnet"
	Java       ProjectType = "java"
	JavaScript ProjectType = "js"
	Python     ProjectType = "python"
)

type Framework string

const (
	React   Framework = "react"
	Angular Framework = "angular"
	VueJs   Framework = "vuejs"
	JQuery  Framework = "jquery"
)

func (f Framework) Display() string {
	switch f {
	case React:
		return "React"
	case Angular:
		return "Angular"
	case VueJs:
		return "Vue.js"
	case JQuery:
		return "JQuery"
	}

	return ""
}

func (f Framework) IsWebUIFramework() bool {
	switch f {
	case React, Angular, VueJs, JQuery:
		return true
	}

	return false
}

type Project struct {
	Language            string
	LanguageToolVersion string
	Frameworks          []Framework
	Path                string
	DetectionRule       string
	Docker              *Docker
}

func (p *Project) HasWebUIFramework() bool {
	for _, f := range p.Frameworks {
		if f.IsWebUIFramework() {
			return true
		}
	}

	return false
}

type Docker struct {
	Path string
}

type ProjectDetector interface {
	Type() ProjectType
	DetectProject(path string, entries []fs.DirEntry) (*Project, error)
}

var allDetectors = []ProjectDetector{
	// Order here determines precedence when two projects are in the same directory.
	// This is unlikely to occur in practice, but reordering could help to break the tie in these cases.
	&PythonDetector{},
	&JavaScriptDetector{},
	&JavaDetector{},
	&DotNetDetector{},
}

// Detects projects located under an application repository.
func Detect(repoRoot string, options ...DetectOption) ([]Project, error) {
	config := newConfig(options...)
	allProjects := []Project{}
	sourceDir := filepath.Join(repoRoot, "src")
	if ent, err := os.Stat(sourceDir); err == nil && ent.IsDir() {
		projects, err := detectUnder(sourceDir, config)
		if err != nil {
			return nil, err
		}

		if projects != nil {
			allProjects = append(allProjects, projects...)
		}
	}

	if len(allProjects) == 0 {
		config.ExcludePatterns = append(config.ExcludePatterns, "*/src/")
		projects, err := detectUnder(repoRoot, config)
		if err != nil {
			return nil, err
		}

		if projects != nil {
			allProjects = append(allProjects, projects...)
		}
	}

	return allProjects, nil
}

// DetectUnder detects projects located under a directory.
func DetectUnder(root string, options ...DetectOption) ([]Project, error) {
	config := newConfig(options...)
	return detectUnder(root, config)
}

// DetectDirectory detects the project located in a directory.
func DetectDirectory(directory string, options ...DetectDirectoryOption) (*Project, error) {
	config := newDirectoryConfig(options...)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("reading directory: %w", err)
	}

	return detectAny(config.detectors, directory, entries)
}

func detectUnder(root string, config detectConfig) ([]Project, error) {
	projects := []Project{}

	walkFunc := func(path string, entries []fs.DirEntry) error {
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		for _, p := range config.IncludePatterns {
			match, err := doublestar.Match(p, relativePath)
			if err != nil {
				return err
			}

			if !match {
				return filepath.SkipDir
			}
		}

		for _, p := range config.ExcludePatterns {
			match, err := doublestar.Match(p, relativePath)
			if err != nil {
				return err
			}
			if match {
				return filepath.SkipDir
			}
		}

		project, err := detectAny(config.detectors, path, entries)
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

	err := WalkDirectories(root, walkFunc)
	if err != nil {
		return nil, fmt.Errorf("scanning directories: %w", err)
	}

	return projects, nil
}

// Detects if a directory belongs to any projects.
func detectAny(detectors []ProjectDetector, path string, entries []fs.DirEntry) (*Project, error) {
	log.Printf("Detecting projects in directory: %s", path)
	for _, detector := range detectors {
		project, err := detector.DetectProject(path, entries)
		if err != nil {
			return nil, fmt.Errorf("detecting %s project: %w", string(detector.Type()), err)
		}

		if project != nil {
			log.Printf("Found project %s at %s", project.Language, path)

			// docker is an optional property of a project, and thus is different than other detectors
			docker, err := DetectDockerProject(path, entries)
			if err != nil {
				return nil, fmt.Errorf("detecting docker project: %w", err)
			}
			project.Docker = docker

			return project, nil
		}
	}

	return nil, nil
}

// WalkDirFunc is the type of function that is called whenever a directory is visited by WalkDirectories.
//
// path is the directory being visited. entries are the file entries (including directories) in that directory.
type WalkDirFunc func(path string, entries []fs.DirEntry) error

// WalkDirectories is like filepath.Walk, except it only visits directories.
//
// Unlike filepath.Walk, it also bubbles up errors by default, unless the error is SkipDir, in which the directory is skipped
// for any further walking.
func WalkDirectories(root string, fn WalkDirFunc) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}

	return walkDirRecursive(root, fs.FileInfoToDirEntry(info), fn)
}

func walkDirRecursive(path string, d fs.DirEntry, fn WalkDirFunc) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("reading directory: %w", err)
	}

	err = fn(path, entries)
	if err != nil {
		// do not bubble up error, and simply do not expand the directory further.
		if errors.Is(err, filepath.SkipDir) {
			return nil
		}

		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			newPath := filepath.Join(path, entry.Name())
			err = walkDirRecursive(newPath, entry, fn)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
