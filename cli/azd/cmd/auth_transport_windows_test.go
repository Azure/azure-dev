// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizePipePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "short form opaque", in: "npipe:azd-auth-foo", want: `\\.\pipe\azd-auth-foo`},
		{name: "long form", in: "npipe:////./pipe/azd-auth-foo", want: `\\.\pipe\azd-auth-foo`},
		{name: "missing name", in: "npipe:", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizePipePath(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNewSocketTransport_NotSupportedOnWindows(t *testing.T) {
	t.Parallel()
	_, _, err := newSocketTransport("unix:/tmp/x.sock")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported on this platform")
}
