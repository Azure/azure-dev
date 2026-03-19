// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// DefaultInputResolver uploads a local input directory as a dataset and returns the dataset resource ID.
// It uses content-based dedup via UploadService: if the same directory contents were uploaded before,
// the existing dataset version is reused. On the rare hash collision, it falls back to job-scoped naming.
type DefaultInputResolver struct {
	uploadSvc   *UploadService
	projectName string
}

// NewDefaultInputResolver creates an input resolver that uploads local input directories.
//   - uploadSvc: handles the actual dataset upload (POST → azcopy → PATCH) with dedup
//   - projectName: used in dataset descriptions
func NewDefaultInputResolver(uploadSvc *UploadService, projectName string) *DefaultInputResolver {
	return &DefaultInputResolver{
		uploadSvc:   uploadSvc,
		projectName: projectName,
	}
}

// ResolveInput uploads a local input directory and returns the dataset resource ID.
// The inputPath must be an absolute path (relative paths should be resolved by the caller).
// inputName is the YAML key (e.g., "training_data") used for dataset naming.
func (r *DefaultInputResolver) ResolveInput(ctx context.Context, inputName string, inputPath string, inputType string) (string, error) {
	// Content-scoped naming: input-{inputName}. Dedup is handled by version (content hash).
	assetName := fmt.Sprintf("input-%s", inputName)
	fmt.Printf("  ├─ %s: uploading %s...\n", inputName, inputPath)

	result, err := r.uploadSvc.UploadDirectory(
		ctx, inputPath, assetName,
		fmt.Sprintf("Input %s for project %s", inputName, r.projectName),
	)
	if err != nil {
		return "", fmt.Errorf("failed to upload input %s: %w", inputName, err)
	}

	// Hash collision fallback: use a unique name without dedup
	if result.Collision {
		fallbackName := fmt.Sprintf("input-%s-%s", uuid.New().String(), inputName)
		fmt.Printf("  (hash collision on %s, falling back to %s)\n", assetName, fallbackName)
		result, err = r.uploadSvc.UploadDirectoryNoDedup(
			ctx, inputPath, fallbackName, "1",
			fmt.Sprintf("Input %s for project %s (collision fallback)", inputName, r.projectName),
		)
		if err != nil {
			return "", fmt.Errorf("failed to upload input %s (fallback): %w", inputName, err)
		}
	}

	if result.Skipped {
		fmt.Printf("  ✓ %s unchanged, reusing existing upload (version: %s)\n",
			inputName, result.DatasetVersion)
	} else {
		fmt.Printf("  ✓ %s uploaded (dataset: %s, version: %s)\n",
			inputName, result.DatasetName, result.DatasetVersion)
	}

	return result.DatasetResourceID, nil
}
