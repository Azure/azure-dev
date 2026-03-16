// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// PendingUploadRequest is the request body for startPendingUpload.
type PendingUploadRequest struct {
	PendingUploadType string `json:"pendingUploadType"` // "BlobReference"
}

// PendingUploadResponse is the API response from startPendingUpload.
type PendingUploadResponse struct {
	BlobReference     *BlobReference `json:"blobReference"`
	PendingUploadID   string         `json:"pendingUploadId"`
	PendingUploadType string         `json:"pendingUploadType"`
}

// BlobReference contains the blob storage details for upload.
type BlobReference struct {
	BlobURI    string        `json:"blobUri"`
	Credential SASCredential `json:"credential"`
}

// SASCredential contains the SAS credential for blob upload.
type SASCredential struct {
	SASUri         string `json:"sasUri"`
	CredentialType string `json:"credentialType"`
}

// DatasetVersion represents a dataset version resource.
type DatasetVersion struct {
	ID          string            `json:"id,omitempty"`
	Name        string            `json:"name,omitempty"`
	Version     string            `json:"version,omitempty"`
	DataURI     string            `json:"dataUri,omitempty"`
	DataType    string            `json:"dataType,omitempty"` // "uri_folder"
	Description string            `json:"description,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
}
