// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package appdetect allows for detection of projects, and their corresponding language or framework.
//
// Projects are detected based on criteria such as:
// 1. Presence of project files.
// 2. Source code language file extensions
//
// To determine frameworks, a project file might also be read for dependent packages.
//
// To understand how each project type is detected, see the correspond detector, named <project type>.go.
// For example, java.go.
package appdetect
