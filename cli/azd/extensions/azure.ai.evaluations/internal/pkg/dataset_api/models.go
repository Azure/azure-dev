// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"azureaieval/internal/messages"
)

// CreateDatasetRequest is the request body for creating (uploading) a dataset.
type CreateDatasetRequest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Format  string `json:"format"`
	Content string `json:"content"`
}

// Dataset is the response for dataset operations.
//
// The field spelling is not consistent across the surface: the live
// project-endpoint GET returns camelCase (dataUri, isSingleFile), while other
// paths have used snake_case (data_uri, blob_uri, content_uri). Both spellings
// are accepted here because binding only one silently yields an empty URI,
// which then fails much later at download time.
type Dataset struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Type    string `json:"type,omitempty"`
	Format  string `json:"format,omitempty"`

	// camelCase spellings (project endpoint).
	DataURICamel    string `json:"dataUri,omitempty"`
	BlobURICamel    string `json:"blobUri,omitempty"`
	ContentURICamel string `json:"contentUri,omitempty"`
	IsSingleFile    bool   `json:"isSingleFile,omitempty"`
	ConnectionName  string `json:"connectionName,omitempty"`

	// snake_case spellings.
	BlobURI    string `json:"blob_uri,omitempty"`
	DataURI    string `json:"data_uri,omitempty"`
	ContentURI string `json:"content_uri,omitempty"`
}

// ResolvedBlobURI returns the first URI the service supplied, across both
// spellings. An empty result means the dataset carries no downloadable URI and
// the caller must fetch a credential instead.
func (d *Dataset) ResolvedBlobURI() string {
	for _, candidate := range []string{
		d.BlobURI, d.BlobURICamel,
		d.DataURI, d.DataURICamel,
		d.ContentURI, d.ContentURICamel,
	} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
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
//  2. Parsable as a decimal number → increment by 1, format as "N.0"
//  3. Ends with trailing digits → increment the trailing numeric part
//  4. Otherwise → append ".1"
func NextVersion(current string) string {
	current = strings.TrimSpace(current)
	if current == "" {
		return "1.0"
	}

	// Try parsing as a decimal number (e.g. "1", "1.0", "2.0").
	if f, err := strconv.ParseFloat(current, 64); err == nil {
		return strconv.FormatFloat(math.Floor(f)+1, 'f', 1, 64)
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

// ReadFirstJSONLFile reads the rows to upload from a .jsonl file, or from the
// first .jsonl in a directory.
//
// A file path is read as itself. Resolving it to its directory and scanning
// would upload whichever .jsonl sorts first, so a project with one file per
// dataset would register the wrong rows under a name while recording the
// fingerprint of the declared file — the two would then agree forever.
//
// An empty file is refused here rather than uploaded: registering it succeeds,
// and the failure then surfaces at the run that tries to score it, which is a
// long way from the command that caused it.
func ReadFirstJSONLFile(path string) (string, error) {
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		data, err := os.ReadFile(path) //nolint:gosec // local artifact path
		if err != nil {
			return "", messages.ReadingPath(path, err)
		}
		return jsonlContent(filepath.Base(path), data)
	}

	dir := path
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", messages.ReadingDatasetDirectory(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".jsonl") {
			data, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // local artifact path
			if err != nil {
				return "", messages.ReadingPath(e.Name(), err)
			}
			return jsonlContent(e.Name(), data)
		}
	}
	return "", messages.NoJSONLInDirectory(dir)
}

// utf8BOM is what Windows editors and PowerShell's Set-Content write ahead of
// otherwise valid UTF-8.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// jsonlContent prepares one file's bytes for upload.
func jsonlContent(name string, data []byte) (string, error) {
	// Uploaded as-is a BOM becomes part of the first row's first key, so every
	// consumer of the dataset sees one malformed record.
	data = bytes.TrimPrefix(data, utf8BOM)
	if strings.TrimSpace(string(data)) == "" {
		return "", messages.DatasetFileHasNoRows(name)
	}
	if err := validateJSONLRows(name, data); err != nil {
		return "", err
	}
	return string(data), nil
}

// validateJSONLRows refuses a file the service would happily store.
//
// Upload does not parse the rows, so one malformed line registers a version
// that looks healthy and only fails in the run that reads it. The reconciler
// checks this too, but `dataset create` and `dataset update` do not go through
// it, so the check belongs on the path every upload shares.
func validateJSONLRows(name string, data []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// A row carrying a whole conversation runs well past the 64KB default.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(text), &row); err != nil {
			return messages.JSONLRowInvalid(name, line, err)
		}
		if len(row) == 0 {
			return messages.JSONLRowEmpty(name, line)
		}
	}
	return scanner.Err()
}
