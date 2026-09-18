// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A dataset's URI points at either the blob or the container holding it,
// depending on how it was created, and nothing in the payload says which:
// isSingleFile is true either way. Uploaded datasets end in the file name;
// generated ones end in the container. Downloading a container returns 409.
func TestLooksLikeBlobURI(t *testing.T) {
	uploaded := "https://acct.blob.core.windows.net:443/container-guid/azd-smoke-golden.jsonl"
	generated := "https://acct.blob.core.windows.net/asayedahme-420d0b21-956c-513b-bb18-f60bfbf5e724"

	require.True(t, looksLikeBlobURI(uploaded), "an uploaded dataset names its file")
	require.False(t, looksLikeBlobURI(generated), "a generated dataset names its container")
}

// A SAS token on the URI must not change the answer.
func TestLooksLikeBlobURIIgnoresQuery(t *testing.T) {
	require.True(t, looksLikeBlobURI(
		"https://acct.blob.core.windows.net/c/data.jsonl?sv=2021&sig=abc"))
	require.False(t, looksLikeBlobURI(
		"https://acct.blob.core.windows.net/c?sv=2021&sig=abc"))
	require.False(t, looksLikeBlobURI("https://acct.blob.core.windows.net/c/"))
}

// An evaluation dataset is JSONL, so that is preferred when a container holds
// more than one file.
func TestPickDatasetBlobPrefersJSONL(t *testing.T) {
	require.Equal(t, "data.jsonl",
		pickDatasetBlob([]string{"_meta.json", "data.jsonl", "readme.txt"}))
	require.Equal(t, "data.JSONL",
		pickDatasetBlob([]string{"data.JSONL"}), "the extension match is case-insensitive")
}

// With nothing recognisable, any real file beats returning nothing.
func TestPickDatasetBlobFallsBackToAnyFile(t *testing.T) {
	require.Equal(t, "data.csv", pickDatasetBlob([]string{"data.csv"}))
	require.Empty(t, pickDatasetBlob([]string{"folder/"}))
	require.Empty(t, pickDatasetBlob(nil))
}
