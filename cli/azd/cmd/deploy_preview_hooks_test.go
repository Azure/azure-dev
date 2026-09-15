// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	internalcmd "github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeploymentHooksDisabledForPreview(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		args    []string
		enabled bool
	}{
		{name: "normal deploy", enabled: true},
		{name: "preview", args: []string{"--preview"}},
		{name: "explicitly disabled preview", args: []string{"--preview=false"}, enabled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			command := internalcmd.NewDeployCmd()
			internalcmd.NewDeployFlags(command, &internal.GlobalCommandOptions{})
			require.NoError(t, command.ParseFlags(tt.args))
			descriptor := &actions.ActionDescriptor{Options: &actions.ActionDescriptorOptions{Command: command}}
			assert.Equal(t, tt.enabled, deploymentHooksEnabled(descriptor),
				"preview must skip constructing hooks middleware, including build-producing service imports")
		})
	}
}
