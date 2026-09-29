// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sasSecret is the signature a storage SAS carries in its query string.
const constructionSASSecret = "SIGNATUREVALUETHATMUSTNOTAPPEAR"

// A URL the parser refuses, still carrying a SAS. Building a request from it
// fails before any transport error can happen, which is the path the redaction
// work missed: Do's failures were wrapped and NewRequest's were not.
func malformedSASURL() string {
	return "https://acct.blob.core.windows.net/c/d.jsonl\x7f?sig=" + constructionSASSecret
}

func constructionClient(t *testing.T) *DatasetClient {
	t.Helper()
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return NewDatasetClientFromPipeline("https://example.invalid", pipeline)
}

// The premise: the raw error names the URL, so returning it unwrapped hands the
// signature to whoever reads the message or the debug log.
func TestRequestConstructionErrorsDoNotCarryTheSAS(t *testing.T) {
	raw := malformedSASURL()
	require.Contains(t, raw, constructionSASSecret, "the fixture has to carry a secret to leak")

	client := constructionClient(t)
	ctx := context.Background()

	cases := []struct {
		name string
		call func() error
	}{
		{"DownloadDataset", func() error { _, err := client.DownloadDataset(ctx, raw); return err }},
		{"DownloadBlob", func() error { _, err := client.DownloadBlob(ctx, raw, "d.jsonl"); return err }},
		{"UploadBlob", func() error { return client.UploadBlob(ctx, raw, "d.jsonl", []byte("{}")) }},
		{"ListContainerBlobs", func() error { _, err := client.ListContainerBlobs(ctx, raw); return err }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()

			require.Error(t, err, "a URL the parser refuses has to fail")
			assert.NotContains(t, err.Error(), constructionSASSecret,
				"the signature reached the caller through %s", tc.name)
			assert.NotContains(t, strings.ToLower(err.Error()), "sig=",
				"even the parameter name should not survive")
		})
	}
}
