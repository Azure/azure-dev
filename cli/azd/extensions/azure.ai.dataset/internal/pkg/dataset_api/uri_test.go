// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The service spells these fields inconsistently, and a URI read from the
// wrong spelling comes back empty rather than wrong — which is how the dataset
// URI went unbound the first time.
func TestDatasetResolvedBlobURI_AcceptsEitherSpelling(t *testing.T) {
	cases := map[string]string{
		`{"dataUri":"https://x/y.jsonl"}`:    "https://x/y.jsonl",
		`{"data_uri":"https://x/y.jsonl"}`:   "https://x/y.jsonl",
		`{"blobUri":"https://x/b.jsonl"}`:    "https://x/b.jsonl",
		`{"contentUri":"https://x/c.jsonl"}`: "https://x/c.jsonl",
	}
	for body, want := range cases {
		var ds Dataset
		require.NoError(t, json.Unmarshal([]byte(body), &ds), body)
		assert.Equal(t, want, ds.ResolvedBlobURI(), body)
	}

	var none Dataset
	require.NoError(t, json.Unmarshal([]byte(`{"name":"x"}`), &none))
	assert.Empty(t, none.ResolvedBlobURI(),
		"no URI means the caller has to fetch a credential, not that the dataset is unreadable")
}

// An upload needs the SAS-bearing URI to write to and the plain one to
// finalize with. Confusing them fails at different stages, so both are read
// from their own place.
func TestPendingUploadURIs(t *testing.T) {
	var p PendingUploadResponse
	require.NoError(t, json.Unmarshal([]byte(`{
      "blobReference": {
        "blobUri": "https://acct.blob.core.windows.net/container",
        "credential": { "sasUri": "https://acct.blob.core.windows.net/container?sig=abc" }
      }
    }`), &p))

	assert.Equal(t, "https://acct.blob.core.windows.net/container?sig=abc", p.ResolvedUploadURI(),
		"the upload target carries the SAS")
	assert.Equal(t, "https://acct.blob.core.windows.net/container", p.ResolvedBlobURI(),
		"the finalize URI does not")

	var empty PendingUploadResponse
	assert.Empty(t, empty.ResolvedUploadURI())
	assert.Empty(t, empty.ResolvedBlobURI())
}

// Credentials arrive in two shapes and the consumption one takes precedence,
// because that is the one scoped for reading.
func TestCredentialResolvedDownloadURI(t *testing.T) {
	var c DatasetCredential
	require.NoError(t, json.Unmarshal([]byte(`{
      "blobReferenceForConsumption": { "credential": { "sasUri": "https://acct/read?sig=r" } },
      "blobReference":               { "credential": { "sasUri": "https://acct/write?sig=w" } }
    }`), &c))
	assert.Equal(t, "https://acct/read?sig=r", c.ResolvedDownloadURI())

	var legacy DatasetCredential
	require.NoError(t, json.Unmarshal([]byte(`{"sas_uri":"https://acct/legacy?sig=l"}`), &legacy))
	assert.Equal(t, "https://acct/legacy?sig=l", legacy.ResolvedDownloadURI(),
		"the flat spelling is still honoured")

	var none DatasetCredential
	assert.Empty(t, none.ResolvedDownloadURI())
}
