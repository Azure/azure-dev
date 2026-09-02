// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"strings"
	"testing"

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
