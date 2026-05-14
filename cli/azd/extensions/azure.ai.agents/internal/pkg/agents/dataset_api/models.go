// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

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
type DatasetCredential struct {
	BlobURI string `json:"blob_uri,omitempty"`
	SAS     string `json:"sas,omitempty"`
	// SASUri is the full URI with SAS token appended, ready for download.
	SASUri string `json:"sas_uri,omitempty"`
}

// ResolvedDownloadURI returns the URL to download the dataset.
// Prefers sas_uri (complete), falls back to blob_uri + sas query string.
func (c *DatasetCredential) ResolvedDownloadURI() string {
	if c.SASUri != "" {
		return c.SASUri
	}
	if c.BlobURI != "" && c.SAS != "" {
		return c.BlobURI + "?" + c.SAS
	}
	return c.BlobURI
}
