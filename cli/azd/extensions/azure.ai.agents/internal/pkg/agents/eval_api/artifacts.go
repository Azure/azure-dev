// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"azureaiagent/internal/pkg/agents/dataset_api"
	"azureaiagent/internal/pkg/agents/opt_eval"
)

// Artifact directory names relative to the agent project root.
const (
	EvaluatorsDir         = "evaluators"
	DatasetsDir           = "datasets"
	EvaluatorContractFile = "rubric_dimensions.json"
)

// ResolveRelPath resolves a relative path against the agent project directory.
// If the path is already absolute it is returned as-is.
func ResolveRelPath(path, agentProject string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(agentProject, path)
}

// DownloadDatasetArtifact downloads the dataset and writes it locally.
// If the download fails (e.g., non-TLS test server), it returns nil gracefully.
// On success it returns the relative local URI (datasets/<name>/<version>/) for the
// downloaded directory. The SAS URI may point to a container (downloads all blobs)
// or a single blob.
func DownloadDatasetArtifact(
	ctx context.Context,
	client *dataset_api.DatasetClient,
	agentProject string,
	ref *opt_eval.DatasetRef,
	apiVersion string,
) (string, error) {
	if ref == nil || ref.Name == "" {
		return "", nil
	}

	// Attempt full download via the dataset API.
	cred, credErr := client.GetDatasetCredential(ctx, ref.Name, ref.Version, apiVersion)
	if credErr != nil {
		return "", fmt.Errorf("getting dataset credential for %q: %w", ref.Name, credErr)
	}

	downloadURL := cred.ResolvedDownloadURI()
	if downloadURL == "" {
		return "", fmt.Errorf("dataset %q returned empty download URI", ref.Name)
	}

	destDir := DatasetArtifactPath(agentProject, ref)

	// Clear existing dataset directory to ensure a clean download.
	if err := os.RemoveAll(destDir); err != nil {
		return "", fmt.Errorf("removing existing dataset dir: %w", err)
	}
	if err := os.MkdirAll(destDir, 0750); err != nil {
		return "", fmt.Errorf("creating dataset artifact dir: %w", err)
	}

	// Determine if this is a container-level SAS (sr=c) or blob-level.
	if isContainerSAS(downloadURL) {
		blobs, err := client.ListContainerBlobs(ctx, downloadURL)
		if err != nil {
			return "", fmt.Errorf("listing container blobs for dataset %q: %w", ref.Name, err)
		}
		if len(blobs) == 0 {
			return "", fmt.Errorf("dataset %q container has no blobs", ref.Name)
		}
		var errs []error
		for _, blobName := range blobs {
			ext := strings.ToLower(filepath.Ext(blobName))
			if ext != ".jsonl" && ext != ".csv" {
				continue
			}
			data, dlErr := client.DownloadBlob(ctx, downloadURL, blobName)
			if dlErr != nil {
				errs = append(errs, fmt.Errorf("downloading blob %q: %w", blobName, dlErr))
				continue
			}
			dest, pathErr := opt_eval.SafePath(destDir, blobName)
			if pathErr != nil {
				errs = append(errs, pathErr)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0750); err != nil {
				errs = append(errs, fmt.Errorf("creating dir for %q: %w", blobName, err))
				continue
			}
			if err := os.WriteFile(dest, data, 0600); err != nil {
				errs = append(errs, fmt.Errorf("writing %q: %w", blobName, err))
				continue
			}
		}
		if len(errs) > 0 {
			return "", errors.Join(errs...)
		}
	} else {
		// Single blob download.
		data, dlErr := client.DownloadDataset(ctx, downloadURL)
		if dlErr != nil {
			return "", fmt.Errorf("downloading dataset %q: %w", ref.Name, dlErr)
		}
		// Infer filename from URL.
		filename := filenameFromURL(downloadURL)
		dest := filepath.Join(destDir, filename)
		if err := os.WriteFile(dest, data, 0600); err != nil {
			return "", fmt.Errorf("writing dataset artifact: %w", err)
		}
	}

	return DatasetLocalURI(ref.Name), nil
}

// isContainerSAS checks if a SAS URI is container-scoped (sr=c in query).
func isContainerSAS(rawURL string) bool {
	_, query, ok := strings.Cut(rawURL, "?")
	if !ok {
		return false
	}
	// Look for sr=c parameter.
	return slices.Contains(strings.Split(query, "&"), "sr=c")
}

