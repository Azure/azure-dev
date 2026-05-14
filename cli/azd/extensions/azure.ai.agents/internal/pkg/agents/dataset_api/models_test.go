// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Dataset
// ---------------------------------------------------------------------------

func TestDataset_ResolvedBlobURI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		dataset  Dataset
		expected string
	}{
		{
			name:     "prefers blob_uri",
			dataset:  Dataset{BlobURI: "https://blob.example", DataURI: "https://data.example"},
			expected: "https://blob.example",
		},
		{
			name:     "falls back to data_uri",
			dataset:  Dataset{DataURI: "https://data.example", ContentURI: "https://content.example"},
			expected: "https://data.example",
		},
		{
			name:     "falls back to content_uri",
			dataset:  Dataset{ContentURI: "https://content.example"},
			expected: "https://content.example",
		},
		{
			name:     "empty when no URI",
			dataset:  Dataset{Name: "test"},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, tc.dataset.ResolvedBlobURI())
		})
	}
}

// ---------------------------------------------------------------------------
// DatasetCredential
// ---------------------------------------------------------------------------

func TestDatasetCredential_ResolvedDownloadURI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		cred     DatasetCredential
		expected string
	}{
		{
			name:     "prefers sas_uri",
			cred:     DatasetCredential{SASUri: "https://blob.example/data?sig=abc", BlobURI: "https://blob.example/data"},
			expected: "https://blob.example/data?sig=abc",
		},
		{
			name:     "combines blob_uri and sas",
			cred:     DatasetCredential{BlobURI: "https://blob.example/data", SAS: "sig=abc&se=2025"},
			expected: "https://blob.example/data?sig=abc&se=2025",
		},
		{
			name:     "blob_uri only",
			cred:     DatasetCredential{BlobURI: "https://blob.example/data"},
			expected: "https://blob.example/data",
		},
		{
			name:     "empty when no fields",
			cred:     DatasetCredential{},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, tc.cred.ResolvedDownloadURI())
		})
	}
}
