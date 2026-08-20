// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDockerProjectOptionsForHostedContainer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		image           string
		networkInjected bool
		wantPassthrough bool
		wantRemoteBuild bool
	}{
		{name: "source build", wantRemoteBuild: true},
		{name: "network injected source build", networkInjected: true},
		{name: "pre-built image", image: "registry.example.com/team/agent:v1", wantPassthrough: true},
		{
			name:            "network injected pre-built image",
			image:           "registry.example.com/team/agent:v1",
			networkInjected: true,
			wantPassthrough: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := dockerProjectOptionsForHostedContainer(test.image, test.networkInjected)
			require.Equal(t, test.wantPassthrough, options.GetImagePassthrough())
			require.Equal(t, test.wantRemoteBuild, options.GetRemoteBuild())
		})
	}
}

func TestDockerProjectMapForHostedContainer(t *testing.T) {
	t.Parallel()

	require.Equal(t, map[string]any{"imagePassthrough": true},
		dockerProjectMapForHostedContainer("registry.example.com/team/agent:v1", false))
	require.Equal(t, map[string]any{"remoteBuild": true},
		dockerProjectMapForHostedContainer("", false))
	require.Equal(t, map[string]any{"remoteBuild": false},
		dockerProjectMapForHostedContainer("", true))
}
