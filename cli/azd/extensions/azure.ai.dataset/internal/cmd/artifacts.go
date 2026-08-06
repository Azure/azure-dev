// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
)

// The sample count the service accepts, and the default the spec documents.
const (
	minSampleSize     = 15
	maxSampleSize     = 1000
	defaultSampleSize = 15
)

// defaultOutputDir is where a generated dataset lands, matching the layout
// `azd ai eval init` scaffolds so a generated file is already where the eval
// configuration expects to find it.
const defaultOutputDir = "evals/datasets"

// Sources a dataset can be generated from.
const (
	generateFromTraces = "traces"
	generateFromAgent  = "agent"
	generateFromPrompt = "prompt"
	generateFromFile   = "file"
)

// generateSources is what --from accepts, in help order.
var generateSources = []string{
	generateFromTraces, generateFromAgent, generateFromPrompt, generateFromFile,
}

// validateGenerateSource rejects a --from value the service has no path for.
func validateGenerateSource(from string) error {
	switch from {
	case "", generateFromTraces, generateFromAgent, generateFromPrompt, generateFromFile:
		return nil
	default:
		return fmt.Errorf(
			"--from %q is not a source; use one of %s",
			from, strings.Join(generateSources, ", "))
	}
}

// validateSampleSize rejects a row count the service would reject, before a
// generation job is submitted and billed.
func validateSampleSize(n int) error {
	if n != 0 && (n < minSampleSize || n > maxSampleSize) {
		return fmt.Errorf(
			"--max-samples must be between %d and %d, got %d",
			minSampleSize, maxSampleSize, n)
	}
	return nil
}

// artifactPath is where a generated dataset is written.
func artifactPath(outputDir, name string) string {
	return filepath.Join(outputDir, name+".jsonl")
}

// envKeyDatasetVersion caches the version resolved at the last publish, so a
// later read does not have to list every version to find the newest.
const envKeyDatasetVersion = "EVAL_DATASET_VERSION"

// checkAssetExistence enforces the one difference between create and update.
func checkAssetExistence(verb, kind, name string, exists bool) error {
	switch {
	case verb == "create" && exists:
		return fmt.Errorf(
			"%s %q already exists: use `update` to publish a new version", kind, name)
	case verb == "update" && !exists:
		return fmt.Errorf(
			"%s %q does not exist: use `create` to register it", kind, name)
	}
	return nil
}

// defaultGenerationSource picks what `dataset generate` sends when --from was
// not given, from the Application Insights connection string the project has
// (or has not) been given.
//
// Traces are the better dataset when they exist, being real conversations
// rather than synthesized ones, so they win whenever the project is wired to
// collect them. Outside a project, or in one with no Application Insights,
// there are no traces to ask for and the agent's own definition is all that is
// left.
func defaultGenerationSource(appInsightsConnection string) []string {
	if appInsightsConnection != "" {
		return []string{generateFromTraces}
	}
	return []string{generateFromAgent}
}
