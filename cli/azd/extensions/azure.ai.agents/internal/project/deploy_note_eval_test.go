// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `azd ai agent eval generate` is deprecated -- the evaluations extension owns
// that surface now -- and every agent deploy was still sending people to it.
// Following the printed suggestion lands on a command that tells you it is
// deprecated, which is a worse first impression than saying nothing.
func TestDeployNoteNamesTheSupportedEvalCommand(t *testing.T) {
	t.Parallel()

	p := &AgentServiceTargetProvider{}
	artifacts := p.deployArtifacts(
		"agent", "1.0.0",
		"", "https://ep.services.ai.azure.com",
		ActivityProfile{},
		[]agent_yaml.ProtocolVersionRecord{{Protocol: "responses"}},
	)
	require.NotEmpty(t, artifacts)
	note := artifacts[len(artifacts)-1].Metadata["note"]

	assert.NotContains(t, note, "azd ai agent eval",
		"that surface is deprecated and must not be advertised")
	assert.Contains(t, note, "azd ai eval init",
		"the evaluations extension owns evaluation setup")
	assert.Contains(t, note, "aka.ms/azd-agents-invoke",
		"the invocation link is unchanged")
}
