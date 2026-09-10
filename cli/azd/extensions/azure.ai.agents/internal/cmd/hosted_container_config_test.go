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
		wantErr         bool
	}{
		{name: "source build", wantRemoteBuild: true},
		{name: "network injected source build", networkInjected: true},
		{name: "unqualified pre-built image", image: "agent:v1", wantErr: true},
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
			options, err := dockerProjectOptionsForHostedContainer(test.image, test.networkInjected)
			if test.wantErr {
				require.ErrorContains(t, err, "must be in format registry/image[:tag]")
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.wantPassthrough, options.GetImagePassthrough())
			require.Equal(t, test.wantRemoteBuild, options.GetRemoteBuild())
		})
	}
}

func TestDockerProjectMapForHostedContainer(t *testing.T) {
	t.Parallel()

	passthrough, err := dockerProjectMapForHostedContainer("registry.example.com/team/agent:v1", false)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"imagePassthrough": true}, passthrough)

	remoteBuild, err := dockerProjectMapForHostedContainer("", false)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"remoteBuild": true}, remoteBuild)

	localBuild, err := dockerProjectMapForHostedContainer("", true)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"remoteBuild": false}, localBuild)

	_, err = dockerProjectMapForHostedContainer("agent:v1", false)
	require.ErrorContains(t, err, "must be in format registry/image[:tag]")
}
