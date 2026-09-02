// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A continuation link carries its credential in the query as `sig`, and a link
// that will not parse used to reach the user twice over: once echoed verbatim,
// and again inside url.Parse's own error, which embeds the URL it was given.
// Both are printed to the terminal and scroll back.
func TestAnUnparseableNextLinkDoesNotCarryItsCredential(t *testing.T) {
	client := NewDatasetClientFromPipeline(
		"https://example.invalid",
		runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	// A control character is what makes this unparseable; the SAS is what must
	// not survive being reported.
	const secret = "s0m3-l1v3-s1gnatur3"
	_, err := client.doRequestGetURL(t.Context(), "https://example.invalid/x\x7f?sig="+secret)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret,
		"a signature must not reach an error the user reads")
	assert.False(t, strings.Contains(err.Error(), "sig="),
		"nor the query it was carried in")
}

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

// The link that parses is the other half. A DNS, TLS or timeout failure never
// reaches the parse or log paths that were already redacted, so it was the one
// way a continuation credential still reached the terminal.
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
