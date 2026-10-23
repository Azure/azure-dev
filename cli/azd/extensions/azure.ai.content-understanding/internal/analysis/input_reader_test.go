// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package analysis

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSingleFileDiscoveryAndReaderIdentity(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "invoice[1].pdf")
	require.NoError(t, os.WriteFile(path, []byte("original sample"), 0o600))
	input, err := DiscoverFile(t.Context(), path)
	require.NoError(t, err)
	assert.Equal(t, path, input.Path)
	assert.Equal(t, "invoice[1].pdf", input.Relative)
	reader, err := input.Open()
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, "original sample", string(body))
	require.NoError(t, reader.Close())
	for _, value := range []string{"", "https://example.com/sample.pdf", filepath.Dir(path), path + ".missing"} {
		_, err := DiscoverFile(t.Context(), value)
		assert.Error(t, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = DiscoverFile(ctx, path)
	assert.ErrorIs(t, err, context.Canceled)
	require.NoError(t, os.Rename(path, path+".original"))
	require.NoError(t, os.WriteFile(path, []byte("replacement"), 0o600))
	_, err = input.Open()
	assert.ErrorContains(t, err, "changed after discovery")
	_, err = (Input{Path: path}).Open()
	assert.ErrorContains(t, err, "changed after discovery")
	require.NoError(t, os.Remove(path))
	_, err = input.Open()
	assert.Error(t, err)
}
