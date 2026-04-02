// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package models defines the request/response types for the Azure AI Foundry dataset API.
//
// Uploading a local directory to Azure happens in 3 steps, each using different structs:
//
//  1. POST startPendingUpload → sends PendingUploadRequest, receives PendingUploadResponse
//     (This reserves a blob storage location and gives us a temporary SAS URL to upload to)
//
//  2. azcopy uploads files to the SAS URL (no struct needed — handled by azcopy binary)
//
//  3. PATCH createOrUpdate → sends DatasetVersion to confirm the upload and record metadata
//     (This tells the API "the files are uploaded, here's where they are and what they are")
package models

// --- Step 1: Start pending upload ---

// PendingUploadRequest is the request body for POST .../startPendingUpload.
// We always use "BlobReference" — it tells the API to give us a blob storage location.
type PendingUploadRequest struct {
	PendingUploadType string `json:"pendingUploadType"` // Always "BlobReference"
}

// PendingUploadResponse is what the API returns from startPendingUpload.
// The important part is BlobReference — it gives us the storage URLs we need.
type PendingUploadResponse struct {
	BlobReference     *BlobReference `json:"blobReference"`     // Contains BlobURI (permanent) + SASUri (temporary) — the two URLs we actually need
	PendingUploadID   string         `json:"pendingUploadId"`   // Server-assigned tracking ID (we don't use this)
	PendingUploadType string         `json:"pendingUploadType"` // Echoes back "BlobReference" (we don't use this)
}

// BlobReference contains the storage location (BlobURI) and a temporary credential (SASUri).
//
// Example BlobURI:  "https://storage.blob.core.windows.net/container/datasets/code-myproject/v1"
// Example SASUri:   "https://storage.blob.core.windows.net/container/datasets/code-myproject/v1?sv=...&sig=..."
//
// BlobURI is the same URL without the ?token part. SASUri has the token appended.
type BlobReference struct {
	BlobURI    string        `json:"blobUri"`    // Permanent address — saved in PATCH
	Credential SASCredential `json:"credential"` // Temporary credential — used by azcopy
}

// SASCredential holds the temporary SAS (Shared Access Signature) URL.
// This URL expires after a short time. Azcopy uses it to authenticate the upload.
type SASCredential struct {
	SASUri         string `json:"sasUri"`         // Temporary URL with embedded access token
	CredentialType string `json:"credentialType"` // Usually "SAS"
}

// --- Step 3: Confirm upload (PATCH) and read back (GET) ---

// DatasetVersion represents a dataset version resource in the Azure AI Foundry API.
// Used for both:
//   - PATCH request body: we send dataUri, dataType, description, tags
//   - GET/PATCH response: the API returns all fields including id, name, version
//
// Example PATCH request body:
//
//	{
//	  "dataUri": "https://storage.blob.core.windows.net/.../code-myproject/v1",
//	  "dataType": "uri_folder",
//	  "description": "Code for project myproject",
//	  "tags": { "contentHash": "a1b2c3d4...full 64 char hash..." }
//	}
//
// Example GET response:
//
//	{
//	  "id": "/subscriptions/.../datasets/code-myproject/versions/a1b2c3...",
//	  "name": "code-myproject",
//	  "version": "a1b2c3d4e5f6...",
//	  "dataUri": "https://storage.blob.core.windows.net/...",
//	  "dataType": "uri_folder",
//	  "tags": { "contentHash": "a1b2c3d4...full 64 char hash..." }
//	}
type DatasetVersion struct {
	ID          string            `json:"id,omitempty"`          // Full ARM resource ID (set by API, not by us)
	Name        string            `json:"name,omitempty"`        // Dataset name, e.g. "code-myproject"
	Version     string            `json:"version,omitempty"`     // Version string, e.g. truncated hash "a1b2c3..."
	DataURI     string            `json:"dataUri,omitempty"`     // Permanent blob URI where files are stored
	DataType    string            `json:"type,omitempty"`        // Always "uri_folder" (a directory of files)
	Description string            `json:"description,omitempty"` // Human-readable description
	Tags        map[string]string `json:"tags,omitempty"`        // Key-value tags; we use "contentHash" for dedup sentinel
}
