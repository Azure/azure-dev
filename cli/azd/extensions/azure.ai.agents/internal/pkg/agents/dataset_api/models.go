// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CreateDatasetRequest is the request body for creating (uploading) a dataset.
type CreateDatasetRequest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Format  string `json:"format"`
	Content string `json:"content"`
}

// Dataset is the response for dataset operations.
type Dataset struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	BlobURI    string `json:"blob_uri,omitempty"`
	Format     string `json:"format,omitempty"`
	DataURI    string `json:"data_uri,omitempty"`
	ContentURI string `json:"content_uri,omitempty"`
}

// ResolvedBlobURI returns the best available blob URI. Prefers blob_uri,
// falls back to data_uri, then content_uri.
func (d *Dataset) ResolvedBlobURI() string {
	if d.BlobURI != "" {
		return d.BlobURI
	}
	if d.DataURI != "" {
		return d.DataURI
	}
	return d.ContentURI
}

// DatasetCredential is the response for dataset credential (SAS token) requests.
// The API returns a nested structure with blobReference and blobReferenceForConsumption.
type DatasetCredential struct {
	// Flat fields (legacy format).
	BlobURI string `json:"blob_uri,omitempty"`
	SAS     string `json:"sas,omitempty"`
	SASUri  string `json:"sas_uri,omitempty"`

	// Nested fields (current API format).
	BlobReference            *BlobReference `json:"blobReference,omitempty"`
	BlobReferenceConsumption *BlobReference `json:"blobReferenceForConsumption,omitempty"`
}

// BlobReference represents a blob storage reference with credentials.
type BlobReference struct {
	BlobURI           string          `json:"blobUri,omitempty"`
	StorageAccountARM string          `json:"storageAccountArmId,omitempty"`
	Credential        *BlobCredential `json:"credential,omitempty"`
}

// BlobCredential holds SAS credential details for blob access.
type BlobCredential struct {
	Type    string `json:"type,omitempty"`
	SASUri  string `json:"sasUri,omitempty"`
	SASPath string `json:"sas,omitempty"`
}

// ResolvedDownloadURI returns the URL to download the dataset.
// Prefers blobReferenceForConsumption.credential.sasUri (current API),
// then blobReference.credential.sasUri, then flat sas_uri, then blob_uri + sas.
func (c *DatasetCredential) ResolvedDownloadURI() string {
	// Current API format: nested blob references.
	if c.BlobReferenceConsumption != nil && c.BlobReferenceConsumption.Credential != nil {
		if uri := c.BlobReferenceConsumption.Credential.SASUri; uri != "" {
			return uri
		}
	}
	if c.BlobReference != nil && c.BlobReference.Credential != nil {
		if uri := c.BlobReference.Credential.SASUri; uri != "" {
			return uri
		}
	}
	// Legacy flat format.
	if c.SASUri != "" {
		return c.SASUri
	}
	if c.BlobURI != "" && c.SAS != "" {
		return c.BlobURI + "?" + c.SAS
	}
	return c.BlobURI
}

// PendingUploadResponse is returned by the startPendingUpload endpoint.
// It contains a SAS URI for uploading blob data and the blob container URI.
type PendingUploadResponse struct {
	BlobReference            *BlobReference `json:"blobReference,omitempty"`
	BlobReferenceConsumption *BlobReference `json:"blobReferenceForConsumption,omitempty"`
	PendingUploadID          *string        `json:"pendingUploadId,omitempty"`
	PendingUploadType        string         `json:"pendingUploadType,omitempty"`
	Version                  string         `json:"version,omitempty"`
}

// ResolvedUploadURI returns the SAS URI for uploading blobs.
func (p *PendingUploadResponse) ResolvedUploadURI() string {
	if p.BlobReference != nil && p.BlobReference.Credential != nil {
		if uri := p.BlobReference.Credential.SASUri; uri != "" {
			return uri
		}
	}
	return ""
}

// ResolvedBlobURI returns the blob container URI (without SAS) for the finalize request.
func (p *PendingUploadResponse) ResolvedBlobURI() string {
	if p.BlobReference != nil {
		return p.BlobReference.BlobURI
	}
	return ""
}

// FinalizeDatasetRequest is the request body for finalizing a dataset version
// after blob upload.
type FinalizeDatasetRequest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Type        string `json:"type"`
	IsReference bool   `json:"isReference"`
	DataURI     string `json:"dataUri"`
}

// NextVersion computes the next dataset version string.
//
// Rules:
//  1. Empty → "1.0"
//  2. Parseable as a decimal number → increment by 1, format as "N.0"
//  3. Ends with trailing digits → increment the trailing numeric part
//  4. Otherwise → append ".1"
func NextVersion(current string) string {
	current = strings.TrimSpace(current)
	if current == "" {
		return "1.0"
	}

	// Try parsing as a decimal number (e.g. "1", "1.0", "2.0").
	if f, err := strconv.ParseFloat(current, 64); err == nil {
		return strconv.FormatFloat(f+1, 'f', 1, 64)
	}

	// Find trailing digits and increment them.
	i := len(current) - 1
	for i >= 0 && current[i] >= '0' && current[i] <= '9' {
		i--
	}
	if i < len(current)-1 {
		prefix := current[:i+1]
		n, err := strconv.Atoi(current[i+1:])
		if err == nil {
			return prefix + strconv.Itoa(n+1)
		}
	}

	return current + ".1"
}

// ReadFirstJSONLFile finds and reads the first .jsonl file in a directory.
func ReadFirstJSONLFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("reading directory: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".jsonl" {
			data, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // local artifact path
			if err != nil {
				return "", fmt.Errorf("reading %s: %w", e.Name(), err)
			}
			return string(data), nil
		}
	}
	return "", fmt.Errorf("no .jsonl file found in %s", dir)
}
