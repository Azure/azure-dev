// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generate used to open on a prompt. The reader supplied answers without ever
// seeing which agent, model, project and file the command had already settled
// on -- and the project is where the money goes.
func TestGenerateContextNamesWhatWasDetected(t *testing.T) {
	var out bytes.Buffer
	writeGenerateContext(&out, generateContext{
		agent:        "support-agent",
		model:        "gpt-4o-mini",
		projectName:  "contoso-project",
		configPath:   "evals/azure.eval.yaml",
		configExists: true,
		evals:        2,
	})

	text := out.String()
	for _, want := range []string{
		"Using local configuration:",
		"support-agent", "gpt-4o-mini", "contoso-project",
		"evals/azure.eval.yaml", "existing, 2 evals",
	} {
		assert.Contains(t, text, want, text)
	}
}

// Nothing in the block was validated remotely, so a value that is simply
// absent says so rather than leaving a blank a reader would fill in themselves.
func TestGenerateContextSaysWhenSomethingIsMissing(t *testing.T) {
	var out bytes.Buffer
	writeGenerateContext(&out, generateContext{configPath: "evals/azure.eval.yaml"})

	assert.Contains(t, out.String(), "not configured", out.String())
	assert.Contains(t, out.String(), "(new)",
		"a configuration that is not there yet is being created, not added to")
}

// The project the run is billed to is the one every call goes to, so it is read
// off the endpoint rather than asked for again.
func TestProjectNameComesOffTheEndpoint(t *testing.T) {
	assert.Equal(t, "contoso-project", projectNameOf(
		"https://acct.services.ai.azure.com/api/projects/contoso-project"))
	assert.Equal(t, "contoso-project", projectNameOf(
		"https://acct.services.ai.azure.com/api/projects/contoso-project/"))
}

// A URL that is not a project endpoint has no project name in it, and inventing
// one would put a name beside "Project:" that the reader could not act on.
func TestProjectNameIsEmptyWhenTheEndpointIsNotAProject(t *testing.T) {
	for _, endpoint := range []string{
		"",
		"https://acct.services.ai.azure.com",
		"https://acct.services.ai.azure.com/api/projects",
		"https://acct.services.ai.azure.com/api/accounts/contoso",
		"://not a url",
	} {
		assert.Emptyf(t, projectNameOf(endpoint), "%q names no project", endpoint)
	}
}

// The flag answers the question, so it is not asked again.
func TestGenerationLevelTakesTheFlag(t *testing.T) {
	level, err := resolveGenerationLevel(noPromptCmd(t, false), "Conversation")
	require.NoError(t, err)
	assert.Equal(t, "conversation", level,
		"the level is a value in a file, so its case is settled here")
}

// A level that is neither is refused by name, with both choices, rather than
// silently becoming the default and generating the wrong shape.
func TestGenerationLevelRefusesWhatIsNotALevel(t *testing.T) {
	_, err := resolveGenerationLevel(noPromptCmd(t, false), "session")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "session")
	assert.Contains(t, err.Error(), "turn")
	assert.Contains(t, err.Error(), "conversation")
}

// Nobody is there to answer under --no-prompt, and turn is the documented
// default, so it is what a script that says nothing gets.
func TestGenerationLevelDefaultsWithoutAPrompt(t *testing.T) {
	level, err := resolveGenerationLevel(noPromptCmd(t, true), "")

	require.NoError(t, err)
	assert.Equal(t, "turn", level)
}

// The name is what makes the two levels distinguishable on disk.
func TestDatasetNameSuffixFollowsTheLevel(t *testing.T) {
	assert.Equal(t, "turn-tests", datasetNameSuffix("turn"))
	assert.Equal(t, "conversation-tests", datasetNameSuffix("conversation"))
	assert.Equal(t, "turn-tests", datasetNameSuffix(""),
		"an unsettled level is turn, the same default the prompt starts on")
}
