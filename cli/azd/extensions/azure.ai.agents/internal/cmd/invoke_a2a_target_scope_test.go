// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestA2ATargetDoesNotApplyUnrelatedLocalDefinition(t *testing.T) {
	for _, tt := range []struct {
		name     string
		endpoint bool
		version  string
		deployed string
	}{
		{name: "explicit endpoint ignores project definition", endpoint: true},
		{name: "historical agent version", version: "1", deployed: "2"},
		{name: "unknown deployed agent version", version: "1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// This server does not implement project lookup: attempting to load a
			// definition would fail, so success proves we skipped unrelated YAML.
			client := newInvokeTestAzdClient(t, newInvokeUserConfigServer())
			action := &InvokeAction{flags: &invokeFlags{a2aVersion: "1.0"}}
			if tt.endpoint {
				action.endpoint = &parsedAgentEndpoint{}
			}
			err := action.resolveA2ATarget(t.Context(), &remoteContext{
				serviceName: "native-agent", azdClient: client, version: tt.version, deployedVersion: tt.deployed,
			})
			require.NoError(t, err)
			assert.Empty(t, action.resolvedA2AVersion)
			assert.Equal(t, "1.0", action.a2aProtocolVersion())
		})
	}
}
