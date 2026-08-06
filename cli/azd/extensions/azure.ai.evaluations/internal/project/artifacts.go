// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Conventional artifact locations, relative to the eval directory.
const (
	DefaultDatasetsDir   = "datasets"
	DefaultEvaluatorsDir = "evaluators"
)

// ArtifactRef is the name/source pair a generation run produces, so the
// command can tell the developer how to reference it.
type ArtifactRef struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// Sample-count bounds enforced by the generation service.
const (
	MinSampleSize     = 15
	MaxSampleSize     = 1000
	DefaultSampleSize = 15
)

// Sources a dataset can be generated from.
const (
	GenerateFromTraces = "traces"
	GenerateFromAgent  = "agent"
	GenerateFromPrompt = "prompt"
	GenerateFromFile   = "file"
)

// GenerateSources is what --from accepts, in help order.
var GenerateSources = []string{
	GenerateFromTraces, GenerateFromAgent, GenerateFromPrompt, GenerateFromFile,
}

// ValidateGenerateSource rejects a --from value the service has no path for.
func ValidateGenerateSource(from string) error {
	switch from {
	case "", GenerateFromTraces, GenerateFromAgent, GenerateFromPrompt, GenerateFromFile:
		return nil
	default:
		return fmt.Errorf(
			"--from %q is not a source; use one of %s",
			from, strings.Join(GenerateSources, ", "))
	}
}

// ValidateSampleSize rejects a row count the service would reject, before a
// generation job is submitted and billed.
func ValidateSampleSize(n int) error {
	if n != 0 && (n < MinSampleSize || n > MaxSampleSize) {
		return fmt.Errorf(
			"sample size must be between %d and %d, got %d",
			MinSampleSize, MaxSampleSize, n)
	}
	return nil
}

// ArtifactPath resolves an output directory against baseDir. The value may be a
// directory, in which case the file name is derived from resourceName and ext,
// or an explicit file path, which is used as-is.
func ArtifactPath(baseDir, outputDir, resourceName, ext string) string {
	if outputDir == "" {
		return filepath.Join(baseDir, resourceName+ext)
	}
	candidate := outputDir
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(baseDir, candidate)
	}
	if looksLikeFile(outputDir, ext) {
		return candidate
	}
	return filepath.Join(candidate, resourceName+ext)
}

// looksLikeFile treats a trailing recognized extension as an explicit file path.
func looksLikeFile(p, ext string) bool {
	got := strings.ToLower(filepath.Ext(p))
	if got == "" {
		return false
	}
	if got == strings.ToLower(ext) {
		return true
	}
	switch got {
	case ".json", ".jsonl", ".yaml", ".yml":
		return true
	}
	return false
}
