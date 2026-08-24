// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvokeCommandBackgroundFlagRegistered(t *testing.T) {
	t.Parallel()

	flag := newInvokeCommand(nil).Flags().Lookup("background")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestInvokeCommandBackgroundValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "rejects local",
			args: []string{"--background", "--local", "hello"},
			want: "supported only for remote Responses agents",
		},
		{
			name: "rejects explicit invocations protocol",
			args: []string{"--background", "--protocol", "invocations", "hello"},
			want: "--background is not supported with the invocations protocol",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := newInvokeCommand(nil)
			cmd.SetArgs(tt.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			err := cmd.Execute()
			require.Error(t, err)
			assert.True(t, strings.Contains(err.Error(), tt.want), "error %q should contain %q", err, tt.want)
		})
	}
}
