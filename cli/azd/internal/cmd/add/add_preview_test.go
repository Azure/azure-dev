// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func TestMetadata_HostPrefix(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type: project.ResourceTypeHostContainerApp,
		Name: "api",
	}
	md := Metadata(r)
	assert.Equal(t, "Microsoft.App/containerApps", md.ResourceType)
	// Host resources use uppercase name as their prefix.
	for _, v := range md.Variables {
		assert.Contains(t, v, "API_")
	}
}

func TestMetadata_ExistingAppendsName(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type:     project.ResourceTypeDbRedis,
		Name:     "mycache",
		Existing: true,
	}
	md := Metadata(r)
	assert.Equal(t, "Microsoft.Cache/redis", md.ResourceType)
	// The existing suffix encodes the resource name.
	for _, v := range md.Variables {
		assert.Contains(t, v, "MYCACHE")
	}
}

func TestMetadata_UnknownResourceReturnsEmpty(t *testing.T) {
	t.Parallel()
	r := &project.ResourceConfig{
		Type: project.ResourceType("unknown.type"),
		Name: "thing",
	}
	md := Metadata(r)
	assert.Equal(t, metaDisplay{}, md)
}

func TestPreviewWriter_BoldAndGreenControlChars(t *testing.T) {
	t.Parallel()
	// Using "b" (bold) and "g" (green) control chars — these are stripped
	// from the output and replaced with a space.
	tests := []struct {
		name string
		in   string
	}{
		{"bold", "b  bold line\n"},
		{"green", "g  green line\n"},
		{"minus", "-  removed\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf testWriter
			pw := &previewWriter{w: &buf}
			_, err := pw.Write([]byte(tt.in))
			require.NoError(t, err)
			// Output should contain some of the visible text.
			out := buf.String()
			assert.NotEmpty(t, out, "input: %q", tt.in)
		})
	}
}

func (w *testWriter) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}

func TestPreviewWriter_PlusAndSpace(t *testing.T) {
	t.Parallel()
	var buf testWriter
	pw := &previewWriter{w: &buf}
	_, err := pw.Write([]byte("+  added line\n"))
	require.NoError(t, err)
	_, err = pw.Write([]byte("   unchanged line\n"))
	require.NoError(t, err)
	assert.NotEmpty(t, buf.String())
}
