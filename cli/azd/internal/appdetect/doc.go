// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package appdetect allows for detection of application projects.
//
// Projects are detected based on criteria such as:
// 1. Presence of project files.
// 2. Source code language file extensions
//
// To determine dependencies, a project file might also be read for dependent packages.
//
// - `Detect()` to detect all projects under a root directory.
// - `DetectDirectory` to detect a project under a given directory.
package appdetect
