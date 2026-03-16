// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"azure.ai.customtraining/internal/azcopy"
	"azure.ai.customtraining/pkg/client"
	"azure.ai.customtraining/pkg/models"
)

// UploadService handles uploading local directories as datasets via the dataset API + azcopy.
type UploadService struct {
	client       *client.Client
	azcopyRunner *azcopy.Runner
}

// NewUploadService creates a new upload service.
func NewUploadService(apiClient *client.Client, azcopyRunner *azcopy.Runner) *UploadService {
	return &UploadService{
		client:       apiClient,
		azcopyRunner: azcopyRunner,
	}
}

// UploadResult contains the dataset reference after an upload.
type UploadResult struct {
	DatasetResourceID string // Full resource ID returned by PATCH (used as codeId or input uri)
	DatasetName       string
	DatasetVersion    string
	Skipped           bool // True if upload was skipped because a matching version already exists (dedup)
	Collision         bool // True if a hash collision was detected (caller should retry with unique naming)
}

// UploadDirectory uploads a local directory as a dataset version with content-based dedup.
//
// Dedup flow:
//  1. Compute a SHA-256 hash of the directory contents (file paths + data).
//  2. Truncate the hash to 49 chars and use it as the dataset version.
//  3. Call GET to check if that version already exists.
//  4. If it exists, skip upload and return the existing dataset resource ID.
//  5. If it doesn't exist, do the full upload: POST startPendingUpload → azcopy → PATCH confirm.
//
// Parameters:
//   - localPath: absolute or relative path to the local directory
//   - datasetName: name for the dataset (e.g., "code-myproject" or "input-training_data")
//   - description: human-readable description for the dataset
func (s *UploadService) UploadDirectory(
	ctx context.Context,
	localPath string,
	datasetName string,
	description string,
) (*UploadResult, error) {
	// Step 1: Compute content hash to use as version
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve local path %s: %w", localPath, err)
	}

	fullHash, err := ComputeDirectoryHash(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to compute hash for %s: %w", localPath, err)
	}
	version := TruncateHashVersion(fullHash)

	// Step 2: Check if this version already exists (dedup)
	existing, err := s.client.GetDatasetVersion(ctx, datasetName, version)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing dataset version: %w", err)
	}
	if existing != nil {
		// Version record exists — check the sentinel tag to verify upload completed
		storedHash, hasTag := existing.Tags["contentHash"]

		if !hasTag || storedHash == "" {
			// Zombie: POST created the version but azcopy/PATCH never completed.
			// Delete the incomplete version and re-upload from scratch.
			fmt.Printf("  (detected incomplete upload for %s, re-uploading)\n", datasetName)
			if err := s.client.DeleteDatasetVersion(ctx, datasetName, version); err != nil {
				return nil, fmt.Errorf("failed to delete zombie dataset version: %w", err)
			}
			// Fall through to fresh upload below
		} else if storedHash == fullHash {
			// Sentinel matches — exact same content was uploaded previously, skip
			return &UploadResult{
				DatasetResourceID: existing.ID,
				DatasetName:       datasetName,
				DatasetVersion:    version,
				Skipped:           true,
			}, nil
		} else {
			// Hash collision: truncated versions match but full hashes differ.
			// Two different directories produced the same 49-char prefix.
			// Signal to the caller to retry with job-scoped unique naming.
			return &UploadResult{
				Collision: true,
			}, nil
		}
	}

	// Step 3–5: Upload via the core flow (POST → azcopy → PATCH with sentinel tag)
	return s.doUploadWithTag(ctx, absPath, datasetName, version, description, fullHash)
}

// UploadDirectoryNoDedup uploads a local directory without content-based dedup.
// This is used as a fallback when a hash collision is detected.
// It always uploads with the given dataset name and version (typically "1").
func (s *UploadService) UploadDirectoryNoDedup(
	ctx context.Context,
	localPath string,
	datasetName string,
	version string,
	description string,
) (*UploadResult, error) {
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve local path %s: %w", localPath, err)
	}

	return s.doUpload(ctx, absPath, datasetName, version, description)
}

