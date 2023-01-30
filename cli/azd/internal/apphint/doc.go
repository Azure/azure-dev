// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package appdetect detects different projects within an application directory.
//
// A source directory (typically ./src) can contain multiple different projects of different languages or frameworks.
// Languages and frameworks are determined individually based on file directory structure, file extension,
// and a list of languages and frameworks are collected.
// Finally, the list of languages and frameworks are matched with well-known application types.
//
// To understand how languages and frameworks are detected, see <TODO>.
//
// To understand how application types are matched, see <TODO>.
package apphint
