// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// transportFailure is what http.Client hands back when a request never gets an
// answer: a *url.Error naming the URL it dialled, SAS query and all.
type transportFailure struct{}

func (transportFailure) Do(req *http.Request) (*http.Response, error) {
	return nil, &url.Error{
		Op:  "Get",
		URL: req.URL.String(),
		Err: errors.New("dial tcp: lookup acct.blob.core.windows.net: no such host"),
	}
}

// The parse and log paths were already redacted, so a DNS, TLS or timeout
// failure was the one way a continuation credential still reached the terminal.
func TestATransportFailureDoesNotCarryTheNextLinkCredential(t *testing.T) {
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{
			Transport: transportFailure{},
			Retry:     policy.RetryOptions{MaxRetries: -1},
		})
	client := NewDatasetClientFromPipeline("https://acct.blob.core.windows.net", pipeline)

	const secret = "s0m3-l1v3-s1gnatur3"
	_, err := client.doRequestGetURL(t.Context(),
		"https://acct.blob.core.windows.net/c/rows.jsonl?sv=2021-08-06&sig="+secret)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret,
		"a transport failure must not show the signature to the user")
	assert.NotContains(t, err.Error(), "sig=")
	assert.Contains(t, err.Error(), "acct.blob.core.windows.net",
		"the host stays, so the message still says where it failed")
}
