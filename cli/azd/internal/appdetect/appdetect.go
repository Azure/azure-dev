// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appdetect

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type ApplicationType string

const (
	// A front-end SPA / static web app.
	WebApp ApplicationType = "web"
	// API only.
	ApiApp ApplicationType = "api"
	// Fullstack solution. Front-end SPA with back-end API.
	ApiWeb ApplicationType = "api-web"
)

type Language string

const (
	DotNet Language = "dotnet"
	Java   Language = "java"
	NodeJs Language = "nodejs"
	Python Language = "python"
)

type Framework string

const (
	// Or just frontend?
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
	InferRule           string
	Docker              *Docker
}

type Docker struct {
	Path string
}

type Application struct {
	Type        ApplicationType
	Projects    []Project
	DisplayName string
}

func (a *Application) String() string {
	return a.DisplayName
}

type ProjectDetector interface {
	DisplayName() string
	DetectProject(path string, entries []fs.DirEntry) (*Project, error)
}

func Detect(root string) (*Application, error) {
	sourceDir := filepath.Join(root, "src")
	if ent, err := os.Stat(sourceDir); err == nil && ent.IsDir() {
		res, err := detect(sourceDir)
		if err != nil {
			return nil, err
		}

		if res != nil {
			return res, nil
		}
	}

	return detect(root)
}

func detect(root string) (*Application, error) {
	detectors := []ProjectDetector{
		// Order here determines precedence when two projects are in the same directory.
		// This case is very unlikely, but reordering could help to break the tie.
		&PythonDetector{},
		&NodeJsDetector{},
		&JavaDetector{},
		&DotNetDetector{},
	}

	app := Application{}

	detectDirectory := func(path string, entries []fs.DirEntry) error {
		isTestDir, err := filepath.Match("tests", path)
		if err != nil {
			return fmt.Errorf("determining test directory: %w", err)
		}

		isDotDir, err := filepath.Match(".*", path)
		if err != nil {
			return fmt.Errorf("determining hidden directory: %w", err)
		}

		if isTestDir || isDotDir {
			return filepath.SkipDir
		}

		for _, detector := range detectors {
			project, err := detector.DetectProject(path, entries)
			if err != nil {
				return fmt.Errorf("detecting %s project: %w", detector.DisplayName(), err)
			}

			if project != nil {
				// docker is an optional property of a project, and thus is different than other detectors
				docker, err := DetectDockerProject(path, entries)
				if err != nil {
					return fmt.Errorf("detecting docker project: %w", err)
				}
				project.Docker = docker

				app.Projects = append(app.Projects, *project)
				// Once a project is detected, we do not consider other projects to be a possibility.
				// We also do not consider other inner-projects under the directory, thus we return SkipDir.
				return filepath.SkipDir
			}
		}

		return nil
	}

	err := WalkDirectories(root, detectDirectory)
	if err != nil {
		return nil, fmt.Errorf("scanning directories: %w", err)
	}

	return &app, nil
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
