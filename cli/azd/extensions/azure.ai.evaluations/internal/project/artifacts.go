// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"path/filepath"
	"strings"

	"azureaieval/internal/messages"
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
	// Version is what the generation job published. It is reported to the
	// author rather than written into the catalog: an evaluator cannot carry
	// both a `source:` and a `version:`, and pinning a generated dataset would
	// freeze it against the very edit it exists to be the starting point for.
	Version string `json:"version,omitempty"`
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

// GenerateSources is what --from offers, in help order.
//
// `file` is missing on purpose. It is a source the command recognizes so that
// asking for it earns the remedy rather than a list, but generate never builds
// from one, so offering it in help would advertise a value the same command
// guarantees to refuse.
var GenerateSources = []string{
	GenerateFromTraces, GenerateFromAgent, GenerateFromPrompt,
}

// ValidateGenerateSource rejects a --from value the service has no path for.
func ValidateGenerateSource(from string) error {
	switch from {
	case "", GenerateFromTraces, GenerateFromAgent, GenerateFromPrompt, GenerateFromFile:
		return nil
	default:
		return messages.FromNotASource(from, GenerateSources)
	}
}

// ValidateSampleSize rejects a row count the service would reject, before a
// generation job is submitted and billed.
func ValidateSampleSize(n int) error {
	if n != 0 && (n < MinSampleSize || n > MaxSampleSize) {
		return messages.SampleSizeOutOfRange(MinSampleSize, MaxSampleSize, n)
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

// OutputDirNamesAFile reports an --output-dir that names a file rather than a
// directory, which is a thing only one artifact can be written to.
func OutputDirNamesAFile(outputDir string) bool {
	return looksLikeFile(outputDir, "")
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
