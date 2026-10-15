// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStripLeadingGlobalFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "no flags", args: []string{"connection", "add"}, want: []string{"connection", "add"}},
		{name: "bool flag", args: []string{"--no-prompt", "connection", "add"}, want: []string{"connection", "add"}},
		{
			name: "value flags",
			args: []string{"-C", "dir", "--environment", "dev", "-o", "json", "--debug", "connection", "add"},
			want: []string{"connection", "add"},
		},
		{
			name: "flags after command kept",
			args: []string{"--debug", "connection", "add", "--no-prompt", "-e", "dev"},
			want: []string{"connection", "add", "--no-prompt", "-e", "dev"},
		},
		{
			name: "unknown leading flag leaves args unchanged",
			args: []string{"--unknown", "connection"},
			want: []string{"--unknown", "connection"},
		},
		{name: "empty", args: []string{}, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, StripLeadingGlobalFlags(tt.args))
		})
	}
}

func TestNewGlobalFlagSetMatchesReservedFlags(t *testing.T) {
	flags := NewGlobalFlagSet()
	for _, name := range []string{"cwd", "debug", "no-prompt", "non-interactive", "environment"} {
		require.NotNil(t, flags.Lookup(name), name)
	}
}
