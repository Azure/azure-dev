// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// samplesDir is the authored-examples tree at the extension root.
const samplesDir = "../../../../samples"

// TestSamples_Parse keeps the hand-authored samples honest.
//
// The samples exist to be copied, so a key that silently fails to bind is worse
// than a broken build: it teaches the wrong schema. Decoding with KnownFields
// turns any typo, stale key, or invented field in samples/**/agent.yaml into a
// test failure here rather than into a deployed agent that quietly ignores it.
func TestSamples_Parse(t *testing.T) {
	t.Parallel()

	manifests := findSampleManifests(t)
	require.NotEmpty(t, manifests, "no sample agent.yaml files found under %s", samplesDir)

	for _, manifest := range manifests {
		name, err := filepath.Rel(samplesDir, manifest)
		require.NoError(t, err)

		t.Run(filepath.ToSlash(name), func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(manifest)
			require.NoError(t, err)

			decoder := yaml.NewDecoder(strings.NewReader(string(content)))
			decoder.KnownFields(true)

			var agent PromptAgent
			require.NoError(t, decoder.Decode(&agent), "sample declares a key PromptAgent does not bind")

			require.Equal(t, AgentKindPrompt, agent.Kind, "samples are all prompt agents")
			require.NotEmpty(t, agent.Name)
			require.NotEmpty(t, agent.Model)

			// Instructions are declared inline, matching the prompt-agent API
			// schema. A sample that drops them would teach a shape the service
			// rejects.
			require.NotEmpty(t, agent.Instructions, "samples declare instructions inline")

			assertSampleMemoryIsDeployable(t, agent.Memory)
		})
	}
}

// TestSamples_BuildAPIRequest runs the samples through the same mapping the
// deploy path uses, so a sample cannot pass parsing yet fail at deploy time.
func TestSamples_BuildAPIRequest(t *testing.T) {
	t.Parallel()

	for _, manifest := range findSampleManifests(t) {
		name, err := filepath.Rel(samplesDir, manifest)
		require.NoError(t, err)

		t.Run(filepath.ToSlash(name), func(t *testing.T) {
			t.Parallel()

			content, err := os.ReadFile(manifest)
			require.NoError(t, err)

			var agent PromptAgent
			require.NoError(t, yaml.Unmarshal(content, &agent))

			request, err := CreatePromptAgentAPIRequest(agent, nil)
			require.NoError(t, err)
			require.Equal(t, agent.Name, request.Name)
		})
	}
}

// assertSampleMemoryIsDeployable mirrors the memory validation the deploy graph
// performs, which lives in the project package and so cannot be called here.
func assertSampleMemoryIsDeployable(t *testing.T, memory *PromptMemory) {
	t.Helper()

	if memory == nil {
		return
	}

	require.NotEmpty(t, memory.Store, "memory requires a store name")
	require.NotEmpty(t, memory.ChatModel, "memory requires a chat model")
	require.NotEmpty(t, memory.EmbeddingModel, "memory requires an embedding model")
}

func findSampleManifests(t *testing.T) []string {
	t.Helper()

	var manifests []string
	err := filepath.WalkDir(samplesDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == "agent.yaml" {
			manifests = append(manifests, path)
		}
		return nil
	})
	require.NoError(t, err)

	return manifests
}
