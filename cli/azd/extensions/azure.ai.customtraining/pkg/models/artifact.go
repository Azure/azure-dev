// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// Artifact represents metadata for a single job artifact.
type Artifact struct {
	ArtifactID  string            `json:"artifactId,omitempty"`
	Origin      string            `json:"origin,omitempty"`
	Container   string            `json:"container,omitempty"`
	Path        string            `json:"path,omitempty"`
	ETag        string            `json:"etag,omitempty"`
	CreatedTime string            `json:"createdTime,omitempty"`
	DataPath    *string           `json:"dataPath,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
}

// ArtifactList represents a paginated list of artifacts.
type ArtifactList struct {
	Value             []Artifact `json:"value"`
	ContinuationToken *string   `json:"continuationToken,omitempty"`
	NextLink          *string   `json:"nextLink,omitempty"`
}

// ArtifactContentInfo represents content information for an artifact, including a SAS URI.
type ArtifactContentInfo struct {
	Origin        string            `json:"origin,omitempty"`
	Container     string            `json:"container,omitempty"`
	Path          string            `json:"path,omitempty"`
	ContentURI    string            `json:"contentUri,omitempty"`
	ContentLength int64             `json:"contentLength,omitempty"`
	Tags          map[string]string `json:"tags,omitempty"`
}

// ArtifactContentInfoList represents a paginated list of artifact content info with SAS URIs.
type ArtifactContentInfoList struct {
	Value             []ArtifactContentInfo `json:"value"`
	ContinuationToken *string              `json:"continuationToken,omitempty"`
	NextLink          *string              `json:"nextLink,omitempty"`
}