// doUploadWithTag performs the core upload flow with a sentinel tag on PATCH.
// Used by the dedup path to mark uploads as complete.
func (s *UploadService) doUploadWithTag(
	ctx context.Context,
	absPath string,
	datasetName string,
	version string,
	description string,
	fullHash string,
) (*UploadResult, error) {
	uploadResp, err := s.client.StartPendingUpload(ctx, datasetName, version)
	if err != nil {
		return nil, fmt.Errorf("failed to start pending upload: %w", err)
	}

	if uploadResp.BlobReference == nil || uploadResp.BlobReference.Credential.SASUri == "" {
		return nil, fmt.Errorf("no SAS URI returned from pending upload")
	}

	sasURI := uploadResp.BlobReference.Credential.SASUri
	blobURI := uploadResp.BlobReference.BlobURI

	if err := s.azcopyRunner.Copy(ctx, absPath, sasURI); err != nil {
		return nil, fmt.Errorf("failed to upload files from %s: %w", absPath, err)
	}

	// Include the full content hash as a sentinel tag. This serves as a completion
	// marker — if this tag is present on GET, we know both azcopy and PATCH succeeded.
	// Absence of this tag indicates a zombie (incomplete) upload.
	datasetReq := &models.DatasetVersion{
		DataURI:     blobURI,
		DataType:    "uri_folder",
		Description: description,
		Tags: map[string]string{
			"contentHash": fullHash,
		},
	}

	datasetResp, err := s.client.CreateOrUpdateDatasetVersion(ctx, datasetName, version, datasetReq)
	if err != nil {
		return nil, fmt.Errorf("failed to confirm dataset version: %w", err)
	}

	return &UploadResult{
		DatasetResourceID: datasetResp.ID,
		DatasetName:       datasetName,
		DatasetVersion:    version,
	}, nil
}

// doUpload performs the core upload flow: POST startPendingUpload → azcopy → PATCH confirm.
func (s *UploadService) doUpload(
	ctx context.Context,
	absPath string,
	datasetName string,
	version string,
	description string,
) (*UploadResult, error) {
	uploadResp, err := s.client.StartPendingUpload(ctx, datasetName, version)
	if err != nil {
		return nil, fmt.Errorf("failed to start pending upload: %w", err)
	}

	if uploadResp.BlobReference == nil || uploadResp.BlobReference.Credential.SASUri == "" {
		return nil, fmt.Errorf("no SAS URI returned from pending upload")
	}

	sasURI := uploadResp.BlobReference.Credential.SASUri
	blobURI := uploadResp.BlobReference.BlobURI

	if err := s.azcopyRunner.Copy(ctx, absPath, sasURI); err != nil {
		return nil, fmt.Errorf("failed to upload files from %s: %w", absPath, err)
	}

	datasetReq := &models.DatasetVersion{
		DataURI:     blobURI,
		DataType:    "uri_folder",
		Description: description,
	}

	datasetResp, err := s.client.CreateOrUpdateDatasetVersion(ctx, datasetName, version, datasetReq)
	if err != nil {
		return nil, fmt.Errorf("failed to confirm dataset version: %w", err)
	}

	return &UploadResult{
		DatasetResourceID: datasetResp.ID,
		DatasetName:       datasetName,
		DatasetVersion:    version,
	}, nil
}

// IsLocalPath returns true if the path is a local file/directory path (not a remote URI).
// Remote paths include https://, azureml://, wasbs://, etc.
func IsLocalPath(path string) bool {
	if path == "" {
		return false
	}

	// Remote URI schemes
	remoteSchemes := []string{
		"https://",
		"http://",
		"azureml://",
		"wasbs://",
		"abfss://",
	}
	lowerPath := strings.ToLower(path)
	for _, scheme := range remoteSchemes {
		if strings.HasPrefix(lowerPath, scheme) {
			return false
		}
	}

	// If it starts with azureml: (short form like azureml:name:version), it's remote
	if strings.HasPrefix(lowerPath, "azureml:") {
		return false
	}

	return true
}
