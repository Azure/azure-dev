// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package appdetect detects different projects within an application directory.
//
// Projects are first looked under ./src. If no projects are found, projects
// Languages and frameworks are determined individually based on file directory structure, file extension,
// and a list of languages and frameworks are collected.
//
// To understand how languages and frameworks are detected, see <TODO>.
//
// To understand how application types are matched, see <TODO>.
package appdetect
