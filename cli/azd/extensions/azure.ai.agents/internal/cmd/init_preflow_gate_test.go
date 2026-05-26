// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validManifestYAML mirrors the fixture in TestDetectLocalManifest so the
// gate code path through detectLocalManifest is exercised end-to-end with
// real LoadAndValidateAgentManifest validation rather than a stub.
const validManifestYAML = `name: test-agent
template:
  kind: hosted
  name: test-agent
  protocols:
    - protocol: responses
      version: v1
`

func TestShouldRunPreflow_SkipsWhenNoPrompt(t *testing.T) {
	flags := &initFlags{noPrompt: true}
	run, err := shouldRunPreflow(flags, t.TempDir())
	require.NoError(t, err)
	assert.False(t, run)
}

func TestShouldRunPreflow_SkipsWhenManifestPointerSet(t *testing.T) {
	flags := &initFlags{manifestPointer: "https://example.com/agent.yaml"}
	run, err := shouldRunPreflow(flags, t.TempDir())
	require.NoError(t, err)
	assert.False(t, run, "preflow must be skipped when -m / --manifest provided")
}

func TestShouldRunPreflow_SkipsWhenFromCode(t *testing.T) {
	flags := &initFlags{fromCode: true}
	run, err := shouldRunPreflow(flags, t.TempDir())
	require.NoError(t, err)
	assert.False(t, run, "preflow must be skipped when --from-code provided")
}

func TestShouldRunPreflow_SkipsWhenSrcSet(t *testing.T) {
	// --src signals "I have decided where the source lives"; the
	// pre-flow would render the wrong project path (it uses cwd) and
	// silently ignore the user's choice. Skipping is the correct
	// behavior. This test pins the contract.
	dir := t.TempDir()
	flags := &initFlags{src: dir}
	run, err := shouldRunPreflow(flags, dir)
	require.NoError(t, err)
	assert.False(t, run, "preflow must be skipped when --src provided")
}

// TestShouldRunPreflow_SkipsWhenDownstreamConfigFlagsSet covers every
// "user is scripting this" flag in one place so any new init flag added
// in the future trips this test if the gate is not updated.
func TestShouldRunPreflow_SkipsWhenDownstreamConfigFlagsSet(t *testing.T) {
	cases := map[string]*initFlags{
		"projectResourceId": {projectResourceId: "/subscriptions/x/resourceGroups/y/providers/Microsoft.CognitiveServices/accounts/z"},
		"modelDeployment":   {modelDeployment: "gpt-4o"},
		"model":             {model: "gpt-4o"},
		"agentName":         {agentName: "my-agent"},
		"protocols":         {protocols: []string{"a2a"}},
		"deployMode":        {deployMode: "container"},
		"runtime":           {runtime: "python_3_13"},
		"entryPoint":        {entryPoint: "app.py"},
		"depResolution":     {depResolution: "remote_build"},
	}
	for name, flags := range cases {
		t.Run(name, func(t *testing.T) {
			run, err := shouldRunPreflow(flags, t.TempDir())
			require.NoError(t, err)
			assert.False(t, run, "preflow must be skipped when %s is set", name)
		})
	}
}

func TestShouldRunPreflow_SkipsWhenAgentYamlExistsInSrc(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "agent.yaml"),
		[]byte("name: existing-agent\n"),
		0o600,
	))
	// flags.src is empty so the gate inspects checkDir="." -- run
	// from the temp dir via t.Chdir so the relative probe lands on
	// our fixture.
	t.Chdir(dir)
	flags := &initFlags{}
	run, err := shouldRunPreflow(flags, dir)
	require.NoError(t, err)
	assert.False(t, run, "preflow must be skipped when an existing agent.yaml is present")
}

func TestShouldRunPreflow_SkipsWhenValidManifestExistsInSrc(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "agent.manifest.yaml"),
		[]byte(validManifestYAML),
		0o600,
	))
	t.Chdir(dir)
	flags := &initFlags{}
	run, err := shouldRunPreflow(flags, dir)
	require.NoError(t, err)
	assert.False(t, run, "preflow must be skipped when a valid agent.manifest.yaml is present")
}

func TestShouldRunPreflow_SkipsWhenAzureYamlInCwd(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "azure.yaml"),
		[]byte("name: existing-project\n"),
		0o600,
	))
	flags := &initFlags{}
	run, err := shouldRunPreflow(flags, dir)
	require.NoError(t, err)
	assert.False(t, run, "preflow must be skipped when azure.yaml is present in cwd")
}

func TestShouldRunPreflow_SkipsWhenAzureDirInCwd(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".azure"), 0o700))
	flags := &initFlags{}
	run, err := shouldRunPreflow(flags, dir)
	require.NoError(t, err)
	assert.False(t, run, "preflow must be skipped when .azure directory is present in cwd")
}

func TestShouldRunPreflow_RunsForGreenfieldInteractiveInit(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	flags := &initFlags{}
	run, err := shouldRunPreflow(flags, dir)
	require.NoError(t, err)
	assert.True(t, run, "preflow must run for a clean interactive greenfield init")
}

func TestHasExplicitInitFlags_NoFlagsSet(t *testing.T) {
	assert.False(t, hasExplicitInitFlags(&initFlags{}))
}

func TestHasExplicitInitFlags_NoiseFlagsDoNotCount(t *testing.T) {
	// --force and --env are intentionally NOT explicit-intent signals.
	// --force is overwrite consent for headless callers; --env just
	// binds an environment name. Neither tells us how the user wants
	// to author the agent, so they MUST NOT suppress the pre-flow on
	// their own.
	assert.False(t, hasExplicitInitFlags(&initFlags{force: true}))
	assert.False(t, hasExplicitInitFlags(&initFlags{env: "dev"}))
}

func TestHasExistingAzdSetup_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	has, err := hasExistingAzdSetup(dir)
	require.NoError(t, err)
	assert.False(t, has)
}

func TestHasExistingAzdSetup_DetectsAzureYml(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yml"), []byte(""), 0o600))
	has, err := hasExistingAzdSetup(dir)
	require.NoError(t, err)
	assert.True(t, has, "azure.yml (with .yml extension) must also count as existing setup")
}
