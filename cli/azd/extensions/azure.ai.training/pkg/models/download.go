// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// ModelVersion is the response shape for GET .../models/{name}/versions/{version}.
// Per the download flow, the BlobURI lives at the root, not nested.
type ModelVersion struct {
	BlobURI   string `json:"blobUri,omitempty"`
	AssetID   string `json:"assetId,omitempty"`
	Name      string `json:"name,omitempty"`
	Version   string `json:"version,omitempty"`
	ModelType string `json:"modelType,omitempty"`
}

// ModelCredentialsRequest is the body for POST .../models/{name}/versions/{v}/credentials.
type ModelCredentialsRequest struct {
	BlobURI                  string `json:"BlobUri"`
	GenerateBlobLevelReadSas bool   `json:"GenerateBlobLevelReadSas"`
}

// CredentialsResponse is the response shape for both model and dataset credentials calls.
// The SAS URI lives at blobReference.credential.sasUri.
type CredentialsResponse struct {
	BlobReference *BlobReference `json:"blobReference,omitempty"`
}

// RunArtifact is a single artifact entry from the AML history artifacts list.
type RunArtifact struct {
	ArtifactID  string `json:"artifactId,omitempty"`
	Origin      string `json:"origin,omitempty"`
	Container   string `json:"container,omitempty"`
	Path        string `json:"path,omitempty"`
	ETag        string `json:"etag,omitempty"`
	CreatedTime string `json:"createdTime,omitempty"`
}

// RunArtifactList is the response shape for the AML artifacts list endpoint.
type RunArtifactList struct {
	Value             []RunArtifact `json:"value"`
	ContinuationToken string        `json:"continuationToken,omitempty"`
}

// RunArtifactContentInfo is the response shape for the AML contentinfo endpoint.
// ContentURI is a SAS URI that can be downloaded directly with no auth header.
type RunArtifactContentInfo struct {
	ArtifactID string `json:"artifactId,omitempty"`
	Origin     string `json:"origin,omitempty"`
	Container  string `json:"container,omitempty"`
	Path       string `json:"path,omitempty"`
	ContentURI string `json:"contentUri,omitempty"`
}