// filenameFromURL extracts the filename from a blob URL path.
// Falls back to "data.jsonl" if unable to determine.
func filenameFromURL(rawURL string) string {
	path := rawURL
	if idx := strings.IndexByte(path, '?'); idx != -1 {
		path = path[:idx]
	}
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		name := parts[len(parts)-1]
		if name != "" && strings.Contains(name, ".") {
			return name
		}
	}
	return "data.jsonl"
}

// DatasetArtifactPath returns the local filesystem path for a downloaded dataset directory.
func DatasetArtifactPath(agentProject string, ref *opt_eval.DatasetRef) string {
	if ref == nil || ref.Name == "" {
		return ""
	}
	return filepath.Join(agentProject, DatasetsDir, ref.Name)
}

// DatasetLocalURI returns the relative path (from the agent project root)
// to a dataset artifact directory. This is the value stored in DatasetRef.LocalURI.
func DatasetLocalURI(name string) string {
	return filepath.Join(DatasetsDir, name)
}

// evaluatorDir returns the full path to an evaluator's local directory.
func evaluatorDir(agentProject, name string) string {
	return filepath.Join(agentProject, EvaluatorsDir, name)
}

// EvaluatorLocalURI returns the relative path (from the agent project root)
// to an evaluator artifact file. This is the value stored in EvaluatorRef.LocalURI.
func EvaluatorLocalURI(name string) string {
	return filepath.Join(EvaluatorsDir, name, EvaluatorContractFile)
}

// SaveEvaluatorResult extracts the rubric dimensions from the evaluator result
// and saves them as the local artifact. Only dimensions are persisted so that
// users can edit weights/descriptions and upload a new evaluator version.
func SaveEvaluatorResult(agentProject, evaluatorName string, result json.RawMessage) error {
	if evaluatorName == "" || len(result) == 0 {
		return nil
	}
	dir := evaluatorDir(agentProject, evaluatorName)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("creating evaluator dir %q: %w", dir, err)
	}

	// Parse the evaluator result to extract the rubric dimensions.
	parsed := ParseEvaluatorResult(result)
	if parsed == nil || len(parsed.Definition.Dimensions) == 0 {
		return nil
	}

	formatted, err := json.MarshalIndent(parsed.Definition.Dimensions, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling evaluator dimensions: %w", err)
	}

	path := filepath.Join(dir, EvaluatorContractFile)
	if err := os.WriteFile(path, formatted, 0600); err != nil {
		return fmt.Errorf("writing evaluator artifact %q: %w", path, err)
	}
	return nil
}

// PrintEvaluatorDimensions prints a compact table of rubric dimensions.
func PrintEvaluatorDimensions(parsed *EvaluatorResult) {
	dims := parsed.Definition.Dimensions
	fmt.Printf("\n   Evaluator dimensions (%d):\n", len(dims))
	fmt.Println("     Weight  Dimension")
	fmt.Println("     ──────  ─────────")
	for _, d := range dims {
		fmt.Printf("     %6d  %s\n", d.Weight, d.ID)
	}
}

// WriteEvalReviewArtifacts writes human-readable review artifacts for evaluators.
// It writes a stub YAML file for each evaluator unless a result JSON already exists.
func WriteEvalReviewArtifacts(agentProject string, cfg *EvalConfig) error {
	if cfg == nil {
		return nil
	}
	var errs []error
	for _, evaluator := range cfg.Evaluators {
		if evaluator.Name == "" || IsBuiltinEvaluator(evaluator.Name) {
			continue
		}
		dir := evaluatorDir(agentProject, evaluator.Name)
		if err := os.MkdirAll(dir, 0750); err != nil {
			errs = append(errs, fmt.Errorf("creating dir for evaluator %q: %w", evaluator.Name, err))
			continue
		}
		// Skip if a result JSON already exists.
		jsonPath := filepath.Join(dir, EvaluatorContractFile)
		if _, err := os.Stat(jsonPath); err == nil {
			continue
		}
		yamlPath := filepath.Join(dir, evaluator.Name+".yaml")
		stub := fmt.Sprintf("# Evaluator stub: %s\nname: %s\n", evaluator.Name, evaluator.Name)
		if err := os.WriteFile(yamlPath, []byte(stub), 0600); err != nil {
			errs = append(errs, fmt.Errorf("writing evaluator stub %q: %w", yamlPath, err))
		}
	}
	return errors.Join(errs...)
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
