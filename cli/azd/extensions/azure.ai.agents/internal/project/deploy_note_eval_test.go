// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noteFromDeploy renders the deploy note the way a caller sees it.
func noteFromDeploy(t *testing.T, hasEvalService bool) string {
	t.Helper()

	p := &AgentServiceTargetProvider{}
	artifacts := p.deployArtifacts(
		"agent", "1.0.0",
		"", "https://ep.services.ai.azure.com",
		ActivityProfile{},
		[]agent_yaml.ProtocolVersionRecord{{Protocol: "responses"}},
		hasEvalService,
	)
	require.NotEmpty(t, artifacts)
	return artifacts[len(artifacts)-1].Metadata["note"]
}

// `azd ai agent eval generate` is deprecated -- the evaluations extension owns
// that surface now -- and every agent deploy was still sending people to it.
// Following the printed suggestion lands on a command that tells you it is
// deprecated, which is a worse first impression than saying nothing.
func TestDeployNoteDoesNotSuggestTheDeprecatedEvalSurface(t *testing.T) {
	note := noteFromDeploy(t, false)

	assert.NotContains(t, note, "azd ai agent eval",
		"that surface is deprecated and must not be advertised")
	assert.Contains(t, note, "azd ai eval init",
		"the evaluations extension owns evaluation setup")
}

// A project that already declares an eval service is already set up. Telling
// its author to set it up reads as though the deploy did not notice what is in
// their azure.yaml.
func TestDeployNoteIsQuietWhenEvaluationIsAlreadyWiredUp(t *testing.T) {
	note := noteFromDeploy(t, true)

	assert.NotContains(t, strings.ToLower(note), "evaluation suite",
		"there is nothing to set up")
	assert.Contains(t, note, "aka.ms/azd-agents-invoke",
		"the invocation link is not conditional on any of this")
}
