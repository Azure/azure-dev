// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package service

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/google/uuid"
)

// DefaultCodeResolver uploads a local code directory as a dataset and returns the dataset resource ID.
// It uses content-based dedup via UploadService: if the same directory contents were uploaded before,
// the existing dataset version is reused. On the rare hash collision, it falls back to job-scoped naming.
type DefaultCodeResolver struct {
	uploadSvc   *UploadService
	projectName string
	yamlDir     string
}

// NewDefaultCodeResolver creates a code resolver that uploads local code directories.
//   - uploadSvc: handles the actual dataset upload (POST → azcopy → PATCH) with dedup
//   - projectName: used for content-scoped naming (e.g., "code-{projectName}")
//   - yamlDir: base directory for resolving relative code paths in the YAML
func NewDefaultCodeResolver(uploadSvc *UploadService, projectName, yamlDir string) *DefaultCodeResolver {
	return &DefaultCodeResolver{
		uploadSvc:   uploadSvc,
		projectName: projectName,
		yamlDir:     yamlDir,
	}
}

// ResolveCode uploads a local code directory and returns the dataset resource ID.
// The codePath is the raw value from the YAML (e.g., "./src" or "../code").
func (r *DefaultCodeResolver) ResolveCode(ctx context.Context, codePath string) (string, error) {
	// Resolve relative paths against the YAML file's directory
	resolvedPath := codePath
	if !filepath.IsAbs(resolvedPath) {
		resolvedPath = filepath.Join(r.yamlDir, resolvedPath)
	}

	// Content-scoped naming: code-{projectName}. Dedup is handled by version (content hash).
	assetName := fmt.Sprintf("code-%s", r.projectName)
	fmt.Printf("  Uploading code (%s)...\n", codePath)

	result, err := r.uploadSvc.UploadDirectory(
		ctx, resolvedPath, assetName,
		fmt.Sprintf("Code for project %s", r.projectName),
	)
	if err != nil {
		return "", fmt.Errorf("failed to upload code: %w", err)
	}

	// Hash collision fallback: use a unique name without dedup
	if result.Collision {
		fallbackName := fmt.Sprintf("code-%s", uuid.New().String())
		fmt.Printf("  (hash collision on %s, falling back to %s)\n", assetName, fallbackName)
		result, err = r.uploadSvc.UploadDirectoryNoDedup(
			ctx, resolvedPath, fallbackName, "1",
			fmt.Sprintf("Code for project %s (collision fallback)", r.projectName),
		)
		if err != nil {
			return "", fmt.Errorf("failed to upload code (fallback): %w", err)
		}
	}

	if result.Skipped {
		fmt.Printf("  ✓ Code unchanged, reusing existing upload (dataset: %s, version: %s)\n",
			result.DatasetName, result.DatasetVersion)
	} else {
		fmt.Printf("  ✓ Code uploaded (dataset: %s, version: %s)\n",
			result.DatasetName, result.DatasetVersion)
	}

	return result.DatasetResourceID, nil
}
