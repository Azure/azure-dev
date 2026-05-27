// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRootCommand_HasAgentSubcommand(t *testing.T) {
	cmd := NewRootCommand()
	var names []string
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}
	assert.Contains(t, names, "agent",
		"azd ai doc must expose the agent subgroup")
	assert.Contains(t, names, "connection",
		"azd ai doc must expose the connection subgroup")
	assert.Contains(t, names, "toolbox",
		"azd ai doc must expose the toolbox subgroup")
	assert.Contains(t, names, "skill",
		"azd ai doc must expose the skill subgroup (Foundry skill resource docs)")
	assert.Contains(t, names, "install",
		"azd ai doc must expose the install subgroup (embedded skill-pack installer)")
}

func TestNewRootCommand_RunsIndexAsDefault(t *testing.T) {
	cmd := NewRootCommand()
	require.NotNil(t, cmd.RunE,
		"azd ai doc with no subcommand should print the index via RunE")
}

func TestDocCategories_HasAgentEntry(t *testing.T) {
	// Pin the wire contract: as new ai.* extensions adopt topic groups in
	// this extension, add them to docCategories. The Name MUST match the
	// directory name under internal/cmd/skills/<name>/.
	require.NotEmpty(t, docCategories,
		"docCategories must contain at least the agent entry")
	assert.Equal(t, "agent", docCategories[0].Name,
		"agent entry must be present")
}

func TestSkillsFS_HasAllAgentTopics(t *testing.T) {
	// Pins the topic set so a future drop or rename is a deliberate test
	// update -- topic names are the wire contract callers rely on.
	topics, err := loadCategoryTopics("agent")
	require.NoError(t, err)
	var got []string
	for _, top := range topics {
		got = append(got, top.Name)
	}
	assert.ElementsMatch(t, []string{
		"samples",
		"initialize",
		"develop",
		"configure",
		"extend",
		"deploy",
		"evaluate",
		"operate",
		"investigate",
	}, got)
}

func TestSkillsFS_HasAllSkillTopics(t *testing.T) {
	// Pins the topic set for the Foundry skill resource docs (the
	// azure.ai.skills extension). A future drop or rename is a
	// deliberate test update -- topic names are the wire contract
	// callers rely on (`azd ai doc skill <topic>`).
	topics, err := loadCategoryTopics("skill")
	require.NoError(t, err)
	var got []string
	for _, top := range topics {
		got = append(got, top.Name)
	}
	assert.ElementsMatch(t, []string{
		"overview",
		"manage",
		"share",
		"consume",
	}, got)
}

func TestPrintCategoryTopic_KnownTopicEmitsBody(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, printCategoryTopic(&buf, "agent", "initialize"))
	out := buf.String()
	// The initialize topic uses a specific H1 we can pin without coupling
	// to the entire body text.
	assert.True(t, strings.Contains(out, "# Initialize:"),
		"initialize topic body missing expected H1: first 120 chars = %q",
		out[:min(120, len(out))])
}

func TestPrintCategoryTopic_UnknownTopicReturnsHelpfulError(t *testing.T) {
	var buf bytes.Buffer
	err := printCategoryTopic(&buf, "agent", "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent",
		"error should name the bad topic so the agent can self-correct")
	assert.Contains(t, err.Error(), "Valid topics",
		"error should list the valid topic names")
}

func TestPrintCategoryTopic_TrailingNewline(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, printCategoryTopic(&buf, "agent", "configure"))
	out := buf.String()
	require.NotEmpty(t, out)
	assert.Equal(t, byte('\n'), out[len(out)-1],
		"topic output should end with a newline so terminal prompts return cleanly")
}

func TestNewAgentCommand_AcceptsZeroOrOneArg(t *testing.T) {
	cmd := newAgentCommand()
	require.NotNil(t, cmd.Args)
	assert.NoError(t, cmd.Args(cmd, []string{}))
	assert.NoError(t, cmd.Args(cmd, []string{"initialize"}))
	assert.Error(t, cmd.Args(cmd, []string{"initialize", "extra"}))
}
