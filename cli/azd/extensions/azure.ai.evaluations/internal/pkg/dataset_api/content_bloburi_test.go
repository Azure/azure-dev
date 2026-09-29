// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An uploaded dataset's download URI names the blob itself, not the container
// holding it. `ListDatasetContent` always sent `comp=list`, which storage
// answers 409 for a blob — so `dataset download` failed for every dataset this
// CLI had published, which is most of the ones anyone would try it on.
func TestListDatasetContentReadsABlobURIWithoutListing(t *testing.T) {
	server := &storageServer{
		uriPath: "/c/rows.jsonl",
		blobs:   map[string]string{"": `{"query":"direct"}`},
	}
	client, _ := server.start(t)

	content, err := client.ListDatasetContent(context.Background(), "ds", "1.0", testAPIVersion)
	require.NoError(t, err)
	assert.True(t, content.SingleFile)
	assert.Nil(t, server.gotListQuery, "a blob URI needs no container listing")

	body, err := client.Open(context.Background(), content, content.Files[0])
	require.NoError(t, err)
	defer body.Close()
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, `{"query":"direct"}`, string(got),
		"and reading it must not append an entry name to a path that is already the blob")
}

// A container-backed dataset still enumerates, so the fix does not trade one
// shape for the other.
func TestListDatasetContentStillListsAContainerURI(t *testing.T) {
	server := &storageServer{
		uriPath: "/generated-container",
		blobs: map[string]string{
			"_meta.json": `{"ignored":true}`,
			"data.jsonl": `{"query":"from the container"}`,
		},
	}
	client, _ := server.start(t)

	content, err := client.ListDatasetContent(context.Background(), "ds", "1.0", testAPIVersion)
	require.NoError(t, err)
	require.NotNil(t, server.gotListQuery, "a container URI has to be enumerated")
	assert.Len(t, content.Files, 2, "every entry is downloaded, not just the rows")
}
