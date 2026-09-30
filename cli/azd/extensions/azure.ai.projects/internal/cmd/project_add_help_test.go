// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjectAddInfraExampleParsing(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		wantArgs []string
	}{
		{name: "explicit value", args: []string{"--infra=bicep"}},
		{name: "optional value", args: []string{"--infra"}},
		{name: "separate value is positional", args: []string{"--infra", "bicep"}, wantArgs: []string{"bicep"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newProjectAddCommand(nil)
			require.NoError(t, cmd.ParseFlags(tt.args))
			value, err := cmd.Flags().GetString("infra")
			require.NoError(t, err)
			require.Equal(t, "bicep", value)
			require.ElementsMatch(t, tt.wantArgs, cmd.Flags().Args())
			if len(tt.wantArgs) > 0 {
				require.Error(t, cmd.ValidateArgs(cmd.Flags().Args()))
			} else {
				require.NoError(t, cmd.ValidateArgs(cmd.Flags().Args()))
			}
		})
	}

	cmd := newProjectAddCommand(nil)
	for line := range strings.SplitSeq(cmd.Example, "\n") {
		if strings.Contains(line, "--infra") {
			args := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "azd ai project add "))
			require.NoError(t, cmd.ParseFlags(args))
			require.NoError(t, cmd.ValidateArgs(cmd.Flags().Args()))
		}
	}
}
