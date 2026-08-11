// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A separator inside a name must not let the request address a route other than
// the one the caller asked for.
//
// Worth pinning because the request builder round-trips the whole path through
// PathUnescape on its way out, and an escape undone there is an escape that
// never happened. It also documents that the traversal this appears to perform
// in an error message is a rendering artefact: azcore prints the DECODED path,
// so a 404 reads as `/datasets/ds-../../etc/passwd/versions` while the bytes on
// the wire are escaped and address the dataset collection correctly.
func TestNameSeparatorsStayEscapedOnTheWire(t *testing.T) {
	client, calls := recordingDatasetClient(t, 200, `{"value":[]}`)

	_, err := client.ListDatasetVersions(context.Background(), "ds-../../etc/passwd", "2025-01-01")
	require.NoError(t, err)
	require.Len(t, *calls, 1)

	got := (*calls)[0]

	assert.Equal(t, "/datasets/ds-..%2F..%2Fetc%2Fpasswd/versions", got.rawPath,
		"the separators inside the name stay escaped, so the name is one path segment")
}
