// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"azureaiagent/internal/pkg/agents/dataset_api"
	"azureaiagent/internal/pkg/agents/opteval"
)

// foundryDir is the directory under .azure where eval artifacts are stored.
const foundryDir = ".azure/.foundry"

// ResolveEvalOutputPath resolves the eval output config path. If output is
// already absolute it is returned as-is; otherwise it is joined with the
// agent project directory.
func ResolveEvalOutputPath(output, agentProject string) string {
	if filepath.IsAbs(output) {
		return output
	}
	return filepath.Join(agentProject, output)
}

// ResolveEvalConfigPath resolves the eval config path for reading. Follows the
// same logic as ResolveEvalOutputPath.
func ResolveEvalConfigPath(config, agentProject string) string {
	return ResolveEvalOutputPath(config, agentProject)
}

// EnsureFoundryDirs creates the .azure/.foundry directory tree under the
// project root if it doesn't already exist.
func EnsureFoundryDirs(projectRoot string) error {
	dir := filepath.Join(projectRoot, foundryDir)
	return os.MkdirAll(dir, 0750)
}

// DownloadDatasetArtifact downloads the dataset referenced by dsRef and saves
// it under .azure/.foundry/datasets/<name>.jsonl.
func DownloadDatasetArtifact(
	ctx context.Context,
	client *dataset_api.DatasetClient,
	projectRoot string,
	dsRef *opteval.DatasetRef,
	apiVersion string,
) error {
	if dsRef == nil || dsRef.Name == "" {
		return fmt.Errorf("dataset reference is empty")
	}

	ds, err := client.GetDataset(ctx, dsRef.Name, dsRef.Version, apiVersion)
	if err != nil {
		return fmt.Errorf("failed to get dataset %q: %w", dsRef.Name, err)
	}

	cred, err := client.GetDatasetCredential(ctx, dsRef.Name, dsRef.Version, apiVersion)
	if err != nil {
		return fmt.Errorf("failed to get dataset credential: %w", err)
	}

	downloadURL := cred.ResolvedDownloadURI()
	if downloadURL == "" {
		downloadURL = ds.ResolvedBlobURI()
	}
	if downloadURL == "" {
		return fmt.Errorf("no download URL available for dataset %q", dsRef.Name)
	}

	data, err := client.DownloadDataset(ctx, downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download dataset: %w", err)
	}

	dir := filepath.Join(projectRoot, foundryDir, "datasets")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create dataset dir: %w", err)
	}

	path := filepath.Join(dir, dsRef.Name+".jsonl")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write dataset artifact: %w", err)
	}

	return nil
}

// DatasetArtifactPath returns the local path where a downloaded dataset
// artifact is stored.
func DatasetArtifactPath(projectRoot string, dsRef *opteval.DatasetRef) string {
	if dsRef == nil || dsRef.Name == "" {
		return ""
	}
	return filepath.Join(projectRoot, foundryDir, "datasets", dsRef.Name+".jsonl")
}

// SaveEvaluatorResult saves the raw JSON result of an evaluator generation job
// under .azure/.foundry/evaluators/<name>.json.
func SaveEvaluatorResult(projectRoot, evaluatorName string, result json.RawMessage) {
	if evaluatorName == "" || len(result) == 0 {
		return
	}
	dir := filepath.Join(projectRoot, foundryDir, "evaluators")
	if err := os.MkdirAll(dir, 0750); err != nil {
		log.Printf("[debug] failed to create evaluator dir: %v", err)
		return
	}
	path := filepath.Join(dir, evaluatorName+".json")
	if err := os.WriteFile(path, result, 0600); err != nil {
		log.Printf("[debug] failed to save evaluator result: %v", err)
	}
}

// WriteEvalReviewArtifacts writes human-readable review artifacts for the eval
// config under .azure/.foundry/review/.
func WriteEvalReviewArtifacts(projectRoot string, cfg *EvalConfig) {
	if cfg == nil {
		return
	}
	dir := filepath.Join(projectRoot, foundryDir, "review")
	if err := os.MkdirAll(dir, 0750); err != nil {
		log.Printf("[debug] failed to create review dir: %v", err)
		return
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		log.Printf("[debug] failed to marshal eval config for review: %v", err)
		return
	}
	path := filepath.Join(dir, "eval-config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		log.Printf("[debug] failed to write review artifact: %v", err)
	}
}

// WriteJSONFile writes a value as indented JSON to the specified path.
func WriteJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling JSON: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// FormatTimestamp formats a timestamp value (int64, float64, or string) as a
// human-readable UTC string.
func FormatTimestamp(ts any) string {
	switch v := ts.(type) {
	case int64:
		if v == 0 {
			return ""
		}
		return time.Unix(v, 0).UTC().Format("2006-01-02 15:04:05 UTC")
	case float64:
		if v == 0 {
			return ""
		}
		return time.Unix(int64(v), 0).UTC().Format("2006-01-02 15:04:05 UTC")
	case int:
		if v == 0 {
			return ""
		}
		return time.Unix(int64(v), 0).UTC().Format("2006-01-02 15:04:05 UTC")
	case string:
		if v == "" {
			return ""
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return v
		}
		return t.UTC().Format("2006-01-02 15:04:05 UTC")
	default:
		return ""
	}
}
